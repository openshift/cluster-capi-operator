package managedcache

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// ObjectBoundAccessManager manages caches and clients bound to objects.
// Each object instance will receive it's own cache and client instance.
type ObjectBoundAccessManager[T RefType] interface {
	manager.Runnable
	// Get returns a TrackingCache for the provided object if one exists.
	// If one does not exist, a new Cache is created and returned.
	Get(ctx context.Context, owner T) (Accessor, error)

	// GetWithUser returns a TrackingCache for the provided object if one exist.
	// If one does not exist, a new Cache is created and returned.
	// The additional user and usedFor parameters are used to automatically
	// stop informers for objects that are no longer watched.
	// After all users have called .FreeWithUser(), the cache itself will be stopped.
	GetWithUser(
		ctx context.Context, owner T,
		user client.Object, usedFor []client.Object,
	) (Accessor, error)

	// Free will stop and remove a TrackingCache for
	// the provided object, if one exists.
	Free(ctx context.Context, owner T) error

	// FreeWithUser informs the manager that the given user no longer needs
	// a cache scoped to owner T. If the cache has no active users, it will be stopped.
	FreeWithUser(ctx context.Context, owner T, user client.Object) error

	// Source returns a controller-runtime source to watch from a controller.
	Source(handler handler.EventHandler, predicates ...predicate.Predicate) source.Source

	GetWatchersForGVK(gvk schema.GroupVersionKind) (out []AccessManagerKey)

	CollectMetrics(ctx context.Context) (ObjectsPerOwnerPerGVK, error)
}

// ObjectsPerOwnerPerGVK is used to store data for collecting managed cache metrics.
type ObjectsPerOwnerPerGVK map[AccessManagerKey]map[schema.GroupVersionKind]int

// Accessor provides write and cached read access to the cluster.
type Accessor interface {
	client.Writer
	TrackingCache
}

// NewObjectBoundAccessManager returns a new ObjectBoundAccessManager for T.
func NewObjectBoundAccessManager[T RefType](
	log logr.Logger,
	mapConfig ConfigMapperFunc[T],
	baseRestConfig *rest.Config,
	baseCacheOptions cache.Options,
) ObjectBoundAccessManager[T] {
	return &objectBoundAccessManagerImpl[T]{
		log:              log.WithName("ObjectBoundAccessManager"),
		restMapper:       baseCacheOptions.Mapper,
		mapConfig:        mapConfig,
		baseRestConfig:   baseRestConfig,
		baseCacheOptions: baseCacheOptions,

		cacheSourcer: newCacheSource(),
		newClient:    client.New,

		accessors:         map[AccessManagerKey]accessorEntry{},
		accessorRequestCh: make(chan accessorRequest[T]),
		accessorStopCh:    make(chan accessorRequest[T]),
	}
}

// RefType constrains the owner type of an ObjectBoundAccessManager.
type RefType interface {
	client.Object
	comparable
}

// ConfigMapperFunc applies changes to rest.Config and cache.Options based on the given object.
type ConfigMapperFunc[T RefType] func(
	context.Context, T, *rest.Config, cache.Options) (*rest.Config, cache.Options, error)

type newClientFunc func(config *rest.Config, opts client.Options) (client.Client, error)

var _ ObjectBoundAccessManager[client.Object] = (*objectBoundAccessManagerImpl[client.Object])(nil)

type objectBoundAccessManagerImpl[T RefType] struct {
	log              logr.Logger
	restMapper       meta.RESTMapper
	mapConfig        ConfigMapperFunc[T]
	baseRestConfig   *rest.Config
	baseCacheOptions cache.Options

	cacheSourcer cacheSourcer
	newClient    newClientFunc

	accessorsLock     sync.RWMutex
	accessors         map[AccessManagerKey]accessorEntry
	accessorRequestCh chan accessorRequest[T]
	accessorStopCh    chan accessorRequest[T]
}

// AccessManagerKey is the key type on the ObjectBoundAccessManager's internal cache accessor map.
type AccessManagerKey struct {
	// UID ensures a re-created object also gets it's own cache.
	UID types.UID
	schema.GroupVersionKind
	client.ObjectKey
}

type accessorEntry struct {
	accessor Accessor
	users    map[AccessManagerKey]sets.Set[schema.GroupVersionKind]
	cancel   func()
}

type accessorRequest[T RefType] struct {
	owner      T
	user       client.Object
	gvks       sets.Set[schema.GroupVersionKind]
	responseCh chan<- accessorResponse
}

type accessorResponse struct {
	cache Accessor
	err   error
}

type cacheDone struct {
	err error
	key AccessManagerKey
}

// implements Accessor interface.
type accessor struct {
	TrackingCache
	client.Writer
}

func (m *objectBoundAccessManagerImpl[T]) Source(
	handler handler.EventHandler, predicates ...predicate.Predicate,
) source.Source {
	return m.cacheSourcer.Source(handler, predicates...)
}

func (m *objectBoundAccessManagerImpl[T]) Start(ctx context.Context) error {
	ctx = logr.NewContext(ctx, m.log)

	var wg sync.WaitGroup

	doneCh := make(chan cacheDone)
	defer close(doneCh)

	for {
		select {
		case done := <-doneCh:
			if err := m.handleCacheDone(ctx, done); err != nil {
				return err
			}

		case req := <-m.accessorRequestCh:
			cache, err := m.handleAccessorRequest(ctx, req, doneCh, &wg)
			req.responseCh <- accessorResponse{
				cache: cache,
				err:   err,
			}

		case req := <-m.accessorStopCh:
			req.responseCh <- accessorResponse{
				err: m.handleAccessorStop(ctx, req),
			}

		case <-ctx.Done():
			// Drain doneCh to ensure shutdown does not block.
			go func() {
				//nolint:revive
				for range doneCh {
				}
			}()

			// All sub-caches should also receive this signal and start to stop.
			// So we don't have to manually cancel caches individually.
			// Just wait for all to close to ensure they have gracefully shutdown.
			wg.Wait()

			return nil
		}
	}
}

func (m *objectBoundAccessManagerImpl[T]) handleCacheDone(
	_ context.Context, done cacheDone,
) error {
	m.accessorsLock.Lock()
	defer m.accessorsLock.Unlock()

	// Remove accessor from list.
	delete(m.accessors, done.key)

	if done.err != nil && !errors.Is(done.err, context.Canceled) {
		return fmt.Errorf("cache for Key %s crashed: %w", done.key, done.err)
	}

	return nil
}

func (m *objectBoundAccessManagerImpl[T]) handleAccessorStop(
	ctx context.Context, req accessorRequest[T],
) error {
	m.accessorsLock.Lock()
	defer m.accessorsLock.Unlock()

	cache, ok := m.accessors[toAccessManagerKey(req.owner)]
	if !ok {
		// nothing todo.
		return nil
	}

	if req.user != nil {
		delete(cache.users, toAccessManagerKey(req.user))
	}

	return m.gcCache(ctx, req.owner)
}

func (m *objectBoundAccessManagerImpl[T]) gcCache(ctx context.Context, owner T) error {
	log := logr.FromContextOrDiscard(ctx)

	key := toAccessManagerKey(owner)

	entry, ok := m.accessors[key]
	if !ok {
		return nil
	}

	if len(entry.users) == 0 {
		// no users left -> close
		log.Info("no users left, closing cache", "gvk", key.GroupVersionKind.String())
		entry.cancel()
		delete(m.accessors, key)

		return nil
	}

	inUseGVKs := sets.Set[schema.GroupVersionKind]{}
	for _, gvks := range entry.users {
		inUseGVKs.Insert(gvks.UnsortedList()...)
	}

	return entry.accessor.RemoveOtherInformers(ctx, inUseGVKs)
}

func (m *objectBoundAccessManagerImpl[T]) handleAccessorRequest(
	ctx context.Context, req accessorRequest[T],
	doneCh chan<- cacheDone, wg *sync.WaitGroup,
) (Accessor, error) {
	m.accessorsLock.Lock()
	defer m.accessorsLock.Unlock()

	log := logr.FromContextOrDiscard(ctx)
	log = log.WithValues(
		"ownerUID", req.owner.GetUID(),
	)
	ctx = logr.NewContext(ctx, log)
	key := toAccessManagerKey(req.owner)

	entry, ok := m.accessors[key]
	if ok {
		log.V(-1).Info("reusing cache for owner")

		if req.user != nil {
			entry.users[toAccessManagerKey(req.user)] = req.gvks
		}

		return entry.accessor, m.gcCache(ctx, req.owner)
	}

	restConfig, cacheOpts, err := m.mapConfig(
		ctx, req.owner, rest.CopyConfig(m.baseRestConfig), m.baseCacheOptions)
	if err != nil {
		return nil, fmt.Errorf("mapping rest.Config and cache.Options: %w", err)
	}

	ctrlcache, err := newTrackingCache(m.log, m.cacheSourcer, cache.New, restConfig, cacheOpts)
	if err != nil {
		return nil, fmt.Errorf("creating new Cache: %w", err)
	}

	client, err := m.newClient(restConfig, client.Options{
		Mapper:     m.baseCacheOptions.Mapper,
		HTTPClient: m.baseCacheOptions.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("creating new Client: %w", err)
	}

	// start cache
	ctx, cancel := context.WithCancel(ctx)
	a := &accessor{
		TrackingCache: ctrlcache,
		Writer:        client,
	}

	entry = accessorEntry{
		accessor: a,
		users:    map[AccessManagerKey]sets.Set[schema.GroupVersionKind]{},
		cancel:   cancel,
	}
	if req.user != nil {
		entry.users[toAccessManagerKey(req.user)] = req.gvks
		log = log.WithValues(
			"userUID", req.user.GetUID(),
			"usedForGVKs", req.gvks.UnsortedList(),
		)
	}

	m.accessors[key] = entry

	log.V(-1).Info("starting new cache")
	wg.Add(1)

	go func(ctx context.Context, doneCh chan<- cacheDone) {
		defer wg.Done()

		doneCh <- cacheDone{key: key, err: ctrlcache.Start(ctx)}
	}(ctx, doneCh)

	return a, nil
}

// request handles internal requests to start or stop an accessor.
func (m *objectBoundAccessManagerImpl[T]) request(
	ctx context.Context, accCh chan accessorRequest[T], req accessorRequest[T],
) (Accessor, error) {
	var zeroT T
	if req.owner == zeroT {
		panic("nil owner provided")
	}

	if len(req.owner.GetUID()) == 0 {
		panic("owner without UID set")
	}

	if req.user != nil {
		if len(req.user.GetUID()) == 0 {
			panic("user without UID set")
		}
	}

	responseCh := make(chan accessorResponse, 1)

	req.responseCh = responseCh
	select {
	case accCh <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// read response
	select {
	case resp := <-responseCh:
		return resp.cache, resp.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *objectBoundAccessManagerImpl[T]) Get(ctx context.Context, owner T) (Accessor, error) {
	return m.GetWithUser(ctx, owner, nil, nil)
}

func (m *objectBoundAccessManagerImpl[T]) GetWithUser(
	ctx context.Context, owner T,
	user client.Object, usedFor []client.Object,
) (Accessor, error) {
	gvks := sets.Set[schema.GroupVersionKind]{}

	for _, obj := range usedFor {
		gvk, err := apiutil.GVKForObject(obj, m.baseCacheOptions.Scheme)
		if err != nil {
			return nil, err
		}

		gvks.Insert(gvk)
	}

	req := accessorRequest[T]{
		owner: owner,
		user:  user,
		gvks:  gvks,
	}

	return m.request(ctx, m.accessorRequestCh, req)
}

func (m *objectBoundAccessManagerImpl[T]) Free(ctx context.Context, owner T) error {
	return m.FreeWithUser(ctx, owner, nil)
}

func (m *objectBoundAccessManagerImpl[T]) FreeWithUser(ctx context.Context, owner T, user client.Object) error {
	req := accessorRequest[T]{
		owner: owner,
		user:  user,
	}
	_, err := m.request(ctx, m.accessorStopCh, req)

	return err
}

func (m *objectBoundAccessManagerImpl[T]) GetWatchersForGVK(gvk schema.GroupVersionKind) (out []AccessManagerKey) {
	m.accessorsLock.RLock()
	defer m.accessorsLock.RUnlock()

	for k, a := range m.accessors {
		if !sets.New(a.accessor.GetGVKs()...).Has(gvk) {
			continue
		}

		out = append(out, k)
		for u := range a.users {
			out = append(out, u)
		}
	}

	return out
}

func toAccessManagerKey[T RefType](owner T) AccessManagerKey {
	return AccessManagerKey{
		UID:              owner.GetUID(),
		ObjectKey:        client.ObjectKeyFromObject(owner),
		GroupVersionKind: owner.GetObjectKind().GroupVersionKind(),
	}
}

func (m *objectBoundAccessManagerImpl[T]) CollectMetrics(ctx context.Context) (ObjectsPerOwnerPerGVK, error) {
	m.accessorsLock.RLock()
	defer m.accessorsLock.RUnlock()

	metrics := make(ObjectsPerOwnerPerGVK, len(m.accessors))

	for owner, entry := range m.accessors {
		objectsPerInformer, err := entry.accessor.GetObjectsPerInformer(ctx)
		if err != nil {
			return nil, fmt.Errorf("getting objects per informer with owner '%s': %w", owner.UID, err)
		}

		metrics[owner] = objectsPerInformer
	}

	return metrics, nil
}

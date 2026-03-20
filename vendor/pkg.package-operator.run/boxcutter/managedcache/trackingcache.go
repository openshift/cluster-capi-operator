package managedcache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// TrackingCache is a cache remembering what objects are being cached and
// allows to stop caches/informers that are no longer needed.
type TrackingCache interface {
	cache.Cache

	// Source returns a source to watch from a controller.
	Source(handler handler.EventHandler, predicates ...predicate.Predicate) source.Source

	// RemoveOtherInformers stops all informers that are not needed to watch the given list of object types.
	RemoveOtherInformers(ctx context.Context, gvks sets.Set[schema.GroupVersionKind]) error

	// GetGVKs returns a list of GVKs known by this trackingCache.
	GetGVKs() []schema.GroupVersionKind

	Watch(ctx context.Context, user client.Object, gvks sets.Set[schema.GroupVersionKind]) error
	Free(ctx context.Context, user client.Object) error

	GetObjectsPerInformer(ctx context.Context) (map[schema.GroupVersionKind]int, error)
}

type cacheSourcer interface {
	Source(handler handler.EventHandler, predicates ...predicate.Predicate) source.Source
	handleNewInformer(cache.Informer) error
}

// - informing the cacheSourcer about new informers.
type trackingCache struct {
	cache.Cache
	log          logr.Logger
	restMapper   meta.RESTMapper
	cacheSourcer cacheSourcer
	scheme       *runtime.Scheme

	// Guards against informers getting removed
	// while someone is still reading.
	accessLock sync.RWMutex

	cacheWatchErrorCh chan error
	gvkRequestCh      chan trackingCacheRequest
	informerSyncCh    chan informerSyncResponse
	knownInformers    sets.Set[schema.GroupVersionKind]

	// waitingForSync contains a slice of error channels for each GVK.
	// The error channels are waiting for the initial cache sync of the GVK's associated informer.
	waitingForSync map[schema.GroupVersionKind][]chan error

	// cacheWaitInFlight contains a stop channel for each GVK that is currently waiting
	// for an initial cache synchronization.
	// The stop channel can be closed to interrupt the wait operation.
	cacheWaitInFlight map[schema.GroupVersionKind]chan struct{}

	// Watches by user
	watchesByUser     map[AccessManagerKey]sets.Set[schema.GroupVersionKind]
	watchesByUserLock sync.Mutex
}

type informerSyncResponse struct {
	gvk schema.GroupVersionKind
	err error
}

type trackingCacheRequest struct {
	do func(ctx context.Context)
}

type newCacheFn func(cfg *rest.Config, opts cache.Options) (cache.Cache, error)

// NewTrackingCache returns a new TrackingCache instance.
func NewTrackingCache(log logr.Logger, config *rest.Config, opts cache.Options) (TrackingCache, error) {
	return newTrackingCache(log, newCacheSource(), cache.New, config, opts)
}

func newTrackingCache(
	log logr.Logger, cacheSourcer cacheSourcer, newCache newCacheFn,
	config *rest.Config, opts cache.Options,
) (TrackingCache, error) {
	wehc := &trackingCache{
		log:          log.WithName("TrackingCache"),
		restMapper:   opts.Mapper,
		cacheSourcer: cacheSourcer,
		scheme:       opts.Scheme,

		cacheWatchErrorCh: make(chan error),
		gvkRequestCh:      make(chan trackingCacheRequest),
		informerSyncCh:    make(chan informerSyncResponse),
		knownInformers:    sets.Set[schema.GroupVersionKind]{},
		waitingForSync:    map[schema.GroupVersionKind][]chan error{},
		cacheWaitInFlight: map[schema.GroupVersionKind]chan struct{}{},
		watchesByUser:     map[AccessManagerKey]sets.Set[schema.GroupVersionKind]{},
	}
	errHandler := opts.DefaultWatchErrorHandler
	opts.DefaultWatchErrorHandler = func(ctx context.Context, r *toolscache.Reflector, err error) {
		wehc.log.V(-1).Info("error in reflector", "typeDescription", r.TypeDescription(), "err", err)

		if errHandler != nil {
			errHandler(ctx, r, err)
		}

		if apistatus, ok := err.(apierrors.APIStatus); ok || errors.As(err, &apistatus) {
			if apistatus.Status().Details != nil {
				wehc.cacheWatchErrorCh <- err
			}
		}
	}

	c, err := newCache(config, opts)
	if err != nil {
		return nil, err
	}

	wehc.Cache = c

	return wehc, nil
}

// GetGVKs returns a list of GVKs known by this trackingCache.
func (c *trackingCache) GetGVKs() []schema.GroupVersionKind {
	c.accessLock.RLock()
	defer c.accessLock.RUnlock()

	return c.knownInformers.UnsortedList()
}

func (c *trackingCache) Source(handler handler.EventHandler, predicates ...predicate.Predicate) source.Source {
	return c.cacheSourcer.Source(handler, predicates...)
}

func (c *trackingCache) Start(ctx context.Context) error {
	ctx = logr.NewContext(ctx, c.log)

	cacheErrCh := make(chan error)

	go func() {
		cacheErrCh <- c.Cache.Start(ctx)
	}()

	for {
		select {
		case res := <-c.informerSyncCh:
			for _, errCh := range c.waitingForSync[res.gvk] {
				errCh <- res.err
			}

			delete(c.waitingForSync, res.gvk)
			delete(c.cacheWaitInFlight, res.gvk)

		case req := <-c.gvkRequestCh:
			req.do(ctx)

		case err := <-c.cacheWatchErrorCh:
			if err := c.handleCacheWatchError(ctx, err); err != nil {
				return err
			}

		case err := <-cacheErrCh:
			return err

		case <-ctx.Done():
			return nil
		}
	}
}

func (c *trackingCache) handleCacheWatchError(ctx context.Context, err error) error {
	apistatus, ok := err.(apierrors.APIStatus)
	apistatusOk := ok || errors.As(err, &apistatus)

	if !apistatusOk {
		// not a APIStatus error
		return nil
	}

	status := apistatus.Status()
	if status.Details == nil {
		// can't map error to waiting GVK.
		return nil
	}

	errorGVKs, rmerr := c.restMapper.KindsFor(schema.GroupVersionResource{
		Group:    status.Details.Group,
		Resource: status.Details.Kind,
	})
	if rmerr != nil {
		return rmerr
	}

	for _, errorGVK := range errorGVKs {
		if err := c.stopInformer(ctx, errorGVK, err); err != nil {
			return err
		}
	}

	return nil
}

func (c *trackingCache) ensureCacheSync(ctx context.Context, obj client.Object) error {
	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return err
	}

	if err := c.ensureCacheSyncForGVK(ctx, gvk); err != nil {
		return fmt.Errorf("ensuring cache sync for GVK: %w", err)
	}

	return nil
}

func (c *trackingCache) ensureCacheSyncList(ctx context.Context, list client.ObjectList) error {
	gvk, err := apiutil.GVKForObject(list, c.scheme)
	if err != nil {
		return err
	}
	// We need the non-list GVK, so chop off the "List" from the end of the kind.
	gvk.Kind = strings.TrimSuffix(gvk.Kind, "List")

	if err := c.ensureCacheSyncForGVK(ctx, gvk); err != nil {
		return fmt.Errorf("ensuring cache sync for (list) GVK: %w", err)
	}

	return nil
}

func (c *trackingCache) ensureCacheSyncForGVK(ctx context.Context, gvk schema.GroupVersionKind) error {
	errCh := make(chan error, 1)

	// This goroutine MUST NOT defer close(errCh),
	// because it's context could be canceled and the .Start()
	// goroutine could try to send a response, which makes it panic.

	c.gvkRequestCh <- trackingCacheRequest{
		do: func(ctx context.Context) {
			log := logr.FromContextOrDiscard(ctx).WithValues("gvk", gvk)
			// If others are already waiting on the same informer to sync.
			if _, ok := c.waitingForSync[gvk]; ok {
				// -> don't start another WaitForCacheSync and instead queue up in c.waitingForSync[gvk].
				log.V(-1).Info("new call waiting for WaitForCacheSync already in flight")

				c.waitingForSync[gvk] = append(c.waitingForSync[gvk], errCh)

				return
			}

			obj := &unstructured.Unstructured{}
			obj.SetGroupVersionKind(gvk)

			i, err := c.Cache.GetInformer(ctx, obj, cache.BlockUntilSynced(false))
			if err != nil {
				errCh <- err

				return
			}

			// If informer is new, store it in c.knownInformers and register event sources.
			isNewInformer := !c.knownInformers.Has(gvk)
			if isNewInformer {
				c.knownInformers.Insert(gvk)

				if err := c.cacheSourcer.handleNewInformer(i); err != nil {
					errCh <- err

					return
				}
			}

			// Return early if informer has already synced.
			if i.HasSynced() {
				errCh <- nil

				return
			}

			// Register request as waiting for sync.
			c.waitingForSync[gvk] = []chan error{errCh}

			stopCh := make(chan struct{})
			c.cacheWaitInFlight[gvk] = stopCh

			go func() {
				log.V(-1).Info("waiting for new informer to sync")

				if toolscache.WaitForCacheSync(stopCh, i.HasSynced) {
					log.V(-1).Info("informer synced successfully")

					c.informerSyncCh <- informerSyncResponse{gvk: gvk, err: nil}

					return
				}

				log.V(-1).Info("wait for informer sync canceled")

				c.informerSyncCh <- informerSyncResponse{gvk: gvk, err: context.Canceled}
			}()
		},
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *trackingCache) Get(
	ctx context.Context, key client.ObjectKey,
	obj client.Object, opts ...client.GetOption,
) error {
	c.accessLock.RLock()
	defer c.accessLock.RUnlock()

	if err := c.ensureCacheSync(ctx, obj); err != nil {
		return err
	}

	err := c.Cache.Get(ctx, key, obj, opts...)
	if err != nil {
		return fmt.Errorf("getting object: %w", err)
	}

	return nil
}

func (c *trackingCache) List(
	ctx context.Context, list client.ObjectList,
	opts ...client.ListOption,
) error {
	c.accessLock.RLock()
	defer c.accessLock.RUnlock()

	if err := c.ensureCacheSyncList(ctx, list); err != nil {
		return err
	}

	return c.Cache.List(ctx, list, opts...)
}

func (c *trackingCache) GetInformer(
	ctx context.Context, obj client.Object,
	opts ...cache.InformerGetOption,
) (cache.Informer, error) {
	c.accessLock.RLock()
	defer c.accessLock.RUnlock()

	if err := c.ensureCacheSync(ctx, obj); err != nil {
		return nil, err
	}

	return c.Cache.GetInformer(ctx, obj, opts...)
}

func (c *trackingCache) GetInformerForKind(
	ctx context.Context, gvk schema.GroupVersionKind,
	opts ...cache.InformerGetOption,
) (cache.Informer, error) {
	c.accessLock.RLock()
	defer c.accessLock.RUnlock()

	if err := c.ensureCacheSyncForGVK(ctx, gvk); err != nil {
		return nil, err
	}

	return c.Cache.GetInformerForKind(ctx, gvk, opts...)
}

func (c *trackingCache) RemoveInformer(ctx context.Context, obj client.Object) error {
	c.accessLock.Lock()
	defer c.accessLock.Unlock()

	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	c.gvkRequestCh <- trackingCacheRequest{
		do: func(ctx context.Context) {
			defer close(errCh)

			err := c.Cache.RemoveInformer(ctx, obj)
			if err != nil {
				errCh <- err

				return
			}

			errCh <- c.stopInformer(ctx, gvk, err)
		},
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stopInformer, only call this within the main control goroutine.
func (c *trackingCache) stopInformer(ctx context.Context, gvk schema.GroupVersionKind, cause error) error {
	log := logr.FromContextOrDiscard(ctx)
	log.V(-1).Info("stopping informer", "gvk", gvk)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)

	err := c.Cache.RemoveInformer(ctx, obj)
	if err != nil {
		return err
	}

	for _, errCh := range c.waitingForSync[gvk] {
		errCh <- cause
	}

	delete(c.waitingForSync, gvk)

	if _, ok := c.cacheWaitInFlight[gvk]; ok {
		close(c.cacheWaitInFlight[gvk])
		delete(c.cacheWaitInFlight, gvk)
	}

	c.knownInformers.Delete(gvk)

	return nil
}

func (c *trackingCache) RemoveOtherInformers(ctx context.Context, gvks sets.Set[schema.GroupVersionKind]) error {
	c.accessLock.Lock()
	defer c.accessLock.Unlock()

	return c.removeOtherInformers(ctx, gvks)
}

func (c *trackingCache) removeOtherInformers(ctx context.Context, gvksToKeep sets.Set[schema.GroupVersionKind]) error {
	errCh := make(chan error, 1)
	c.gvkRequestCh <- trackingCacheRequest{
		do: func(ctx context.Context) {
			defer close(errCh)

			log := logr.FromContextOrDiscard(ctx)

			gvksToStop := c.knownInformers.Difference(gvksToKeep).UnsortedList()
			if len(gvksToStop) > 0 {
				log.V(-1).Info("stopping informers", "gvks", gvksToStop)
			}

			var errs []error

			for _, gvkToStop := range gvksToStop {
				obj := &unstructured.Unstructured{}
				obj.SetGroupVersionKind(gvkToStop)

				if err := c.Cache.RemoveInformer(ctx, obj); err != nil {
					errs = append(errs, err)

					continue
				}

				if err := c.stopInformer(ctx, gvkToStop, nil); err != nil {
					errs = append(errs, err)

					continue
				}
			}

			errCh <- errors.Join(errs...)
		},
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *trackingCache) Watch(ctx context.Context, user client.Object, gvks sets.Set[schema.GroupVersionKind]) error {
	c.watchesByUserLock.Lock()
	defer c.watchesByUserLock.Unlock()

	if err := c.watch(ctx, gvks); err != nil {
		return err
	}

	c.watchesByUser[toAccessManagerKey(user)] = gvks

	c.accessLock.Lock()
	defer c.accessLock.Unlock()

	if err := c.gcUnusedGVK(ctx); err != nil {
		return err
	}

	return nil
}

func (c *trackingCache) watch(ctx context.Context, gvks sets.Set[schema.GroupVersionKind]) error {
	c.accessLock.RLock()
	defer c.accessLock.RUnlock()

	g, ctx := errgroup.WithContext(ctx)
	for _, gvk := range gvks.UnsortedList() {
		g.Go(func() error { return c.ensureCacheSyncForGVK(ctx, gvk) })
	}

	return g.Wait()
}

func (c *trackingCache) Free(ctx context.Context, user client.Object) error {
	c.watchesByUserLock.Lock()
	defer c.watchesByUserLock.Unlock()

	delete(c.watchesByUser, toAccessManagerKey(user))

	c.accessLock.Lock()
	defer c.accessLock.Unlock()

	if err := c.gcUnusedGVK(ctx); err != nil {
		return err
	}

	return nil
}

// gcUnusedGVK garbage collects informers that are no longer in use by any user by stopping them.
// Warning: Needs a lock on c.accessLock and c.watchesByUserLock.
func (c *trackingCache) gcUnusedGVK(ctx context.Context) error {
	gvksInUse := sets.New[schema.GroupVersionKind]()
	for _, gvks := range c.watchesByUser {
		gvksInUse.Insert(gvks.UnsortedList()...)
	}

	return c.removeOtherInformers(ctx, gvksInUse)
}

func (c *trackingCache) GetObjectsPerInformer(ctx context.Context) (map[schema.GroupVersionKind]int, error) {
	c.accessLock.RLock()
	defer c.accessLock.RUnlock()

	objects := make(map[schema.GroupVersionKind]int, len(c.knownInformers))

	for gvk := range c.knownInformers {
		listObj := &unstructured.UnstructuredList{}
		listObj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    gvk.Kind + "List",
		})

		if err := c.Cache.List(ctx, listObj); err != nil {
			return nil, fmt.Errorf("listing objects for GVK '%s': %w", gvk.String(), err)
		}

		objects[gvk] = len(listObj.Items)
	}

	return objects, nil
}

/*
Copyright 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package installer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorv1alpha1apply "github.com/openshift/client-go/operator/applyconfigurations/operator/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"pkg.package-operator.run/boxcutter"
	"pkg.package-operator.run/boxcutter/managedcache"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	controllerName = "InstallerController"
	clusterAPIName = "cluster"

	opresult = operatorstatus.ControllerResultGenerator(controllerName)
)

// InstallerController reconciles ClusterAPI revisions, using boxcutter to apply
// and manage the lifecycle of CAPI provider components on the cluster.
type InstallerController struct {
	client           client.Client
	trackingCache    managedcache.TrackingCache
	revisionEngine   *boxcutter.RevisionEngine
	providerProfiles []providerimages.ProviderImageManifests
	restMapper       meta.RESTMapper
}

// SetupWithManager creates the boxcutter dependencies and sets up the installer
// controller with the Manager. Additional sources may be provided to trigger
// reconciliation from external events (e.g. a channel source for testing).
func SetupWithManager(mgr ctrl.Manager, providerProfiles []providerimages.ProviderImageManifests, additionalSources ...source.Source) error {
	trackingCache, err := setupTrackingCache(mgr)
	if err != nil {
		return fmt.Errorf("unable to setup tracking cache: %w", err)
	}

	revisionEngine, err := setupRevisionEngine(mgr, trackingCache)
	if err != nil {
		return fmt.Errorf("unable to setup revision engine: %w", err)
	}

	c := &InstallerController{
		client:           mgr.GetClient(),
		trackingCache:    trackingCache,
		revisionEngine:   revisionEngine,
		providerProfiles: providerProfiles,
		restMapper:       mgr.GetRESTMapper(),
	}

	toClusterAPI := func(_ context.Context, _ client.Object) []reconcile.Request {
		return []reconcile.Request{{
			NamespacedName: client.ObjectKey{Name: clusterAPIName},
		}}
	}

	b := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&operatorv1alpha1.ClusterAPI{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == clusterAPIName
			}))).
		WatchesRawSource(
			c.trackingCache.Source(
				handler.EnqueueRequestsFromMapFunc(toClusterAPI),
				predicate.Or(
					// 'Spec' changes
					predicate.GenerationChangedPredicate{},
					noGenerationPredicate(),

					// Metadata changes
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},

					// Status changes where a probe becomes successful
					probeSucceededPredicate(allProbes()...),

					// We also rely on deletion events for teardown. These are
					// already handled implicitly by several of the above
					// predicates.
				),
			),
		)

	for _, src := range additionalSources {
		b = b.WatchesRawSource(src)
	}

	if err := b.Complete(c); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

func setupTrackingCache(mgr ctrl.Manager) (managedcache.TrackingCache, error) {
	// Configure cache to watch only objects with our label. The label is
	// applied by the revision generator.
	managedByReq, err := labels.NewRequirement(revisiongenerator.ManagedLabelKey, selection.Exists, nil)
	if err != nil {
		return nil, fmt.Errorf("creating managed-by label requirement: %w", err)
	}

	trackingCache, err := managedcache.NewTrackingCache(
		mgr.GetLogger(),
		mgr.GetConfig(),
		cache.Options{
			Scheme:               mgr.GetScheme(),
			DefaultLabelSelector: labels.NewSelector().Add(*managedByReq),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create tracking cache: %w", err)
	}

	if err := mgr.Add(trackingCache); err != nil {
		return nil, fmt.Errorf("unable to add tracking cache to manager: %w", err)
	}

	return trackingCache, nil
}

func setupRevisionEngine(mgr ctrl.Manager, trackingCache managedcache.TrackingCache) (*boxcutter.RevisionEngine, error) {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
	if err != nil {
		return nil, fmt.Errorf("unable to create discovery client: %w", err)
	}

	revisionEngine, err := boxcutter.NewRevisionEngine(boxcutter.RevisionEngineOptions{
		Scheme:           mgr.GetScheme(),
		FieldOwner:       string(operatorstatus.CAPIFieldOwner(controllerName)),
		SystemPrefix:     operatorstatus.CAPIOperatorIdentifierDomain,
		DiscoveryClient:  discoveryClient,
		RestMapper:       mgr.GetRESTMapper(),
		Writer:           mgr.GetClient(),
		Reader:           trackingCache,
		UnfilteredReader: mgr.GetAPIReader(),
	})
	if err != nil {
		return nil, fmt.Errorf("unable to create revision engine: %w", err)
	}

	return revisionEngine, nil
}

// Reconcile handles applying and managing revisions on the cluster.
func (c *InstallerController) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)
	log.Info("Reconciling installer revisions")

	reconcileResult := c.reconcile(ctx, log)

	if err := reconcileResult.WriteClusterOperatorStatus(ctx, log, c.client); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to write conditions: %w", err)
	}

	log.Info("Reconcile finished")

	return reconcileResult.Result()
}

func (c *InstallerController) reconcile(ctx context.Context, log logr.Logger) operatorstatus.ReconcileResult {
	// Get ClusterAPI singleton
	clusterAPI := &operatorv1alpha1.ClusterAPI{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI); err != nil {
		if apierrors.IsNotFound(err) {
			return opresult.WaitingOnExternal("ClusterAPI")
		}

		return opresult.Error(fmt.Errorf("fetching ClusterAPI: %w", err))
	}

	if len(clusterAPI.Status.Revisions) == 0 {
		if err := writeRelatedObjects(ctx, c.client, staticRelatedObjects()); err != nil {
			return opresult.Error(fmt.Errorf("writing relatedObjects: %w", err))
		}

		return opresult.WaitingOnExternal("ClusterAPI revisions")
	}

	// Read cluster-wide proxy configuration
	var renderOpts []revisiongenerator.RevisionRenderOption

	proxy, err := util.GetProxy(ctx, c.client)
	if err != nil {
		return opresult.Error(fmt.Errorf("fetching proxy: %w", err))
	}

	if envVars := util.ProxyEnvVars(proxy); len(envVars) > 0 {
		log.Info("Injecting proxy configuration into provider manifests",
			"httpProxy", proxy.Status.HTTPProxy, "httpsProxy", proxy.Status.HTTPSProxy, "noProxy", proxy.Status.NoProxy)

		renderOpts = append(renderOpts, revisiongenerator.WithProxyConfig(envVars))
	}

	revisionReconciler := newRevisionReconciler(c, log, renderOpts...)
	reconciledRevision, messages, errs := revisionReconciler.reconcile(ctx, clusterAPI.Status.Revisions)

	// Write relatedObjects via non-SSA merge patch so the SSA conditions
	// write never claims ownership of the relatedObjects field.
	relatedObjects := mergeRelatedObjects(staticRelatedObjects(), revisionReconciler.dynamicRelatedObjects())
	if err := writeRelatedObjects(ctx, c.client, relatedObjects); err != nil {
		return opresult.Error(fmt.Errorf("writing relatedObjects: %w", err))
	}

	// Update tracking cache watches for all current revisions
	if err := c.updateWatches(ctx, log, clusterAPI, revisionReconciler.gvks); err != nil {
		return opresult.Error(err)
	}

	if reconciledRevision != nil {
		return c.success(ctx, log, clusterAPI, *reconciledRevision, errs)
	}

	if len(errs) > 0 {
		return c.error(errs)
	}

	return opresult.Progressing(strings.Join(messages, "\n"))
}

func (c *InstallerController) success(ctx context.Context, log logr.Logger, clusterAPI *operatorv1alpha1.ClusterAPI, reconciledRevision operatorv1alpha1.RevisionName, errs []error) operatorstatus.ReconcileResult {
	if len(errs) > 0 {
		log.Error(errors.Join(errs...), "Ignoring errors because the reconciliation is complete")
	}

	// Write the current revision to the ClusterAPI status in its own SSA transaction
	if err := c.writeCurrentRevision(ctx, clusterAPI, reconciledRevision); err != nil {
		return opresult.Error(fmt.Errorf("writing current revision: %w", err))
	}

	return opresult.Success()
}

func (c *InstallerController) error(errs []error) operatorstatus.ReconcileResult {
	// Filter errors into terminal and non-terminal errors.
	// Unwraps the terminal errors to get the underlying errors.
	var (
		terminalErrors    []error
		nonTerminalErrors []error
	)

	for _, err := range errs {
		if errors.Is(err, reconcile.TerminalError(nil)) {
			terminalErrors = append(terminalErrors, err)
		} else {
			nonTerminalErrors = append(nonTerminalErrors, err)
		}
	}

	// If there were any non-terminal errors, convert all terminal errors to
	// non-terminal errors so controller-runtime will retry the reconciliation
	if len(nonTerminalErrors) > 0 {
		unwrappedTerminalErrors := util.SliceMap(terminalErrors, func(err error) error {
			for errors.Is(err, reconcile.TerminalError(nil)) {
				err = errors.Unwrap(err)
			}

			return err
		})
		nonTerminalErrors = append(nonTerminalErrors, unwrappedTerminalErrors...)

		return opresult.Error(fmt.Errorf("reconciling revisions: %w", errors.Join(nonTerminalErrors...)))
	}

	return opresult.NonRetryableError(fmt.Errorf("reconciling revisions: %w", errors.Join(errs...)))
}

func (c *InstallerController) updateWatches(ctx context.Context, log logr.Logger, clusterAPI *operatorv1alpha1.ClusterAPI, allGVKs sets.Set[schema.GroupVersionKind]) error {
	log.Info("Watching GVKs", "gvks", strings.Join(util.SliceMap(allGVKs.UnsortedList(), func(gvk schema.GroupVersionKind) string {
		return gvk.String()
	}), ", "))

	err := c.trackingCache.Watch(ctx, clusterAPI, allGVKs)
	if err != nil {
		return fmt.Errorf("watching GVKs: %w", err)
	}

	return nil
}

func (c *InstallerController) writeCurrentRevision(ctx context.Context, clusterAPI *operatorv1alpha1.ClusterAPI, revisionName operatorv1alpha1.RevisionName) error {
	applyConfig := operatorv1alpha1apply.ClusterAPI(clusterAPIName).
		WithUID(clusterAPI.UID).
		WithStatus(operatorv1alpha1apply.ClusterAPIStatus().
			WithCurrentRevision(revisionName),
		)

	patch := util.ApplyConfigPatch(applyConfig)
	if err := c.client.Status().Patch(ctx, clusterAPI, patch, operatorstatus.CAPIFieldOwner(controllerName), client.ForceOwnership); err != nil {
		return fmt.Errorf("updating ClusterAPI status: %w", err)
	}

	return nil
}

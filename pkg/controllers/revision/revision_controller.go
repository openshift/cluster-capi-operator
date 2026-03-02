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

package revision

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorv1alpha1ac "github.com/openshift/client-go/operator/applyconfigurations/operator/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	controllerName = "RevisionController"

	clusterAPIName      = "cluster"
	infrastructureName  = "cluster"
	maxRevisionsAllowed = 16

	opresult = operatorstatus.ControllerResultGenerator(controllerName)
)

var (
	errMaxRevisionsAllowed = errors.New("max number of revisions reached")
)

// RevisionController reconciles the ClusterAPI singleton to create and track revisions
// based on provider images.
type RevisionController struct {
	client.Client
	ProviderProfiles []providerimages.ProviderImageManifests
	ReleaseVersion   string
}

// Reconcile handles creating revisions in the ClusterAPI singleton status.
func (r *RevisionController) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)
	log.Info("Reconciling ClusterAPI revisions")

	reconcileResult := r.reconcile(ctx, log)

	if err := reconcileResult.WriteClusterOperatorConditions(ctx, log, r.Client); err != nil {
		// We deliberately do not return the reconcile error here. This error is
		// guaranteed to force a requeue, which we want because we need to write
		// our status. However, if the reconcile error contained a terminal
		// error it would prevent a requeue.
		return ctrl.Result{}, fmt.Errorf("failed to write conditions: %w", err)
	}

	return reconcileResult.Result()
}

func (r *RevisionController) reconcile(ctx context.Context, log logr.Logger) operatorstatus.ReconcileResult {
	// Generate a desired revision from the current state
	desiredRevision, result := r.generateDesiredRevision(ctx)
	if result != nil {
		return *result
	}

	// Get ClusterAPI singleton
	clusterAPI := &operatorv1alpha1.ClusterAPI{}
	if err := r.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI); err != nil {
		if apierrors.IsNotFound(err) {
			return opresult.WaitingOnExternal("ClusterAPI not found")
		}

		return opresult.Error(fmt.Errorf("fetching ClusterAPI: %w", err))
	}

	// Create a reverse sorted, merged list of revisions. It will prepend the
	// new revision if necessary. Note that the latest revision is always
	// first, and there is guaranteed to be at least one revision.
	apiRevisions, err := r.mergeRevisions(log, clusterAPI.Status.Revisions, desiredRevision)
	if err != nil {
		return opresult.Error(fmt.Errorf("error merging revisions: %w", err))
	}

	// We can't proceed if we exceed the max number of revisions. In normal
	// operation we don't expect to see more than 2 revisions. 16 revisions
	// would indicate a bug or some highly unfavourable environmental condition,
	// so we should stop. There is no safe way to automatically prune revisions
	// in this case. This requires manual intervention.
	if len(apiRevisions) > maxRevisionsAllowed {
		return opresult.NonRetryableError(errMaxRevisionsAllowed)
	}

	// Trim old revisions if the current revision is up to date
	if len(apiRevisions) > 0 && clusterAPI.Status.CurrentRevision == apiRevisions[0].Name {
		apiRevisions = apiRevisions[:1]
	}

	if err := r.writeRevisions(ctx, log, clusterAPI, apiRevisions); err != nil {
		return opresult.Error(fmt.Errorf("writing new revision: %w", err))
	}

	return opresult.Success()
}

func (r *RevisionController) generateDesiredRevision(ctx context.Context) (revisiongenerator.RenderedRevision, *operatorstatus.ReconcileResult) {
	infra := &configv1.Infrastructure{}
	if err := r.Get(ctx, client.ObjectKey{Name: infrastructureName}, infra); err != nil {
		return nil, opresult.ErrorP(fmt.Errorf("fetching infrastructure: %w", err))
	}

	if infra.Status.PlatformStatus == nil {
		return nil, opresult.WaitingOnExternalP("Infrastructure PlatformStatus")
	}

	// Build ordered component list from provider metadata
	providerComponents := r.buildComponentList(infra.Status.PlatformStatus.Type)

	revision, err := revisiongenerator.NewRenderedRevision(providerComponents)
	if err != nil {
		return nil, opresult.ErrorP(fmt.Errorf("error creating rendered revision: %w", err))
	}

	return revision, nil
}

func (r *RevisionController) mergeRevisions(log logr.Logger, apiRevisions []operatorv1alpha1.ClusterAPIInstallerRevision, desiredRevision revisiongenerator.RenderedRevision) ([]operatorv1alpha1.ClusterAPIInstallerRevision, error) {
	// If there's no current revision we have nothing to merge
	if desiredRevision == nil {
		return apiRevisions, nil
	}

	nextRevisionIndex := int64(1)

	// If there are existing revisions, we don't need to merge if the latest revision has the same content ID
	if len(apiRevisions) > 0 {
		contentID, err := desiredRevision.ContentID()
		if err != nil {
			return nil, fmt.Errorf("error getting content ID: %w", err)
		}

		// Reverse sort existing revisions, both for output and to ensure that the latest revision is first
		apiRevisions = slices.Clone(apiRevisions)
		slices.SortFunc(apiRevisions, func(a, b operatorv1alpha1.ClusterAPIInstallerRevision) int {
			return cmp.Compare(b.Revision, a.Revision)
		})

		latestRevision := apiRevisions[0]

		if latestRevision.ContentID == contentID {
			log.Info("No new revision needed")
			return apiRevisions, nil
		}

		nextRevisionIndex = latestRevision.Revision + 1
	}

	// Generate an API revision for the new revision and prepend it to the list of existing revisions
	newRevision, err := desiredRevision.ForInstall(r.ReleaseVersion, nextRevisionIndex)
	if err != nil {
		return nil, fmt.Errorf("error creating installer revision: %w", err)
	}

	newAPIRevision, err := newRevision.ToAPIRevision()
	if err != nil {
		return nil, fmt.Errorf("error converting installer revision to API revision: %w", err)
	}

	log.Info("Creating new revision",
		"revisionName", newAPIRevision.Name,
		"revisionIndex", newAPIRevision.Revision,
		"contentID", newAPIRevision.ContentID,
	)

	newRevisions := make([]operatorv1alpha1.ClusterAPIInstallerRevision, 0, len(apiRevisions)+1)
	newRevisions = append(newRevisions, newAPIRevision)
	newRevisions = append(newRevisions, apiRevisions...)

	return newRevisions, nil
}

func (r *RevisionController) writeRevisions(ctx context.Context, log logr.Logger, clusterAPI *operatorv1alpha1.ClusterAPI, apiRevisions []operatorv1alpha1.ClusterAPIInstallerRevision) error {
	if len(apiRevisions) == 0 {
		log.Info("No revisions to write")
		return nil
	}

	revisionACs, err := revisiongenerator.RevisionsToApplyConfig(apiRevisions)
	if err != nil {
		return fmt.Errorf("error converting revisions to apply config: %w", err)
	}

	revisionACPtrs := util.SliceMap(revisionACs, func(ac operatorv1alpha1ac.ClusterAPIInstallerRevisionApplyConfiguration) *operatorv1alpha1ac.ClusterAPIInstallerRevisionApplyConfiguration {
		return &ac
	})

	applyConfig := operatorv1alpha1ac.ClusterAPI(clusterAPI.Name).
		WithStatus(operatorv1alpha1ac.ClusterAPIStatus().
			WithDesiredRevision(apiRevisions[0].Name).
			WithRevisions(revisionACPtrs...))

	if err := r.Status().Patch(ctx, clusterAPI, util.ApplyConfigPatch(applyConfig),
		operatorstatus.CAPIFieldOwner(controllerName), client.ForceOwnership); err != nil {
		return fmt.Errorf("updating ClusterAPI status: %w", err)
	}

	return nil
}

// buildComponentList builds an ordered list of provider components for the given platform.
// Components are ordered by: core+global, core+platform, infra+global, infra+platform
// Providers that don't match the current platform are filtered out.
func (r *RevisionController) buildComponentList(platform configv1.PlatformType) []providerimages.ProviderImageManifests {
	// Iterate over only providers that have either no platform restriction, or
	// match the current platform.
	componentsByPlatform := util.IterFilter(slices.Values(r.ProviderProfiles), func(provider providerimages.ProviderImageManifests) bool {
		return provider.OCPPlatform == "" || provider.OCPPlatform == platform
	})

	// Sort components by install order, then platform, then name.
	return slices.SortedFunc(componentsByPlatform, func(a, b providerimages.ProviderImageManifests) int {
		// Sort by install order
		if c := cmp.Compare(a.InstallOrder, b.InstallOrder); c != 0 {
			return c
		}

		// Sort by platform within the same install order. This intentionally
		// puts components with no platform before platform-specific components.
		if c := cmp.Compare(a.OCPPlatform, b.OCPPlatform); c != 0 {
			return c
		}

		// Sort by name as a tie-breaker
		return strings.Compare(a.Name, b.Name)
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *RevisionController) SetupWithManager(mgr ctrl.Manager) error {
	isInfrastructureReady := func(obj client.Object) bool {
		if obj == nil {
			return false
		}

		infra, ok := obj.(*configv1.Infrastructure)
		if !ok {
			return false
		}

		return infra.Status.PlatformStatus != nil
	}

	toClusterAPI := func(context.Context, client.Object) []reconcile.Request {
		return []reconcile.Request{{
			NamespacedName: client.ObjectKey{Name: clusterAPIName},
		}}
	}

	err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&operatorv1alpha1.ClusterAPI{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == clusterAPIName
			}))).
		Watches(&configv1.Infrastructure{},
			handler.EnqueueRequestsFromMapFunc(toClusterAPI),
			builder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					return isInfrastructureReady(e.Object)
				},

				UpdateFunc: func(e event.UpdateEvent) bool {
					// Only enqueue if the infrastructure is ready and was not ready before
					return isInfrastructureReady(e.ObjectNew) && !isInfrastructureReady(e.ObjectOld)
				},
			}),
		).
		Complete(r)

	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

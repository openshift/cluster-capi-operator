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
)

const (
	controllerName = "RevisionController"

	clusterAPIName       = "cluster"
	infrastructureName   = "cluster"
	clusterOperatorName  = "cluster-api"
	maxRevisionNameLen   = 255
	revisionContentIDLen = 8
	maxRevisionsAllowed  = 16

	// ssaFieldOwner is the field manager name for Server-Side Apply patches to ClusterOperator conditions.
	ssaFieldOwner = "capi-operator.openshift.io/revision-controller"
)

var (
	errMaxRevisionsAllowed = errors.New("max number of revisions reached")
	opresult               = operatorstatus.NewControllerResultGenerator(controllerName, ssaFieldOwner)
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
		return ctrl.Result{}, errors.Join(reconcileResult.Error(), fmt.Errorf("failed to write conditions: %w", err))
	}

	return reconcileResult.Result()
}

func (r *RevisionController) reconcile(ctx context.Context, log logr.Logger) operatorstatus.ReconcileResult {
	platform, err := r.getPlatform(ctx)
	if err != nil {
		return opresult.Error(fmt.Errorf("getting platform: %w", err))
	}

	if platform == "" {
		return opresult.WaitingOnExternal("Infrastructure PlatformStatus")
	}

	// Build ordered component list from provider metadata
	providerComponents := r.buildComponentList(platform)
	if len(providerComponents) == 0 {
		log.Info("No components for current platform", "platform", platform)
		return opresult.Success()
	}

	// Build a rendered revision from the current provider components for
	// comparison against the latest revision.
	revision, err := revisiongenerator.NewRenderedRevision(providerComponents)
	if err != nil {
		return opresult.Error(fmt.Errorf("error creating rendered revision: %w", err))
	}

	contentID, err := revision.ContentID()
	if err != nil {
		return opresult.Error(fmt.Errorf("error getting content ID: %w", err))
	}

	// Get ClusterAPI singleton
	clusterAPI := &operatorv1alpha1.ClusterAPI{}
	if err := r.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI); err != nil {
		if apierrors.IsNotFound(err) {
			return opresult.WaitingOnExternal("ClusterAPI not found")
		}

		return opresult.Error(fmt.Errorf("fetching ClusterAPI: %w", err))
	}

	// We need a new revision if the latest revision has a different contentID
	latestAPIRevision := getLatestRevision(clusterAPI.Status.Revisions)
	if latestAPIRevision != nil && latestAPIRevision.ContentID == contentID {
		log.Info("No new revision needed", "contentID", contentID)
		return opresult.Success()
	}

	// We can't proceed if we're at the max number of revisions. In normal
	// operation we don't expect to see more than 2 revisions. 16 revisions
	// would indicate a bug or some highly unfavourable environmental condition,
	// so we should stop. There is no safe way to automatically prune revisions
	// in this case. This requires manual intervention.
	if len(clusterAPI.Status.Revisions) >= maxRevisionsAllowed {
		return opresult.NonRetryableError(errMaxRevisionsAllowed)
	}

	newRevision, err := r.writeNewRevision(ctx, clusterAPI, latestAPIRevision, revision)
	if err != nil {
		return opresult.Error(fmt.Errorf("writing new revision: %w", err))
	}

	log.Info("Created new revision",
		"revisionName", newRevision.Name,
		"revisionIndex", newRevision.Revision,
		"contentID", contentID)

	return opresult.Success()
}

func (r *RevisionController) writeNewRevision(ctx context.Context, clusterAPI *operatorv1alpha1.ClusterAPI, latestAPIRevision *operatorv1alpha1.ClusterAPIInstallerRevision, revision revisiongenerator.RenderedRevision) (*operatorv1alpha1.ClusterAPIInstallerRevision, error) {
	// Calculate the next revision number.
	var nextRevisionIndex int64 = 1
	if latestAPIRevision != nil {
		nextRevisionIndex = latestAPIRevision.Revision + 1
	}

	newRevision, err := revision.ToAPIRevision(r.ReleaseVersion, nextRevisionIndex)
	if err != nil {
		return nil, fmt.Errorf("error converting rendered revision to API revision: %w", err)
	}

	// XXX: This is wrong, because it will conflict with the installer
	// controller. The resourceVersion will prevent incorrect behaviour, but we
	// should convert this to SSA.
	clusterAPI.Status.Revisions = append(clusterAPI.Status.Revisions, newRevision)
	clusterAPI.Status.DesiredRevision = newRevision.Name

	if err := r.Status().Update(ctx, clusterAPI); err != nil {
		return nil, fmt.Errorf("updating ClusterAPI status: %w", err)
	}

	return &newRevision, nil
}

func (r *RevisionController) getPlatform(ctx context.Context) (configv1.PlatformType, error) {
	infra := &configv1.Infrastructure{}
	if err := r.Get(ctx, client.ObjectKey{Name: infrastructureName}, infra); err != nil {
		return "", fmt.Errorf("fetching infrastructure: %w", err)
	}

	if infra.Status.PlatformStatus == nil {
		return "", nil
	}

	return infra.Status.PlatformStatus.Type, nil
}

func getLatestRevision(revisions []operatorv1alpha1.ClusterAPIInstallerRevision) *operatorv1alpha1.ClusterAPIInstallerRevision {
	var latest *operatorv1alpha1.ClusterAPIInstallerRevision

	for i := range revisions {
		rev := &revisions[i]
		if latest == nil || rev.Revision > latest.Revision {
			latest = rev
		}
	}

	return latest
}

// buildComponentList builds an ordered list of provider components for the given platform.
// Components are ordered by: core+global, core+platform, infra+global, infra+platform
// Providers that don't match the current platform are filtered out.
func (r *RevisionController) buildComponentList(platform configv1.PlatformType) []providerimages.ProviderImageManifests {
	// Iterate over only providers that have either no platform restriction, or
	// match the current platform.
	componentsByPlatform := func(yield func(providerimages.ProviderImageManifests) bool) {
		for _, provider := range r.ProviderProfiles {
			if provider.OCPPlatform == "" || provider.OCPPlatform == platform {
				if !yield(provider) {
					return
				}
			}
		}
	}

	// Sort components by install order, then platform, then name.
	return slices.SortedStableFunc(componentsByPlatform, func(a, b providerimages.ProviderImageManifests) int {
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
	toClusterOperator := func(ctx context.Context, obj client.Object) []reconcile.Request {
		return []reconcile.Request{{
			NamespacedName: client.ObjectKey{Name: clusterOperatorName},
		}}
	}

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

	err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&operatorv1alpha1.ClusterAPI{},
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == clusterAPIName
			}))).
		Watches(&configv1.Infrastructure{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
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

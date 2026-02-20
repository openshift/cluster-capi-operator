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
	"iter"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/util"
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

	// Condition types for the RevisionController, prefixed to avoid collision with other controllers.
	conditionTypeProgressing configv1.ClusterStatusConditionType = "RevisionControllerProgressing"
	conditionTypeDegraded    configv1.ClusterStatusConditionType = "RevisionControllerDegraded"

	// Condition reasons.
	conditionReasonSuccess           = "Success"
	conditionReasonWaitingOnExternal = "WaitingOnExternal"
	conditionReasonEphemeralError    = "EphemeralError"
	conditionReasonNonRetryableError = "NonRetryableError"
	conditionReasonPersistentError   = "PersistentError"
	conditionReasonProgressing       = "Progressing"
)

type reconcileResult struct {
	progressingReason string
	message           string
	error             error
}

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

	if err := r.updateClusterOperatorConditions(ctx, log, reconcileResult); err != nil {
		return ctrl.Result{}, errors.Join(reconcileResult.error, fmt.Errorf("failed to update ClusterOperator conditions: %w", err))
	}

	if reconcileResult.progressingReason == conditionReasonNonRetryableError {
		// Don't requeue for non-retryable errors
		log.Error(reconcileResult.error, "Not requeuing for non-retryable error")
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, reconcileResult.error
}

func (r *RevisionController) reconcile(ctx context.Context, log logr.Logger) reconcileResult {
	platform, err := r.getPlatform(ctx)
	if err != nil {
		return reconcileResult{progressingReason: conditionReasonEphemeralError, error: fmt.Errorf("getting platform: %w", err)}
	}

	if platform == "" {
		return reconcileResult{progressingReason: conditionReasonWaitingOnExternal, message: "Waiting for Infrastructure PlatformStatus"}
	}

	// Build ordered component list from provider metadata
	providerComponents := r.buildComponentList(platform)
	if len(providerComponents) == 0 {
		log.Info("No components for current platform", "platform", platform)
		return reconcileResult{}
	}

	// Build a rendered revision from the current provider components for
	// comparison against the latest revision.
	revision, err := revisiongenerator.NewRenderedRevision(providerComponents)
	if err != nil {
		return reconcileResult{progressingReason: conditionReasonEphemeralError, error: fmt.Errorf("error creating rendered revision: %w", err)}
	}

	contentID, err := revision.ContentID()
	if err != nil {
		return reconcileResult{progressingReason: conditionReasonEphemeralError, error: fmt.Errorf("error getting content ID: %w", err)}
	}

	// Get ClusterAPI singleton
	clusterAPI := &operatorv1alpha1.ClusterAPI{}
	if err := r.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcileResult{progressingReason: conditionReasonWaitingOnExternal, message: "ClusterAPI not found"}
		}

		return reconcileResult{progressingReason: conditionReasonEphemeralError, error: fmt.Errorf("fetching ClusterAPI: %w", err)}
	}

	// We need a new revision if the latest revision has a different contentID
	latestAPIRevision := getLatestRevision(clusterAPI.Status.Revisions)
	if latestAPIRevision != nil && latestAPIRevision.ContentID == contentID {
		log.Info("No new revision needed", "contentID", contentID)
		return reconcileResult{}
	}

	// We can't proceed if we're at the max number of revisions. In normal
	// operation we don't expect to see more than 2 revisions. 16 revisions
	// would indicate a bug or some highly unfavourable environmental condition,
	// so we should stop. There is no safe way to automatically prune revisions
	// in this case. This requires manual intervention.
	if len(clusterAPI.Status.Revisions) >= maxRevisionsAllowed {
		log.Error(errMaxRevisionsAllowed, "max number of revisions reached")
		return reconcileResult{progressingReason: conditionReasonNonRetryableError, error: errMaxRevisionsAllowed}
	}

	newRevision, err := r.writeNewRevision(ctx, clusterAPI, latestAPIRevision, revision)
	if err != nil {
		return reconcileResult{progressingReason: conditionReasonEphemeralError, error: fmt.Errorf("writing new revision: %w", err)}
	}

	log.Info("Created new revision",
		"revisionName", newRevision.Name,
		"revisionIndex", newRevision.Revision,
		"contentID", contentID)

	return reconcileResult{}
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
	// Select only proviers that have either no platform restriction, or match the current platform.
	componentsByPlatform := filterComponentsByPlatform(r.ProviderProfiles, platform)

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

// toAPIComponents converts provider image manifests to API component format.
func toAPIComponents(providers []providerimages.ProviderImageManifests) []operatorv1alpha1.ClusterAPIInstallerComponent {
	components := make([]operatorv1alpha1.ClusterAPIInstallerComponent, 0, len(providers))
	for _, p := range providers {
		components = append(components, operatorv1alpha1.ClusterAPIInstallerComponent{
			Type: operatorv1alpha1.InstallerComponentTypeImage,
			Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
				Ref:     operatorv1alpha1.ImageDigestFormat(p.ImageRef),
				Profile: p.Profile,
			},
		})
	}

	return components
}

// filterComponentsByPlatform returns an iterator that yields only providers matching the given platform.
// A provider matches if it has no platform restriction (global) or matches the specified platform.
func filterComponentsByPlatform(providers []providerimages.ProviderImageManifests, platform configv1.PlatformType) iter.Seq[providerimages.ProviderImageManifests] {
	return func(yield func(providerimages.ProviderImageManifests) bool) {
		for _, provider := range providers {
			if provider.OCPPlatform == "" || provider.OCPPlatform == platform {
				if !yield(provider) {
					return
				}
			}
		}
	}
}

// findLatestRevision returns the revision with the highest revision number, or nil if none exist.
func findLatestRevision(clusterAPI *operatorv1alpha1.ClusterAPI) *operatorv1alpha1.ClusterAPIInstallerRevision {
	if len(clusterAPI.Status.Revisions) == 0 {
		return nil
	}

	var latest *operatorv1alpha1.ClusterAPIInstallerRevision

	for i := range clusterAPI.Status.Revisions {
		rev := &clusterAPI.Status.Revisions[i]
		if latest == nil || rev.Revision > latest.Revision {
			latest = rev
		}
	}

	return latest
}

// buildRevisionName constructs a revision name from version, contentID, and number.
func (r *RevisionController) buildRevisionName(version, contentID string, number int64) string {
	// Format: <version>-<contentID[:8]>-<number>
	shortContentID := contentID
	if len(shortContentID) > revisionContentIDLen {
		shortContentID = shortContentID[:revisionContentIDLen]
	}

	name := fmt.Sprintf("%s-%s-%d", version, shortContentID, number)

	// Truncate if necessary
	if len(name) > maxRevisionNameLen {
		name = name[:maxRevisionNameLen]
	}

	return name
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

// updateClusterOperatorConditions updates the RevisionController conditions on the ClusterOperator.
func (r *RevisionController) updateClusterOperatorConditions(ctx context.Context, log logr.Logger, result reconcileResult) error {
	// Get the ClusterOperator
	co := &configv1.ClusterOperator{}
	if err := r.Get(ctx, client.ObjectKey{Name: clusterOperatorName}, co); err != nil {
		return fmt.Errorf("failed to get ClusterOperator: %w", err)
	}

	// Build conditions based on reconcile result
	conditions := buildConditions(result)
	needsUpdate, logConditions := mergeConditions(conditions, co.Status.Conditions)

	if !needsUpdate {
		return nil
	}

	log.Info("Updating conditions", logConditions...)

	clusterOperatorApplyConfig := configv1apply.ClusterOperator(clusterOperatorName).
		WithStatus(configv1apply.ClusterOperatorStatus().
			WithConditions(conditions...),
		)

	patch := util.ApplyConfigPatch(clusterOperatorApplyConfig)
	if err := r.Status().Patch(ctx, co, patch, client.FieldOwner(ssaFieldOwner), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to patch ClusterOperator status: %w", err)
	}

	return nil
}

func mergeConditions(newConditions []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration, existingConditions []configv1.ClusterOperatorStatusCondition) (bool, []any) {
	now := metav1.Now()

	// Check if any conditions changed
	needsUpdate := false
	logConditions := make([]any, 0, len(newConditions)*2)

	for _, cond := range newConditions {
		if cond.Type == nil || cond.Status == nil || cond.Reason == nil || cond.Message == nil {
			// Programming error - should never happen
			panic(fmt.Sprintf("condition is missing required fields: %+v", cond))
		}

		existing := findClusterOperatorCondition(existingConditions, *cond.Type)

		switch {
		case existing == nil:
			needsUpdate = true

			cond.WithLastTransitionTime(now)

		// Don't update LastTransitionTime if Status/Reason are the same
		case existing.Status == *cond.Status && existing.Reason == *cond.Reason:
			cond.WithLastTransitionTime(existing.LastTransitionTime)

			if existing.Message != *cond.Message {
				needsUpdate = true
			}

		default:
			needsUpdate = true

			cond.WithLastTransitionTime(now)
		}

		logConditions = append(logConditions, *cond.Type, *cond.Status)
	}

	return needsUpdate, logConditions
}

// buildConditions builds the Progressing and Degraded conditions based on the reconcile error.
func buildConditions(result reconcileResult) []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	progressing := configv1apply.ClusterOperatorStatusCondition().WithType(conditionTypeProgressing)
	degraded := configv1apply.ClusterOperatorStatusCondition().WithType(conditionTypeDegraded)

	switch {
	// Success - not progressing, not degraded
	case result.progressingReason == "" && result.error == nil:
		progressing.
			WithStatus(configv1.ConditionFalse).
			WithReason(conditionReasonSuccess).
			WithMessage("Revision is current")

		degraded.
			WithStatus(configv1.ConditionFalse).
			WithReason(conditionReasonSuccess).
			WithMessage("Not degraded")

	// Permanent error - not progressing (can't make progress), degraded
	case result.progressingReason == conditionReasonNonRetryableError:
		progressing.
			WithStatus(configv1.ConditionFalse).
			WithReason(result.progressingReason).
			WithMessage(result.error.Error())

		degraded.
			WithStatus(configv1.ConditionTrue).
			WithReason(result.progressingReason).
			WithMessage(result.error.Error())

	// Progressing, possibly with ephemeral error
	default:
		reason := result.progressingReason
		if reason == "" {
			reason = conditionReasonEphemeralError
		}

		message := result.message
		if message == "" && result.error != nil {
			message = result.error.Error()
		}

		progressing.
			WithStatus(configv1.ConditionTrue).
			WithReason(reason).
			WithMessage(message)

		degraded.
			WithStatus(configv1.ConditionFalse).
			WithReason(conditionReasonProgressing).
			WithMessage("Revision controller is progressing")
	}

	return []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{progressing, degraded}
}

// findClusterOperatorCondition finds a condition by type in a slice of conditions.
func findClusterOperatorCondition(conditions []configv1.ClusterOperatorStatusCondition, condType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}

	return nil
}

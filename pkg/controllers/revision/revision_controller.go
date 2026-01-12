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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
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

	// degradedThreshold is the duration after which ephemeral errors trigger the Degraded condition.
	degradedThreshold = 5 * time.Minute
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
func (r *RevisionController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
	// Get current platform from Infrastructure singleton
	infra := &configv1.Infrastructure{}
	if err := r.Get(ctx, client.ObjectKey{Name: infrastructureName}, infra); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcileResult{progressingReason: conditionReasonWaitingOnExternal, message: "Infrastructure not found"}
		}

		return reconcileResult{progressingReason: conditionReasonEphemeralError, error: fmt.Errorf("fetching infrastructure: %w", err)}
	}

	if infra.Status.PlatformStatus == nil {
		log.Info("Infrastructure PlatformStatus is nil, requeuing")
		return reconcileResult{progressingReason: conditionReasonWaitingOnExternal, message: "Waiting for Infrastructure PlatformStatus"}
	}

	// Get ClusterAPI singleton
	clusterAPI := &operatorv1alpha1.ClusterAPI{}
	if err := r.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcileResult{progressingReason: conditionReasonWaitingOnExternal, message: "ClusterAPI not found"}
		}

		return reconcileResult{progressingReason: conditionReasonEphemeralError, error: fmt.Errorf("fetching ClusterAPI: %w", err)}
	}

	sortedRevisions := slices.SortedStableFunc(slices.Values(clusterAPI.Status.Revisions), func(a, b operatorv1alpha1.ClusterAPIInstallerRevision) int {
		return cmp.Compare(a.Revision, b.Revision)
	})

	var latestRevision *operatorv1alpha1.ClusterAPIInstallerRevision
	if len(sortedRevisions) > 0 {
		latestRevision = &sortedRevisions[len(sortedRevisions)-1]
	}

	platform := infra.Status.PlatformStatus.Type

	// Build ordered component list from provider metadata
	providerComponents := r.buildComponentList(platform)
	if len(providerComponents) == 0 {
		log.Info("No components for current platform", "platform", platform)
		return reconcileResult{}
	}

	// Calculate contentID = SHA256(component1.contentID + component2.contentID + ...)
	contentID := calculateContentID(providerComponents)

	// We need a new revision if the latest revision has a different contentID
	if latestRevision != nil && latestRevision.ContentID == contentID {
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

	// Build revision name: <version>-<contentID[:8]>-<revisionNumber>
	var revisionNumber int64 = 1
	if latestRevision != nil {
		revisionNumber = latestRevision.Revision + 1
	}

	revisionName := r.buildRevisionName(r.ReleaseVersion, contentID, revisionNumber)

	// Convert provider components to API format
	apiComponents := toAPIComponents(providerComponents)

	// Create revision
	newRevision := operatorv1alpha1.ClusterAPIInstallerRevision{
		Name:       operatorv1alpha1.RevisionName(revisionName),
		Revision:   revisionNumber,
		ContentID:  contentID,
		Components: apiComponents,
	}

	clusterAPI.Status.Revisions = append(clusterAPI.Status.Revisions, newRevision)
	clusterAPI.Status.DesiredRevision = operatorv1alpha1.RevisionName(revisionName)

	if err := r.Status().Update(ctx, clusterAPI); err != nil {
		return reconcileResult{progressingReason: conditionReasonEphemeralError, error: fmt.Errorf("updating ClusterAPI status: %w", err)}
	}

	log.Info("Created new revision",
		"revisionName", revisionName,
		"revisionNumber", revisionNumber,
		"contentID", contentID)

	return reconcileResult{}
}

// buildComponentList builds an ordered list of provider components for the given platform.
// Components are ordered by: core+global, core+platform, infra+global, infra+platform
// Providers that don't match the current platform are filtered out.
func (r *RevisionController) buildComponentList(platform configv1.PlatformType) []providerimages.ProviderImageManifests {
	componentsByPlatform := filterComponentsByPlatform(r.ProviderProfiles, platform)

	return slices.SortedStableFunc(componentsByPlatform, func(a, b providerimages.ProviderImageManifests) int {
		// Sort by install order
		if c := cmp.Compare(a.InstallOrder, b.InstallOrder); c != 0 {
			return c
		}

		// Sort no platform before platform-specific when install order is the same
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

// calculateContentID calculates a SHA256 hash of all provider ContentID fields.
func calculateContentID(providers []providerimages.ProviderImageManifests) string {
	h := sha256.New()

	for _, p := range providers {
		h.Write([]byte(p.ContentID))
	}

	return hex.EncodeToString(h.Sum(nil))
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
	conditions := r.buildConditions(result, co.Status.Conditions)

	now := metav1.Now()

	// Check if any conditions changed
	needsUpdate := false
	logConditions := make([]any, 0, len(conditions)*2)

	for _, cond := range conditions {
		if cond.Type == nil || cond.Status == nil || cond.Reason == nil || cond.Message == nil {
			// Programming error - should never happen
			panic(fmt.Sprintf("condition is missing required fields: %+v", cond))
		}

		existing := findClusterOperatorCondition(co.Status.Conditions, *cond.Type)

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

	if !needsUpdate {
		return nil
	}

	clusterOperatorApplyConfig := configv1apply.ClusterOperator(clusterOperatorName).
		WithStatus(configv1apply.ClusterOperatorStatus().
			WithConditions(conditions...),
		)

	log.Info("Updating ClusterOperator conditions", logConditions...)

	patch := util.ApplyConfigPatch(clusterOperatorApplyConfig)
	if err := r.Status().Patch(ctx, co, patch, client.FieldOwner(ssaFieldOwner), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to patch ClusterOperator status: %w", err)
	}

	return nil
}

// buildConditions builds the Progressing and Degraded conditions based on the reconcile error.
func (r *RevisionController) buildConditions(result reconcileResult, existing []configv1.ClusterOperatorStatusCondition) []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	if result.progressingReason == "" && result.error == nil {
		// Success - not progressing, not degraded
		return []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{
			configv1apply.ClusterOperatorStatusCondition().
				WithType(conditionTypeProgressing).
				WithStatus(configv1.ConditionFalse).
				WithReason(conditionReasonSuccess).
				WithMessage("Revision is current"),

			configv1apply.ClusterOperatorStatusCondition().
				WithType(conditionTypeDegraded).
				WithStatus(configv1.ConditionFalse).
				WithReason(conditionReasonSuccess).
				WithMessage("Not degraded"),
		}
	}

	// Check if error is non-retryable
	if result.progressingReason == conditionReasonNonRetryableError {
		// Permanent error - not progressing (can't make progress), degraded
		return []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{
			configv1apply.ClusterOperatorStatusCondition().
				WithType(conditionTypeProgressing).
				WithStatus(configv1.ConditionFalse).
				WithReason(result.progressingReason).
				WithMessage(result.error.Error()),

			configv1apply.ClusterOperatorStatusCondition().
				WithType(conditionTypeDegraded).
				WithStatus(configv1.ConditionTrue).
				WithReason(result.progressingReason).
				WithMessage(result.error.Error()),
		}
	}

	reason := result.progressingReason
	if reason == "" {
		reason = conditionReasonEphemeralError
	}

	message := result.message
	if message == "" && result.error != nil {
		message = result.error.Error()
	}

	// Ephemeral error - progressing (will retry), potentially degraded
	progressing := configv1apply.ClusterOperatorStatusCondition().
		WithType(conditionTypeProgressing).
		WithStatus(configv1.ConditionTrue).
		WithReason(reason).
		WithMessage(message)

	// Calculate if degraded threshold exceeded
	degraded := configv1apply.ClusterOperatorStatusCondition().
		WithType(conditionTypeDegraded)

	// Use the progressing condition's timestamp to determine if we've exceeded the threshold.
	// If we preserved the timestamp above, check against that; otherwise it's a new error.
	existingProgressing := findClusterOperatorCondition(existing, conditionTypeProgressing)
	if existingProgressing != nil && time.Since(existingProgressing.LastTransitionTime.Time) > degradedThreshold {
		degraded.
			WithStatus(configv1.ConditionTrue).
			WithReason(conditionReasonPersistentError).
			WithMessage(fmt.Sprintf("Ephemeral error persisting for > %v", degradedThreshold))
	} else {
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

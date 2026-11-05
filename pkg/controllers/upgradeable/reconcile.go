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
package upgradeable

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	assessment "github.com/openshift/cluster-capi-operator/pkg/upgradeable"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var errStaleImages = errors.New("payload images ConfigMap does not match assessment")

func (r *UpgradeableController) reconcile(ctx context.Context) UpgradeableResult {
	cv := &configv1.ClusterVersion{}
	if err := r.Get(ctx, client.ObjectKey{Name: clusterVersionName}, cv); err != nil {
		return assessmentError(fmt.Errorf("getting ClusterVersion: %w", err))
	}

	target, err := resolveTarget(cv)
	if err != nil {
		return assessmentError(err)
	}

	currentVersion := cv.Status.Desired.Version

	// Both sides identify the assessment: changing the current release must
	// invalidate old results even if the same target is still offered.
	var liveLabels map[string]string
	if target != nil {
		liveLabels = assessment.AssessmentLabels(currentVersion, target.Version)
	}

	done, err := r.cleanup(ctx, liveLabels)
	if err != nil {
		return assessmentError(err)
	}

	if target == nil {
		// No relevant update is not a compatibility verdict. Omitting Upgradeable
		// removes any previous assessment result instead of leaving a stale block.
		return UpgradeableResult{ResultGenerator.Success()}
	}

	if !done {
		// Requirements use fixed names and create only semantics. Old resources
		// must disappear before the next creator can safely accept AlreadyExists.
		return assessmentWaiting("previous assessment deletion")
	}

	co := &configv1.ClusterOperator{}
	if err := r.Get(ctx, client.ObjectKey{Name: operatorstatus.ClusterOperatorName}, co); err != nil {
		return assessmentError(fmt.Errorf("getting ClusterOperator owner: %w", err))
	}

	pod := r.extractorPod(currentVersion, target, co)
	if result := r.ensureAssessmentPod(ctx, pod); result != nil {
		return *result
	}

	cm := &corev1.ConfigMap{}
	if err := r.assessmentReader().Get(ctx, client.ObjectKey{Namespace: assessment.Namespace, Name: assessment.ImagesConfigMapName}, cm); err != nil {
		return assessmentError(fmt.Errorf("getting extracted payload images: %w", err))
	}

	if !labels.SelectorFromSet(liveLabels).Matches(labels.Set(cm.Labels)) {
		return assessmentError(fmt.Errorf("%w %s to %s", errStaleImages, currentVersion, target.Version))
	}

	pod = r.requirementsPod(currentVersion, target.Version, cm, co)
	// Only the creator knows how many CRDs the target contains. Its successful
	// exit is the completeness signal, including when the resulting set is empty.
	if result := r.ensureAssessmentPod(ctx, pod); result != nil {
		return *result
	}

	return r.processRequirements(ctx, liveLabels, target.Version)
}

// Stop old writers before removing their outputs, so a terminating pod cannot
// recreate stale requirements after cleanup and race the next assessment.
func (r *UpgradeableController) cleanup(ctx context.Context, live map[string]string) (bool, error) {
	// The ClusterOperator also owns resources outside this chain. Labels scope
	// invalidation to our assessment instead of relying on shared ownership.
	managed := client.MatchingLabels{assessment.ManagedLabel: assessment.ManagedLabelValue}
	isStale := func(obj client.Object) bool {
		return live == nil || !labels.SelectorFromSet(live).Matches(labels.Set(obj.GetLabels()))
	}

	pods := &corev1.PodList{}
	if err := r.assessmentReader().List(ctx, pods, managed, client.InNamespace(assessment.Namespace)); err != nil {
		return false, fmt.Errorf("listing assessment pods: %w", err)
	}

	waiting := false

	for i := range pods.Items {
		pod := &pods.Items[i]
		if !isStale(pod) {
			continue
		}

		waiting = true

		if err := r.deleteObject(ctx, pod); err != nil {
			return false, err
		}
	}

	if waiting {
		// A deletion request does not stop a pod immediately. Wait until the old
		// writers are actually gone before deleting anything they could recreate.
		return false, nil
	}

	cms := &corev1.ConfigMapList{}
	if err := r.assessmentReader().List(ctx, cms, managed, client.InNamespace(assessment.Namespace)); err != nil {
		return false, fmt.Errorf("listing assessment ConfigMaps: %w", err)
	}

	for i := range cms.Items {
		if isStale(&cms.Items[i]) {
			waiting = true

			if err := r.deleteObject(ctx, &cms.Items[i]); err != nil {
				return false, err
			}
		}
	}

	done, err := r.cleanupRequirements(ctx, live)

	return !waiting && done, err
}

func (r *UpgradeableController) cleanupRequirements(ctx context.Context, live map[string]string) (bool, error) {
	crs := &openshiftapiextensionsv1.CompatibilityRequirementList{}
	if err := r.assessmentReader().List(ctx, crs, client.MatchingLabels{assessment.ManagedLabel: assessment.ManagedLabelValue}); err != nil {
		return false, fmt.Errorf("listing assessment requirements for cleanup: %w", err)
	}

	waiting := false
	deletedAssessments := map[string]bool{}
	// Collection deletion is scoped to exact stale assessment label sets.
	for i := range crs.Items {
		cr := &crs.Items[i]
		if live != nil && labels.SelectorFromSet(live).Matches(labels.Set(cr.Labels)) {
			continue
		}

		waiting = true

		selector := client.MatchingLabels{assessment.ManagedLabel: assessment.ManagedLabelValue, assessment.CurrentVersionLabel: cr.Labels[assessment.CurrentVersionLabel], assessment.TargetVersionLabel: cr.Labels[assessment.TargetVersionLabel]}

		key := labels.SelectorFromSet(map[string]string(selector)).String()
		if deletedAssessments[key] {
			continue
		}

		if err := r.DeleteAllOf(ctx, &openshiftapiextensionsv1.CompatibilityRequirement{}, selector); err != nil {
			return false, fmt.Errorf("deleting stale assessment requirements: %w", err)
		}

		deletedAssessments[key] = true
	}

	return !waiting, nil
}

func (r *UpgradeableController) deleteObject(ctx context.Context, obj client.Object) error {
	if obj.GetDeletionTimestamp() != nil {
		return nil
	}

	// A cached object may already have been replaced under the same fixed name.
	// Delete only the instance we observed, never its replacement.
	if err := r.Delete(ctx, obj, client.Preconditions{UID: ptr.To(obj.GetUID())}); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("deleting %T %s: %w", obj, obj.GetName(), err)
	}

	return nil
}

func (r *UpgradeableController) processRequirements(ctx context.Context, assessmentLabels map[string]string, targetVersion string) UpgradeableResult {
	crs := &openshiftapiextensionsv1.CompatibilityRequirementList{}
	if err := r.assessmentReader().List(ctx, crs, client.MatchingLabels(assessmentLabels)); err != nil {
		return assessmentError(fmt.Errorf("listing assessment requirements: %w", err))
	}

	clusterAPI := &operatorv1alpha1.ClusterAPI{}
	if err := r.assessmentReader().Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI); err != nil {
		return assessmentError(fmt.Errorf("getting Cluster API configuration: %w", err))
	}

	// Get the unmanaged CRDs names from the ClusterAPI configuration.
	unmanaged := sets.New[string]()
	if clusterAPI.Spec != nil {
		unmanaged.Insert(clusterAPI.Spec.UnmanagedCustomResourceDefinitions...)
	}

	conflicts := []string{}

	// A partial result cannot represent the whole target payload. Even if one
	// requirement is incompatible, wait for all verdicts to be admitted and fresh.
	for _, cr := range crs.Items {
		admitted := meta.FindStatusCondition(cr.Status.Conditions, openshiftapiextensionsv1.CompatibilityRequirementAdmitted)

		compatible := meta.FindStatusCondition(cr.Status.Conditions, openshiftapiextensionsv1.CompatibilityRequirementCompatible)

		// Wait for the CompatibilityRequirement Admitted and Compatible conditions to be present and true.
		if admitted == nil || admitted.Status != metav1.ConditionTrue ||
			compatible == nil || compatible.ObservedGeneration < cr.Generation || compatible.Status == metav1.ConditionUnknown {
			return assessmentWaiting("CompatibilityRequirement conditions")
		}

		// Check if the CRD is incompatible/conflicting.
		if conflictErr := r.requirementConflicts(ctx, &cr, unmanaged); conflictErr != nil {
			var conflict *crdConflictError
			if !errors.As(conflictErr, &conflict) {
				// If the error is not a crdConflictError, return an assessment error.
				return assessmentError(conflictErr)
			}

			conflicts = append(conflicts, conflict.Error())
		}
	}

	result := UpgradeableResult{ResultGenerator.Success()}
	if len(conflicts) == 0 {
		// If there are no conflicts, return a success result.
		return result.WithUpgradeable(configv1.ConditionTrue, "Compatible", fmt.Sprintf("Cluster API components are compatible with release %s", targetVersion))
	}

	// Stable ordering avoids status churn when API list order changes.
	sort.Strings(conflicts)

	message := "Incompatible CRDs:\n- " + strings.Join(conflicts, "\n- ")
	// Bound the message so a large conflict set remains suitable for ClusterOperator status.
	if len(message) > 4000 {
		message = message[:3997] + "..."
	}

	return result.WithUpgradeable(configv1.ConditionFalse, "NotCompatible", message)
}

// Conflicts block upgrade eligibility, while API errors mean eligibility is unknown.
func (r *UpgradeableController) requirementConflicts(ctx context.Context, requirement *openshiftapiextensionsv1.CompatibilityRequirement, unmanaged sets.Set[string]) error {
	name := requirement.Status.CRDName
	crd := &apiextensionsv1.CustomResourceDefinition{}
	compatible := meta.FindStatusCondition(requirement.Status.Conditions, openshiftapiextensionsv1.CompatibilityRequirementCompatible)

	// Get the CRD.
	if err := r.assessmentReader().Get(ctx, client.ObjectKey{Name: name}, crd); err != nil {
		if apierrors.IsNotFound(err) {
			if !unmanaged.Has(name) {
				return nil
			}

			return &crdConflictError{
				CRDName:              name,
				Reason:               unmanagedCRDNotFoundReason,
				Message:              "the CRD is declared unmanaged but is absent from the cluster.",
				CompatibilityReason:  compatible.Reason,
				CompatibilityMessage: compatible.Message,
				Remediation:          fmt.Sprintf("Have the external CRD manager install %q before upgrading.", name),
			}
		}

		return fmt.Errorf("getting CRD %s: %w", name, err)
	}

	if unmanaged.Has(name) {
		// If the CRD is declared as unmanaged, check the compatible condition.
		if compatible.Status != metav1.ConditionFalse {
			return nil
		}

		return &crdConflictError{
			CRDName:              name,
			Reason:               unmanagedCRDIncompatibleReason,
			Message:              "the unmanaged CRD does not satisfy the target requirements.",
			CompatibilityReason:  compatible.Reason,
			CompatibilityMessage: compatible.Message,
			Remediation:          fmt.Sprintf("Have the external CRD manager update %q to resolve the compatibility errors above before upgrading.", name),
		}
	}

	// For CRDs not explicitly declared as unmanaged, check if the cluster-api operator managed label is present.
	// If not, it is a conflict, as the CRD is not managed by the cluster-api operator, nor declared as unmanaged.
	if _, managed := crd.Labels[revisiongenerator.ManagedLabelKey]; !managed {
		return &crdConflictError{
			CRDName:              name,
			Reason:               foreignCRDReason,
			Message:              "the CRD exists but is neither managed by the cluster-api operator nor declared unmanaged.",
			CompatibilityReason:  compatible.Reason,
			CompatibilityMessage: compatible.Message,
			Remediation:          fmt.Sprintf("Declare %q in ClusterAPI/cluster.spec.unmanagedCustomResourceDefinitions and resolve any compatibility errors above. The unmanaged declaration cannot be removed once added.", name),
		}
	}

	return nil
}

// Pod completion and output informer events can arrive in either order. Read
// outputs directly so a completed pod cannot appear to have written an empty set.
func (r *UpgradeableController) assessmentReader() client.Reader {
	if r.apiReader != nil {
		return r.apiReader
	}

	return r.Client
}

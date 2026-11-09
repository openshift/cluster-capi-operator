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
	assessment "github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
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

	// Empty target version means no target. We deliberately reconcile this to
	// clean up artifacts and report Upgradeable=True.
	targetVersion := ""
	if target != nil {
		targetVersion = target.Version
	}

	liveLabels := assessment.AssessmentLabels(currentVersion, targetVersion)

	co := &configv1.ClusterOperator{}
	if err := r.Get(ctx, client.ObjectKey{Name: operatorstatus.ClusterOperatorName}, co); err != nil {
		return assessmentError(fmt.Errorf("getting ClusterOperator owner: %w", err))
	}

	if result := r.ensureAssessmentPod(ctx, r.extractorPod(currentVersion, target, co)); result != nil {
		return *result
	}

	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: assessment.Namespace, Name: assessment.ImagesConfigMapName}, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return assessmentWaiting(fmt.Sprintf("payload images ConfigMap %s", assessment.ImagesConfigMapName))
		}

		return assessmentError(fmt.Errorf("getting extracted payload images: %w", err))
	}

	if !labels.SelectorFromSet(liveLabels).Matches(labels.Set(cm.Labels)) {
		// The informer cache can briefly serve the previous assessment's copy of
		// the ConfigMap. A succeeded extractor has already written the new one.
		return assessmentWaiting(fmt.Sprintf("payload images for %s to %s", currentVersion, targetVersion))
	}

	pod := r.requirementsPod(currentVersion, targetVersion, cm, co)
	// Only the creator knows how many CRDs the target contains. Its successful
	// exit is the completeness signal, including when the resulting set is empty.
	if result := r.ensureAssessmentPod(ctx, pod); result != nil {
		return *result
	}

	return r.processRequirements(ctx, liveLabels, targetVersion)
}

// deleteObject deletes only the instance that was observed. A failed pod is
// replaced by the next assessment; a pod left over from an assessment that no
// longer applies is ignored rather than collected.
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
	// Without a next minor release there is no target payload, so there is nothing
	// that could block an upgrade: report Upgradeable=True whatever earlier
	// requirements report. The origin sig-arch test "[sig-arch][Early] Managed
	// cluster should start all core operators" (test/extended/operators/operators.go,
	// surprisingConditions) requires Upgradeable=True on every
	// ClusterOperator which sets the Condition.
	if targetVersion == "" {
		result := UpgradeableResult{ResultGenerator.Success()}
		return result.WithUpgradeable(configv1.ConditionTrue, "NoUpdate", "No update to the next minor release is available, so Cluster API compatibility cannot be assessed")
	}

	crs := &openshiftapiextensionsv1.CompatibilityRequirementList{}
	if err := r.List(ctx, crs, client.MatchingLabels(assessmentLabels)); err != nil {
		return assessmentError(fmt.Errorf("listing assessment requirements: %w", err))
	}

	clusterAPI := &operatorv1alpha1.ClusterAPI{}
	if err := r.Get(ctx, client.ObjectKey{Name: clusterAPIName}, clusterAPI); err != nil {
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
		if !conclusiveVerdict(&cr) {
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

// conclusiveVerdict reports whether the requirement has been admitted and has a
// current compatibility verdict. Both the assessment and its watch predicate
// depend on this definition of a publishable result.
func conclusiveVerdict(requirement *openshiftapiextensionsv1.CompatibilityRequirement) bool {
	admitted := meta.FindStatusCondition(requirement.Status.Conditions, openshiftapiextensionsv1.CompatibilityRequirementAdmitted)
	compatible := meta.FindStatusCondition(requirement.Status.Conditions, openshiftapiextensionsv1.CompatibilityRequirementCompatible)

	return admitted != nil && admitted.Status == metav1.ConditionTrue &&
		compatible != nil && compatible.Status != metav1.ConditionUnknown &&
		compatible.ObservedGeneration >= requirement.Generation
}

// Conflicts block upgrade eligibility, while API errors mean eligibility is unknown.
func (r *UpgradeableController) requirementConflicts(ctx context.Context, requirement *openshiftapiextensionsv1.CompatibilityRequirement, unmanaged sets.Set[string]) error {
	name := requirement.Status.CRDName
	crd := &apiextensionsv1.CustomResourceDefinition{}
	compatible := meta.FindStatusCondition(requirement.Status.Conditions, openshiftapiextensionsv1.CompatibilityRequirementCompatible)

	// Get the CRD.
	if err := r.Get(ctx, client.ObjectKey{Name: name}, crd); err != nil {
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

	// A CRD the target payload adopts is not a foreign owner: the installer's
	// revision rewrites it to the target definition during the upgrade.
	if adoptableByInstaller(requirement) {
		return nil
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

// adoptableByInstaller reports whether the target payload's manifest declares
// that the installer adopts an existing CRD of this name. The requirements pod
// carries the annotation from that manifest onto the requirement, because the
// installer strips it from the objects it applies.
func adoptableByInstaller(requirement *openshiftapiextensionsv1.CompatibilityRequirement) bool {
	return requirement.Annotations[revisiongenerator.AdoptExistingAnnotation] == revisiongenerator.AdoptExistingAlways
}

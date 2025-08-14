/*
Copyright 2025 Red Hat, Inc.

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

package crdcompatibility

import (
	"context"
	"fmt"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1applyconfig "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorapplyconfig "github.com/openshift/client-go/operator/applyconfigurations/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	progressingReasonConfigurationError string = "ConfigurationError"
	progressingReasonTransientError     string = "TransientError"
	progressingReasonUpToDate           string = "UpToDate"

	compatibleReasonRequirementsNotMet     string = "RequirementsNotMet"
	compatibleReasonCompatibleWithWarnings string = "CompatibleWithWarnings"
	compatibleReasonCompatible             string = "Compatible"

	admittedReasonAdmitted               string = "Admitted"
	admittedReasonCompatibilityCRDNotSet string = "CompatibilityCRDNotSet"
)

func (r *reconcileState) writeStatus(ctx context.Context, obj *operatorv1alpha1.CRDCompatibilityRequirement, reconcileErr error) error {
	// Don't write status if the object has no finalizer
	if obj.DeletionTimestamp.IsZero() && !slices.Contains(obj.Finalizers, finalizerName) {
		log.FromContext(ctx).Info("Skipping status because the object is being deleted")
		return nil
	}

	admittedCondition := r.getAdmittedCondition()
	compatibleCondition := r.getCompatibleCondition()
	progressingCondition := r.getProgressingCondition(reconcileErr)

	currentConditions := obj.Status.Conditions
	now := metav1.Now()
	applyConfigStatus := operatorapplyconfig.CRDCompatibilityRequirementStatus().
		WithConditions(
			util.SetLastTransitionTimeMetaV1(now, currentConditions, admittedCondition).WithObservedGeneration(obj.GetGeneration()),
			util.SetLastTransitionTimeMetaV1(now, currentConditions, compatibleCondition).WithObservedGeneration(obj.GetGeneration()),
			util.SetLastTransitionTimeMetaV1(now, currentConditions, progressingCondition).WithObservedGeneration(obj.GetGeneration()),
		)
	if r.currentCRD != nil {
		applyConfigObservedCRD := operatorapplyconfig.ObservedCRD().
			WithUID(string(r.currentCRD.GetUID())).
			WithGeneration(r.currentCRD.GetGeneration())
		applyConfigStatus.WithObservedCRD(applyConfigObservedCRD)
	}

	applyConfig := operatorapplyconfig.CRDCompatibilityRequirement(obj.GetName()).
		WithUID(obj.GetUID()).
		WithStatus(applyConfigStatus)
	if err := r.client.Status().Patch(ctx, obj, util.ApplyConfigPatch(applyConfig), client.ForceOwnership, client.FieldOwner(controllerName+"-Status")); err != nil {
		return fmt.Errorf("failed to write status: %w", err)
	}

	return nil
}

// Progressing indicates whether the controller is currently progressing towards
// being Ready. Setting Progressing to False indicates to an observer that the
// current state is final until a change is made.
func (r *reconcileState) getProgressingCondition(reconcileErr error) *metav1applyconfig.ConditionApplyConfiguration {
	progressingCondition := metav1applyconfig.Condition().WithType("Progressing")
	if reconcileErr != nil {
		if noRequeueError := util.AsNoRequeueError(reconcileErr); noRequeueError != nil {
			progressingCondition.
				WithStatus(metav1.ConditionFalse).
				WithReason(noRequeueError.Reason).
				WithMessage(noRequeueError.Error())
		} else {
			progressingCondition.
				WithStatus(metav1.ConditionTrue).
				WithReason(progressingReasonTransientError).
				WithMessage(reconcileErr.Error())
		}
	} else {
		progressingCondition.
			WithStatus(metav1.ConditionFalse).
			WithReason(progressingReasonUpToDate).
			WithMessage("The CRDCompatibilityRequirement is up to date")
	}

	return progressingCondition
}

// Ready indicates whether the CRDCompatibililtyRequirement has been completely admitted, i.e. all required admission policies have been created.
// Not yet implemented
func (r *reconcileState) getAdmittedCondition() *metav1applyconfig.ConditionApplyConfiguration {
	admittedCondition := metav1applyconfig.Condition().WithType("Admitted")

	if r.compatibilityCRD != nil {
		admittedCondition.
			WithStatus(metav1.ConditionTrue).
			WithReason(admittedReasonAdmitted).
			WithMessage("The CRDCompatibilityRequirement has been admitted")
	} else {
		admittedCondition.
			WithStatus(metav1.ConditionFalse).
			WithReason(admittedReasonCompatibilityCRDNotSet).
			WithMessage("The compatibility CRD is not set")
	}

	return admittedCondition
}

// Compatible indicates whether the CRD is compatible with the compatibilityCRD
// Not yet implemented
func (r *reconcileState) getCompatibleCondition() *metav1applyconfig.ConditionApplyConfiguration {
	compatibleCondition := metav1applyconfig.Condition().WithType("Compatible")

	if len(r.compatibilityErrors) > 0 {
		compatibleCondition.
			WithStatus(metav1.ConditionFalse).
			WithReason(compatibleReasonRequirementsNotMet).
			WithMessage(strings.Join(r.compatibilityErrors, "\n"))
	} else {
		compatibleCondition.WithStatus(metav1.ConditionTrue)

		if len(r.compatibilityWarnings) > 0 {
			compatibleCondition.WithReason(compatibleReasonCompatibleWithWarnings).
				WithMessage(strings.Join(r.compatibilityWarnings, "\n"))
		} else {
			compatibleCondition.WithReason(compatibleReasonCompatible).
				WithMessage("The CRD is compatible with this requirement")
		}
	}

	return compatibleCondition
}

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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1applyconfig "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorapplyconfig "github.com/openshift/client-go/operator/applyconfigurations/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

func (r *reconcileState) writeStatus(ctx context.Context, obj *operatorv1alpha1.CRDCompatibilityRequirement, reconcileErr error) error {
	// Don't write status if the object has no finalizer
	if obj.DeletionTimestamp.IsZero() && !slices.Contains(obj.Finalizers, finalizerName) {
		log.FromContext(ctx).Info("Skipping status because the object is being deleted")
		return nil
	}

	admittedCondition := r.getAdmittedCondition().WithObservedGeneration(obj.GetGeneration())
	compatibleCondition := r.getCompatibleCondition().WithObservedGeneration(obj.GetGeneration())
	progressingCondition := r.getProgressingCondition(reconcileErr).WithObservedGeneration(obj.GetGeneration())

	currentConditions := obj.Status.Conditions
	now := metav1.Now()
	applyConfigStatus := operatorapplyconfig.CRDCompatibilityRequirementStatus().
		WithConditions(
			util.SetLastTransitionTimeMetaV1(now, currentConditions, admittedCondition),
			util.SetLastTransitionTimeMetaV1(now, currentConditions, compatibleCondition),
			util.SetLastTransitionTimeMetaV1(now, currentConditions, progressingCondition),
		).
		WithName(r.compatibilityCRD.GetName())

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
		// Ignore the error if the object is already gone.
		if !obj.DeletionTimestamp.IsZero() && apierrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to write status: %w", err)
	}

	return nil
}

// Progressing indicates whether the controller is currently progressing towards
// being Ready. Setting Progressing to False indicates to an observer that the
// current state is final until a change is made.
func (r *reconcileState) getProgressingCondition(reconcileErr error) *metav1applyconfig.ConditionApplyConfiguration {
	progressingCondition := metav1applyconfig.Condition().WithType(operatorv1alpha1.CRDCompatibilityConditionTypeProgressing)

	if reconcileErr != nil {
		if noRequeueError := util.AsNoRequeueError(reconcileErr); noRequeueError != nil {
			progressingCondition.
				WithStatus(metav1.ConditionFalse).
				WithReason(noRequeueError.Reason).
				WithMessage(noRequeueError.Error())
		} else {
			progressingCondition.
				WithStatus(metav1.ConditionTrue).
				WithReason(operatorv1alpha1.CRDCompatibilityProgressingReasonTransientError).
				WithMessage(reconcileErr.Error())
		}
	} else {
		progressingCondition.
			WithStatus(metav1.ConditionFalse).
			WithReason(operatorv1alpha1.CRDCompatibilityProgressingReasonUpToDate).
			WithMessage("The CRDCompatibilityRequirement is up to date")
	}

	return progressingCondition
}

// Ready indicates whether the CRDCompatibililtyRequirement has been completely admitted, i.e. all required admission policies have been created.
func (r *reconcileState) getAdmittedCondition() *metav1applyconfig.ConditionApplyConfiguration {
	admittedCondition := metav1applyconfig.Condition().WithType(operatorv1alpha1.CRDCompatibilityConditionTypeAdmitted)

	if r.compatibilityCRD != nil {
		admittedCondition.
			WithStatus(metav1.ConditionTrue).
			WithReason(operatorv1alpha1.CRDCompatibilityAdmittedReasonAdmitted).
			WithMessage("The CRDCompatibilityRequirement has been admitted")
	} else {
		admittedCondition.
			WithStatus(metav1.ConditionFalse).
			WithReason(operatorv1alpha1.CRDCompatibilityAdmittedReasonNotAdmitted).
			WithMessage("The compatibility CRD is not set")
	}

	return admittedCondition
}

// Compatible indicates whether the CRD is compatible with the compatibilityCRD.
func (r *reconcileState) getCompatibleCondition() *metav1applyconfig.ConditionApplyConfiguration {
	compatibleCondition := metav1applyconfig.Condition().WithType(operatorv1alpha1.CRDCompatibilityConditionTypeCompatible)

	switch {
	case r.currentCRD == nil:
		compatibleCondition.
			WithStatus(metav1.ConditionFalse).
			WithReason(operatorv1alpha1.CRDCompatibilityCompatibleReasonCRDDoesNotExist).
			WithMessage("The target CRD does not exist")
	case len(r.compatibilityErrors) > 0:
		compatibleCondition.
			WithStatus(metav1.ConditionFalse).
			WithReason(operatorv1alpha1.CRDCompatibilityCompatibleReasonRequirementsNotMet).
			WithMessage(strings.Join(r.compatibilityErrors, "\n"))
	case len(r.compatibilityWarnings) > 0:
		compatibleCondition.
			WithStatus(metav1.ConditionTrue).
			WithReason(operatorv1alpha1.CRDCompatibilityCompatibleReasonCompatibleWithWarnings).
			WithMessage(strings.Join(r.compatibilityWarnings, "\n"))
	default:
		compatibleCondition.
			WithStatus(metav1.ConditionTrue).
			WithReason(operatorv1alpha1.CRDCompatibilityCompatibleReasonCompatible).
			WithMessage("The CRD is compatible with this requirement")
	}

	return compatibleCondition
}

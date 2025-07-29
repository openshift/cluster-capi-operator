package crdcompatibility

import (
	"context"
	"errors"
	"fmt"

	operatorapplyconfig "github.com/openshift/client-go/operator/applyconfigurations/operator/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1applyconfig "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	progressingReasonConfigurationError string = "ConfigurationError"
	progressingReasonTransientError     string = "TransientError"
	progressingReasonUpToDate           string = "UpToDate"
)

func (r *reconcileState) writeStatus(ctx context.Context, reconcileErr error) error {
	// Ready indicates whether the CRDCompatibililtyRequirement has been completely admitted, i.e. all required admission policies have been
	// Not yet implemented
	readyCondition := metav1applyconfig.Condition().WithType("Ready").
		WithStatus(metav1.ConditionFalse).
		WithReason("NotImplemented").
		WithMessage("Not implemented")

	// Compatible indicates whether the CRD is compatible with the compatibilityCRD
	// Not yet implemented
	compatibleCondition := metav1applyconfig.Condition().WithType("Compatible").
		WithStatus(metav1.ConditionFalse).
		WithReason("NotImplemented").
		WithMessage("Not implemented")

	// Progressing indicates whether the controller is currently progressing towards being Ready.
	// Setting Progressing to False indicates to an observer that the current state is final until a change is made.
	progressingCondition := metav1applyconfig.Condition().WithType("Progressing")
	if reconcileErr != nil {
		noRequeueError := noRequeueErrorWrapper{}
		if errors.As(reconcileErr, &noRequeueError) {
			progressingCondition.
				WithStatus(metav1.ConditionFalse).
				WithReason(progressingReasonConfigurationError).
				WithMessage(reconcileErr.Error())
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

	applyConfigStatus := operatorapplyconfig.CRDCompatibilityRequirementStatus().
		WithConditions(readyCondition, compatibleCondition, progressingCondition)
	if r.currentCRD != nil {
		applyConfigObservedCRD := operatorapplyconfig.ObservedCRD().
			WithUID(string(r.currentCRD.GetUID())).
			WithGeneration(r.currentCRD.GetGeneration())
		applyConfigStatus.WithObservedCRD(applyConfigObservedCRD)
	}

	applyConfig := operatorapplyconfig.CRDCompatibilityRequirement(r.compatibilityCRD.GetName()).WithStatus(applyConfigStatus)
	if err := r.Client.Status().Patch(ctx, r.compatibilityCRD, util.ApplyConfigPatch(applyConfig), client.ForceOwnership, client.FieldOwner(controllerName+"-Status")); err != nil {
		return fmt.Errorf("failed to write status: %w", err)
	}

	return nil
}

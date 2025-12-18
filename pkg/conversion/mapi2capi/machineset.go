/*
Copyright 2024 Red Hat, Inc.

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
package mapi2capi

import (
	"cmp"
	"sort"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ptr "k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capierrors "sigs.k8s.io/cluster-api/errors"
)

// fromMAPIMachineSetToCAPIMachineSet takes a MAPI MachineSet and returns a converted CAPI MachineSet.
func fromMAPIMachineSetToCAPIMachineSet(mapiMachineSet *mapiv1beta1.MachineSet) (*clusterv1.MachineSet, utilerrors.Aggregate) {
	var errs field.ErrorList

	specSelector := convertMAPIMachineSetSelectorToCAPI(mapiMachineSet.Spec.Selector)

	capiMachineSet := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            mapiMachineSet.Name,
			Namespace:       mapiMachineSet.Namespace,
			Labels:          convertMAPILabelsToCAPI(mapiMachineSet.Labels),
			Annotations:     convertMAPIAnnotationsToCAPI(mapiMachineSet.Annotations),
			Finalizers:      nil, // The CAPI MachineSet finalizer is managed by the CAPI machineset controller.
			OwnerReferences: nil, // OwnerReferences not populated here. They are added later by the machineSetSync controller.
		},
		Spec: clusterv1.MachineSetSpec{
			Selector: specSelector,
			Replicas: mapiMachineSet.Spec.Replicas,
			// ClusterName: // ClusterName not populated here. It is added later by higher level functions
			Deletion: clusterv1.MachineSetDeletionSpec{
				Order: cmp.Or(clusterv1.MachineSetDeletionOrder(mapiMachineSet.Spec.DeletePolicy), clusterv1.RandomMachineSetDeletionOrder),
			},
			Template: clusterv1.MachineTemplateSpec{
				ObjectMeta: clusterv1.ObjectMeta{
					Labels:      convertMAPILabelsToCAPI(mapiMachineSet.Spec.Template.Labels),
					Annotations: convertMAPIAnnotationsToCAPI(mapiMachineSet.Spec.Template.Annotations),
				},
				Spec: clusterv1.MachineSpec{
					// Further Spec is not populated here. It is added later by higher level functions.
					MinReadySeconds: nilIfZero(mapiMachineSet.Spec.MinReadySeconds),
				},
			},
			// MachineNamingStrategy: // Not supported in MAPI, remains nil. No equivalent field in MAPI MachineSet.
			// AuthoritativeAPI: // Ignore, this is part of the conversion mechanism.
		},
		Status: convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet, specSelector),
	}

	errs = append(errs, handleUnsupportedMAPIObjectMetaFields(field.NewPath("spec", "template", "metadata"), mapiMachineSet.Spec.Template.ObjectMeta)...)

	return capiMachineSet, errs.ToAggregate()
}

func nilIfZero(i int32) *int32 {
	if i == 0 {
		return nil
	}

	return ptr.To(i)
}

// convertMAPIMachineSetToCAPIMachineSetStatus converts a MAPI MachineSet to CAPI MachineSetStatus.
func convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet *mapiv1beta1.MachineSet, specSelector metav1.LabelSelector) clusterv1.MachineSetStatus {
	capiStatus := clusterv1.MachineSetStatus{
		Deprecated: &clusterv1.MachineSetDeprecatedStatus{
			V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
				FullyLabeledReplicas: mapiMachineSet.Status.FullyLabeledReplicas,
				ReadyReplicas:        mapiMachineSet.Status.ReadyReplicas,
				AvailableReplicas:    mapiMachineSet.Status.AvailableReplicas,
				Conditions:           convertMAPIMachineSetConditionsToCAPIMachineSetConditions(mapiMachineSet),
			},
		},
		Replicas: ptr.To(mapiMachineSet.Status.Replicas),
		// ObservedGeneration: // We don't set the observed generation at this stage as it is handled by the machineSetSync controller.
		ReadyReplicas:     nilIfZero(mapiMachineSet.Status.ReadyReplicas),
		AvailableReplicas: nilIfZero(mapiMachineSet.Status.AvailableReplicas),
		Conditions:        convertMAPIMachineSetConditionsToCAPIMachineSetV1Beta2StatusConditions(mapiMachineSet),
		// If the current MachineSet is a stand-alone MachineSet, the MachineSet controller does not set an up-to-date condition
		// on its child Machines, allowing tools managing higher level abstractions to set this condition.
		// This is also consistent with the fact that the MachineSet controller primarily takes care of the number of Machine
		// replicas, it doesn't reconcile them (even if we have a few exceptions like in-place propagation of a few selected
		// fields and remediation).
		// So considering we don't use the MachineDeployments on the MAPI side
		// and don't support "matching" higher level abstractions
		// for the conversion of a MachineSet from MAPI to CAPI
		// We always want to set this to zero on conversion.
		// ref:
		// https://github.com/kubernetes-sigs/cluster-api/blob/9c2eb0a04d5a03e18f2d557f1297391fb635f88d/internal/controllers/machineset/machineset_controller.go#L610-L618
		UpToDateReplicas: ptr.To(int32(0)),
	}

	// Convert ErrorReason/ErrorMessage to FailureReason/FailureMessage
	if mapiMachineSet.Status.ErrorReason != nil {
		capiStatus.Deprecated.V1Beta1.FailureReason = convertMAPIErrorReasonToCAPIFailureReason(*mapiMachineSet.Status.ErrorReason)
	}

	if mapiMachineSet.Status.ErrorMessage != nil {
		capiStatus.Deprecated.V1Beta1.FailureMessage = mapiMachineSet.Status.ErrorMessage
	}

	// Set Deprecated to nil if the values are zero
	if capiStatus.Deprecated.V1Beta1.FullyLabeledReplicas == 0 &&
		capiStatus.Deprecated.V1Beta1.ReadyReplicas == 0 &&
		capiStatus.Deprecated.V1Beta1.AvailableReplicas == 0 &&
		capiStatus.Deprecated.V1Beta1.FailureReason == nil &&
		capiStatus.Deprecated.V1Beta1.FailureMessage == nil &&
		len(capiStatus.Deprecated.V1Beta1.Conditions) == 0 {
		capiStatus.Deprecated = nil
	}

	// Copy the CAPI MachineSet .spec.Selector (label selector) to its status.Selector counterpart in string format.
	// Do this on a best effort basis, so only if the conversion is successful, otherwise we leave the field empty.
	statusSelector, err := metav1.LabelSelectorAsSelector(&specSelector)
	if err == nil {
		capiStatus.Selector = statusSelector.String()
	}

	// unused fields from MAPI MachineSetStatus
	// - AuthoritativeAPI: this is part of the conversion mechanism, it is not used in CAPI.
	// - SynchronizedGeneration: this is part of the conversion mechanism, it is not used in CAPI.

	return capiStatus
}

// convertMAPIErrorReasonToCAPIFailureReason converts MAPI MachineSetStatusError to CAPI MachineSetStatusError.
func convertMAPIErrorReasonToCAPIFailureReason(mapiErrorReason mapiv1beta1.MachineSetStatusError) *capierrors.MachineSetStatusError {
	capiErrorReason := capierrors.MachineSetStatusError(mapiErrorReason)
	return &capiErrorReason
}

// convertMAPIMachineSetConditionsToCAPIMachineSetConditions converts MAPI conditions to CAPI conditions.
func convertMAPIMachineSetConditionsToCAPIMachineSetConditions(mapiMachineSet *mapiv1beta1.MachineSet) clusterv1.Conditions {
	capiConditions := []clusterv1.Condition{}

	// According to https://github.com/kubernetes-sigs/cluster-api/blob/a5e21a3f92b863f65668d2140632a73003b4d76b/docs/proposals/20240916-improve-status-in-CAPI-resources.md#machineset-newconditions
	// these are the conditions that are supported by CAPI in the v1beta1 status:
	// Ready, MachinesCreated, Resized, MachinesReady.

	// CAPI ResizedCondition documents a MachineSet is resizing the set of controlled machines.
	resizedCondition := clusterv1.Condition{
		Type: clusterv1.ResizedV1Beta1Condition,
		// Compute the status for this CAPI condition based on the number of existing .status.replicas vs spec.replicas of the MAPI MachineSet.
		Status: func() corev1.ConditionStatus {
			if mapiMachineSet.Status.Replicas == ptr.Deref(mapiMachineSet.Spec.Replicas, 1) {
				return corev1.ConditionTrue
			}

			return corev1.ConditionFalse
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	// CAPI MachinesCreatedCondition documents that the machines controlled by the MachineSet are created.
	// When this condition is false, it indicates that there was an error when cloning the infrastructure/bootstrap template or
	// when generating the machine object.
	machinesCreatedCondition := clusterv1.Condition{
		Type: clusterv1.MachinesCreatedV1Beta1Condition,
		// Compute the status for this CAPI condition based on the number of existing .status.replicas vs spec.replicas of the MAPI MachineSet.
		Status: func() corev1.ConditionStatus {
			if mapiMachineSet.Status.Replicas == ptr.Deref(mapiMachineSet.Spec.Replicas, 1) {
				return corev1.ConditionTrue
			}

			return corev1.ConditionFalse
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	// CAPI MachinesReadyCondition reports an aggregate of current status of the machines controlled by the MachineSet.
	machinesReadyCondition := clusterv1.Condition{
		Type: clusterv1.MachinesReadyV1Beta1Condition,
		// Compute the status for this CAPI condition based on the number of existing .status.readyReplicas vs spec.replicas of the MAPI MachineSet.
		Status: func() corev1.ConditionStatus {
			if mapiMachineSet.Status.ReadyReplicas == ptr.Deref(mapiMachineSet.Spec.Replicas, 1) {
				return corev1.ConditionTrue
			}

			return corev1.ConditionFalse
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	// ReadyCondition defines the Ready condition type that summarizes the operational state of a Cluster API object.
	// This is a summary of the other conditions.
	readyCondition := clusterv1.Condition{
		Type: clusterv1.ReadyV1Beta1Condition,
		// Compute the status for this CAPI condition based on the status of the other conditions (resized, machinesCreated, machinesReady).
		Status: func() corev1.ConditionStatus {
			if resizedCondition.Status == corev1.ConditionTrue &&
				machinesCreatedCondition.Status == corev1.ConditionTrue &&
				machinesReadyCondition.Status == corev1.ConditionTrue {
				return corev1.ConditionTrue
			}

			return corev1.ConditionFalse
		}(),
		// LastTransitionTime will be set by the condition utilities.
	}

	capiConditions = append(capiConditions, readyCondition, resizedCondition, machinesCreatedCondition, machinesReadyCondition)

	// Sort the CAPI conditions by type, as CAPI ensures specific order of conditions.
	sort.SliceStable(capiConditions, func(i, j int) bool {
		return capiConditions[i].Type < capiConditions[j].Type
	})

	return capiConditions
}

// convertMAPIMachineSetConditionsToCAPIMachineSetV1Beta2StatusConditions converts MAPI conditions to CAPI v1beta2 conditions.
func convertMAPIMachineSetConditionsToCAPIMachineSetV1Beta2StatusConditions(mapiMachineSet *mapiv1beta1.MachineSet) []metav1.Condition {
	capiConditions := []metav1.Condition{}

	// According to https://github.com/kubernetes-sigs/cluster-api/blob/a5e21a3f92b863f65668d2140632a73003b4d76b/docs/proposals/20240916-improve-status-in-CAPI-resources.md#machineset-newconditions
	// these are the conditions that are supported by CAPI in the v1beta2 status:
	// MachinesReady, MachinesUpToDate, ScalingUp, ScalingDown, Remediating, Deleting, Paused

	// Paused documents that the MachineSet is paused.
	// We ignore paused condition at this level as it is handled by the machineSetMigration controller.

	// Remediating If the MachineSet is remediating, this condition surfaces details about ongoing remediation of the controlled machines
	// We don't have details about this on the MAPI MachineSet status, so we don't populate this condition.

	// Deleting If the MachineSet is deleted, this condition surfaces details about ongoing deletion of the controlled machines
	isDeleting := mapiMachineSet.DeletionTimestamp != nil && !mapiMachineSet.DeletionTimestamp.IsZero()
	deletingCondition := metav1.Condition{
		Type:   clusterv1.MachineSetDeletingCondition,
		Status: map[bool]metav1.ConditionStatus{true: metav1.ConditionTrue, false: metav1.ConditionFalse}[isDeleting],
		Reason: map[bool]string{true: clusterv1.MachineSetDeletingReason, false: clusterv1.MachineSetNotDeletingReason}[isDeleting],
		// LastTransitionTime will be set by the condition utilities.
	}

	// ScalingUp If the MachineSet is scaling up, this condition surfaces details about ongoing scaling up of the controlled machines
	isScalingUp := ptr.Deref(mapiMachineSet.Spec.Replicas, 1) > mapiMachineSet.Status.Replicas
	scalingUpCondition := metav1.Condition{
		Type:   clusterv1.MachineSetScalingUpCondition,
		Status: map[bool]metav1.ConditionStatus{true: metav1.ConditionTrue, false: metav1.ConditionFalse}[isScalingUp],
		Reason: map[bool]string{true: clusterv1.MachineSetScalingUpReason, false: clusterv1.MachineSetNotScalingUpReason}[isScalingUp],
		// LastTransitionTime will be set by the condition utilities.
	}

	// ScalingDown If the MachineSet is scaling down, this condition surfaces details about ongoing scaling down of the controlled machines
	isScalingDown := ptr.Deref(mapiMachineSet.Spec.Replicas, 1) < mapiMachineSet.Status.Replicas
	scalingDownCondition := metav1.Condition{
		Type:   clusterv1.MachineSetScalingDownCondition,
		Status: map[bool]metav1.ConditionStatus{true: metav1.ConditionTrue, false: metav1.ConditionFalse}[isScalingDown],
		Reason: map[bool]string{true: clusterv1.MachineSetScalingDownReason, false: clusterv1.MachineSetNotScalingDownReason}[isScalingDown],
		// LastTransitionTime will be set by the condition utilities.
	}

	// MachinesReady If the MachineSet is ready, This condition surfaces detail of issues on the controlled machines, if any
	isMachinesReady := mapiMachineSet.Status.ReadyReplicas == ptr.Deref(mapiMachineSet.Spec.Replicas, 1)
	machinesReadyCondition := metav1.Condition{
		Type:   clusterv1.MachineSetMachinesReadyCondition,
		Status: map[bool]metav1.ConditionStatus{true: metav1.ConditionTrue, false: metav1.ConditionFalse}[isMachinesReady],
		Reason: map[bool]string{true: clusterv1.MachineSetMachinesReadyReason, false: clusterv1.MachineSetMachinesNotReadyReason}[isMachinesReady],
		// LastTransitionTime will be set by the condition utilities.
	}

	// MachinesUpToDate If the MachineSet is up to date, this condition surfaces details about the status of the controlled machines
	isMachinesUpToDate := mapiMachineSet.Status.FullyLabeledReplicas == ptr.Deref(mapiMachineSet.Spec.Replicas, 1)
	machinesUpToDateCondition := metav1.Condition{
		Type:   clusterv1.MachineSetMachinesUpToDateCondition,
		Status: map[bool]metav1.ConditionStatus{true: metav1.ConditionTrue, false: metav1.ConditionFalse}[isMachinesUpToDate],
		Reason: map[bool]string{true: clusterv1.MachineSetMachinesUpToDateReason, false: clusterv1.MachineSetMachinesNotUpToDateReason}[isMachinesUpToDate],
		// LastTransitionTime will be set by the condition utilities.
	}

	capiConditions = append(capiConditions, deletingCondition, scalingUpCondition, scalingDownCondition, machinesReadyCondition, machinesUpToDateCondition)

	// Sort the CAPI conditions by type, as CAPI ensures specific order of conditions.
	sort.SliceStable(capiConditions, func(i, j int) bool {
		return capiConditions[i].Type < capiConditions[j].Type
	})

	return capiConditions
}

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

	mapiv1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ptr "k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capierrors "sigs.k8s.io/cluster-api/errors"
)

// fromMAPIMachineSetToCAPIMachineSet takes a MAPI MachineSet and returns a converted CAPI MachineSet.
func fromMAPIMachineSetToCAPIMachineSet(mapiMachineSet *mapiv1.MachineSet) (*clusterv1.MachineSet, utilerrors.Aggregate) {
	var errs field.ErrorList

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
			Selector: convertMAPIMachineSetSelectorToCAPI(mapiMachineSet.Spec.Selector),
			Replicas: mapiMachineSet.Spec.Replicas,
			// ClusterName: // ClusterName not populated here. It is added later by higher level functions
			MinReadySeconds: mapiMachineSet.Spec.MinReadySeconds,
			DeletePolicy:    cmp.Or(mapiMachineSet.Spec.DeletePolicy, string(clusterv1.RandomMachineSetDeletePolicy)), // CAPI defaults to Random if empty.
			Template: clusterv1.MachineTemplateSpec{
				ObjectMeta: clusterv1.ObjectMeta{
					Labels:      convertMAPILabelsToCAPI(mapiMachineSet.Spec.Template.Labels),
					Annotations: convertMAPIAnnotationsToCAPI(mapiMachineSet.Spec.Template.Annotations),
				},
				// Spec: // Spec not populated here. It is added later by higher level functions.
			},
			// MachineNamingStrategy: // Not supported in MAPI, remains nil. No equivalent field in MAPI MachineSet.
			// AuthoritativeAPI: // Ignore, this is part of the conversion mechanism.
		},
		Status: convertMAPIMachineSetStatusToCAPI(mapiMachineSet.Status),
	}

	errs = append(errs, handleUnsupportedMAPIObjectMetaFields(field.NewPath("spec", "template", "metadata"), mapiMachineSet.Spec.Template.ObjectMeta)...)

	return capiMachineSet, errs.ToAggregate()
}

// convertMAPIMachineSetStatusToCAPI converts a MAPI MachineSetStatus to CAPI format.
func convertMAPIMachineSetStatusToCAPI(mapiStatus mapiv1.MachineSetStatus) clusterv1.MachineSetStatus {
	capiStatus := clusterv1.MachineSetStatus{
		Selector:             "", // TODO(damdo): check this: CAPI Selector field is not available in MAPI, will be populated by CAPI controller
		Replicas:             mapiStatus.Replicas,
		FullyLabeledReplicas: mapiStatus.FullyLabeledReplicas,
		ReadyReplicas:        mapiStatus.ReadyReplicas,
		AvailableReplicas:    mapiStatus.AvailableReplicas,
		// ObservedGeneration: // Ignore, this field as it shouldn't match between CAPI and MAPI.
		Conditions: convertMAPIMachineSetConditionsToCAPIMachineSetConditions(mapiStatus.Conditions),
		V1Beta2:    convertMAPIMachineSetStatusToCAPIMachineSetV1Beta2Status(mapiStatus),
	}

	// Convert ErrorReason/ErrorMessage to FailureReason/FailureMessage
	if mapiStatus.ErrorReason != nil {
		capiStatus.FailureReason = convertMAPIErrorReasonToCAPIFailureReason(*mapiStatus.ErrorReason)
	}
	if mapiStatus.ErrorMessage != nil {
		capiStatus.FailureMessage = mapiStatus.ErrorMessage
	}

	return capiStatus
}

func convertMAPIMachineSetStatusToCAPIMachineSetV1Beta2Status(mapiStatus mapiv1.MachineSetStatus) *clusterv1.MachineSetV1Beta2Status {
	return &clusterv1.MachineSetV1Beta2Status{
		ReadyReplicas:     ptr.To(mapiStatus.ReadyReplicas),
		AvailableReplicas: ptr.To(mapiStatus.AvailableReplicas),
		UpToDateReplicas:  ptr.To(mapiStatus.FullyLabeledReplicas), // TODO(damdo): is it ok to do this?
		Conditions:        convertMAPIMachineSetConditionsToCAPIMachineSetV1Beta2StatusConditions(mapiStatus.Conditions),
	}
}

// convertMAPIErrorReasonToCAPIFailureReason converts MAPI MachineSetStatusError to CAPI MachineSetStatusError.
func convertMAPIErrorReasonToCAPIFailureReason(mapiErrorReason mapiv1.MachineSetStatusError) *capierrors.MachineSetStatusError {
	capiErrorReason := capierrors.MachineSetStatusError(mapiErrorReason)
	return &capiErrorReason
}

// convertMAPIMachineSetConditionsToCAPIMachineSetConditions converts MAPI conditions to CAPI conditions.
func convertMAPIMachineSetConditionsToCAPIMachineSetConditions(mapiConditions []mapiv1.Condition) clusterv1.Conditions {
	if mapiConditions == nil {
		return nil
	}

	capiConditions := make(clusterv1.Conditions, 0, len(mapiConditions))

	for _, mapiCondition := range mapiConditions {
		// Ignore MAPI specific conditions.
		// TODO(damdo): Make sure we only convert the conditions that are supported by CAPI.
		if mapiCondition.Type == "Paused" || mapiCondition.Type == "Synchronized" {
			continue
		}

		capiCondition := clusterv1.Condition{
			Type:               clusterv1.ConditionType(mapiCondition.Type),
			Status:             mapiCondition.Status,
			LastTransitionTime: mapiCondition.LastTransitionTime,
			Reason:             mapiCondition.Reason,
			Message:            mapiCondition.Message,
		}
		// Severity must only be set when the condition is not True.
		if mapiCondition.Status != corev1.ConditionTrue && mapiCondition.Severity != "" {
			capiCondition.Severity = clusterv1.ConditionSeverity(mapiCondition.Severity)
		}

		capiConditions = append(capiConditions, capiCondition)
	}

	return capiConditions
}

func convertMAPIMachineSetConditionsToCAPIMachineSetV1Beta2StatusConditions(mapiConditions []mapiv1.Condition) []metav1.Condition {
	if mapiConditions == nil {
		return nil
	}

	capiConditions := make([]metav1.Condition, 0, len(mapiConditions))

	for _, mapiCondition := range mapiConditions {
		// Ignore MAPI specific conditions.
		// TODO(damdo): Make sure we only convert the conditions that are supported by CAPI.
		if mapiCondition.Type == "Paused" || mapiCondition.Type == "Synchronized" {
			continue
		}

		capiCondition := metav1.Condition{
			Type:               string(mapiCondition.Type),
			Status:             metav1.ConditionStatus(mapiCondition.Status),
			LastTransitionTime: mapiCondition.LastTransitionTime,
			// Severity is not supported by CAPI v1beta2 status condition (metav1.Condition).
			Reason:  mapiCondition.Reason,
			Message: mapiCondition.Message,
		}

		capiConditions = append(capiConditions, capiCondition)
	}

	return capiConditions
}

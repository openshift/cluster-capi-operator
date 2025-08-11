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
package capi2mapi

import (
	mapiv1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capierrors "sigs.k8s.io/cluster-api/errors"
)

// fromCAPIMachineSetToMAPIMachineSet takes a CAPI MachineSet and returns a converted MAPI MachineSet.
func fromCAPIMachineSetToMAPIMachineSet(capiMachineSet *clusterv1.MachineSet) (*mapiv1.MachineSet, error) {
	errs := field.ErrorList{}

	mapiMachineSet := &mapiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            capiMachineSet.Name,
			Namespace:       capiMachineSet.Namespace,
			Labels:          convertCAPILabelsToMAPILabels(capiMachineSet.Labels),
			Annotations:     convertCAPIAnnotationsToMAPIAnnotations(capiMachineSet.Annotations),
			Finalizers:      nil, // MAPI MachineSet does not have finalizers.
			OwnerReferences: nil, // OwnerReferences not populated here. They are added later by the machineSetSync controller.
		},
		Spec: mapiv1.MachineSetSpec{
			Selector:        convertCAPIMachineSetSelectorToMAPI(capiMachineSet.Spec.Selector),
			Replicas:        capiMachineSet.Spec.Replicas,
			MinReadySeconds: capiMachineSet.Spec.MinReadySeconds,
			DeletePolicy:    capiMachineSet.Spec.DeletePolicy,
			Template: mapiv1.MachineTemplateSpec{
				ObjectMeta: mapiv1.ObjectMeta{
					Labels:      convertCAPILabelsToMAPILabels(capiMachineSet.Spec.Template.Labels),
					Annotations: convertCAPIAnnotationsToMAPIAnnotations(capiMachineSet.Spec.Template.Annotations),
				},
			},
		},
		Status: convertCAPIMachineSetStatusToMAPI(capiMachineSet.Status, capiMachineSet.Generation),
	}

	// Unused fields - Below this line are fields not used from the CAPI Machine.
	// metadata.OwnerReferences - handled by the machineSetSync controller.
	// capiMachineSet.Spec.ClusterName - Ignore this as it can be reconstructed from the infra object.
	// capiMachineSet.Spec.Template.Spec - Ignore as we convert this at a higher level using the Machine conversion logic.
	// capiMachineSet.Spec.MachineNamingStrategy - Not supported in MAPI, no equivalent field exists.

	if len(errs) > 0 {
		// Return the mapiMachine so that the logic continues and collects all possible conversion errors.
		return mapiMachineSet, errs.ToAggregate()
	}

	return mapiMachineSet, nil
}

// convertCAPIMachineSetStatusToMAPI converts a CAPI MachineSetStatus to MAPI format.
func convertCAPIMachineSetStatusToMAPI(capiStatus clusterv1.MachineSetStatus, observedGeneration int64) mapiv1.MachineSetStatus {
	mapiStatus := mapiv1.MachineSetStatus{
		Replicas:             capiStatus.Replicas,
		FullyLabeledReplicas: capiStatus.FullyLabeledReplicas,
		ReadyReplicas:        capiStatus.ReadyReplicas,
		AvailableReplicas:    capiStatus.AvailableReplicas,
		ObservedGeneration:   observedGeneration, // Set the observed generation to the current CAPI MachineSet generation.
		// AuthoritativeAPI: // Ignore, this field as it is not present in CAPI.
		// SynchronizedGeneration: // Ignore, this field as it is not present in CAPI.
		Conditions: convertCAPIConditionsToMAPI(capiStatus.Conditions),
	}

	// Convert FailureReason/FailureMessage to ErrorReason/ErrorMessage
	if capiStatus.FailureReason != nil {
		mapiStatus.ErrorReason = convertCAPIFailureReasonToMAPIErrorReason(*capiStatus.FailureReason)
	}

	if capiStatus.FailureMessage != nil {
		mapiStatus.ErrorMessage = capiStatus.FailureMessage
	}

	// unused fields from CAPI MachineSetStatus
	// - Selector: label selection is different between CAPI and MAPI.
	// - V1Beta2: for now we use the V1Beta1 status fields to obtain the status of the MAPI MachineSet.

	return mapiStatus
}

// convertCAPIFailureReasonToMAPIErrorReason converts CAPI MachineSetStatusError to MAPI MachineSetStatusError.
func convertCAPIFailureReasonToMAPIErrorReason(capiFailureReason capierrors.MachineSetStatusError) *mapiv1.MachineSetStatusError {
	mapiErrorReason := mapiv1.MachineSetStatusError(capiFailureReason)
	return &mapiErrorReason
}

// convertCAPIConditionsToMAPI converts CAPI conditions to MAPI conditions.
func convertCAPIConditionsToMAPI(capiConditions clusterv1.Conditions) []mapiv1.Condition {
	if capiConditions == nil {
		return nil
	}

	mapiConditions := make([]mapiv1.Condition, 0, len(capiConditions))

	for _, capiCondition := range capiConditions {
		// Ignore CAPI specific conditions.
		// TODO(damdo): Make sure we only convert the conditions that are supported by MAPI.
		if capiCondition.Type == "Paused" || capiCondition.Type == "MachinesCreated" || capiCondition.Type == "Ready" || capiCondition.Type == "Synchronized" {
			continue
		}

		mapiCondition := mapiv1.Condition{
			Type:               mapiv1.ConditionType(capiCondition.Type),
			Status:             capiCondition.Status,
			LastTransitionTime: capiCondition.LastTransitionTime,
			Reason:             capiCondition.Reason,
			Message:            capiCondition.Message,
		}

		// Severity must only be set when the condition is not True.
		if capiCondition.Status != corev1.ConditionTrue && capiCondition.Severity != "" {
			mapiCondition.Severity = mapiv1.ConditionSeverity(capiCondition.Severity)
		}

		mapiConditions = append(mapiConditions, mapiCondition)
	}

	return mapiConditions
}

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
package util

import (
	"reflect"

	"github.com/go-test/deep"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetLastTransitionTime determines if the last transition time should be set or updated for a given condition type.
func SetLastTransitionTime(condType mapiv1beta1.ConditionType, conditions []mapiv1beta1.Condition, conditionAc *machinev1applyconfigs.ConditionApplyConfiguration) {
	for _, condition := range conditions {
		if condition.Type == condType {
			if !hasSameState(&condition, conditionAc) {
				conditionAc.WithLastTransitionTime(metav1.Now())

				return
			}

			conditionAc.WithLastTransitionTime(condition.LastTransitionTime)

			return
		}
	}
	// Condition does not exist; set the transition time
	conditionAc.WithLastTransitionTime(metav1.Now())
}

// HasSameState returns true if a condition has the same state as a condition
// apply config; state is defined by the union of following fields: Type,
// Status.
func hasSameState(i *mapiv1beta1.Condition, j *machinev1applyconfigs.ConditionApplyConfiguration) bool {
	return i.Type == *j.Type &&
		i.Status == *j.Status
}

// ObjectMetaEqual compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising MAPI and CAPI Machines.
func ObjectMetaEqual(a, b metav1.ObjectMeta) map[string]any {
	objectMetaDiff := map[string]any{}

	if diffLabels := deep.Equal(a.Labels, b.Labels); len(diffLabels) > 0 {
		objectMetaDiff[".labels"] = diffLabels
	}

	// Ignore conversion-data because this data is managed by Cluster API for down conversion.
	aAnnotations := a.DeepCopy().GetAnnotations()
	delete(aAnnotations, "cluster.x-k8s.io/conversion-data")

	bAnnotations := b.DeepCopy().GetAnnotations()
	delete(bAnnotations, "cluster.x-k8s.io/conversion-data")

	if diffAnnotations := deep.Equal(aAnnotations, bAnnotations); len(diffAnnotations) > 0 {
		objectMetaDiff[".annotations"] = diffAnnotations
	}

	// Ignore the differences in finalizers, as CAPI always put finalizers on its resources
	// even when its controllers are paused:
	// https://github.com/kubernetes-sigs/cluster-api/blob/c70dca0fc387b44457c69b71a719132a0d9bed58/internal/controllers/machine/machine_controller.go#L207-L210

	if diffOwnerReferences := deep.Equal(a.OwnerReferences, b.OwnerReferences); len(diffOwnerReferences) > 0 {
		objectMetaDiff[".ownerReferences"] = diffOwnerReferences
	}

	return objectMetaDiff
}

// CAPIMachineSetStatusEqual compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising MAPI and CAPI Machines.
func CAPIMachineSetStatusEqual(a, b clusterv1beta1.MachineSetStatus) map[string]any {
	diff := map[string]any{}

	if diffReadyReplicas := deep.Equal(a.ReadyReplicas, b.ReadyReplicas); len(diffReadyReplicas) > 0 {
		diff[".readyReplicas"] = diffReadyReplicas
	}

	if diffAvailableReplicas := deep.Equal(a.AvailableReplicas, b.AvailableReplicas); len(diffAvailableReplicas) > 0 {
		diff[".availableReplicas"] = diffAvailableReplicas
	}

	if diffFullyLabeledReplicas := deep.Equal(a.FullyLabeledReplicas, b.FullyLabeledReplicas); len(diffFullyLabeledReplicas) > 0 {
		diff[".fullyLabeledReplicas"] = diffFullyLabeledReplicas
	}

	if diffFailureReason := deep.Equal(a.FailureReason, b.FailureReason); len(diffFailureReason) > 0 {
		diff[".failureReason"] = diffFailureReason
	}

	if diffFailureMessage := deep.Equal(a.FailureMessage, b.FailureMessage); len(diffFailureMessage) > 0 {
		diff[".failureMessage"] = diffFailureMessage
	}

	if diffConditions := compareCAPIV1Beta1Conditions(a.Conditions, b.Conditions); len(diffConditions) > 0 {
		diff[".conditions"] = diffConditions
	}

	// Compare the v1beta2 fields.
	if a.V1Beta2 == nil {
		a.V1Beta2 = &clusterv1beta1.MachineSetV1Beta2Status{}
	}

	if b.V1Beta2 == nil {
		b.V1Beta2 = &clusterv1beta1.MachineSetV1Beta2Status{}
	}

	if diffUpToDateReplicas := deep.Equal(a.V1Beta2.UpToDateReplicas, b.V1Beta2.UpToDateReplicas); len(diffUpToDateReplicas) > 0 {
		diff[".v1beta2.upToDateReplicas"] = diffUpToDateReplicas
	}

	if diffAvailableReplicas := deep.Equal(a.V1Beta2.AvailableReplicas, b.V1Beta2.AvailableReplicas); len(diffAvailableReplicas) > 0 {
		diff[".v1beta2.availableReplicas"] = diffAvailableReplicas
	}

	if diffReadyReplicas := deep.Equal(a.V1Beta2.ReadyReplicas, b.V1Beta2.ReadyReplicas); len(diffReadyReplicas) > 0 {
		diff[".v1beta2.readyReplicas"] = diffReadyReplicas
	}

	if diffConditions := compareCAPIV1Beta2Conditions(a.V1Beta2.Conditions, b.V1Beta2.Conditions); len(diffConditions) > 0 {
		diff[".v1beta2.conditions"] = diffConditions
	}

	return diff
}

// CAPIMachineStatusEqual compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising CAPI and MAPI Machines.
func CAPIMachineStatusEqual(a, b clusterv1beta1.MachineStatus) map[string]any {
	diff := map[string]any{}

	if diffFailureReason := deep.Equal(a.FailureReason, b.FailureReason); len(diffFailureReason) > 0 {
		diff[".failureReason"] = diffFailureReason
	}

	if diffFailureMessage := deep.Equal(a.FailureMessage, b.FailureMessage); len(diffFailureMessage) > 0 {
		diff[".failureMessage"] = diffFailureMessage
	}

	if diffLastUpdated := deep.Equal(a.LastUpdated, b.LastUpdated); len(diffLastUpdated) > 0 {
		diff[".lastUpdated"] = diffLastUpdated
	}

	if diffPhase := deep.Equal(a.Phase, b.Phase); len(diffPhase) > 0 {
		diff[".phase"] = diffPhase
	}

	if diffAddresses := deep.Equal(a.Addresses, b.Addresses); len(diffAddresses) > 0 {
		diff[".addresses"] = diffAddresses
	}

	if diffBootstrapReady := deep.Equal(a.BootstrapReady, b.BootstrapReady); len(diffBootstrapReady) > 0 {
		diff[".bootstrapReady"] = diffBootstrapReady
	}

	if diffInfrastructureReady := deep.Equal(a.InfrastructureReady, b.InfrastructureReady); len(diffInfrastructureReady) > 0 {
		diff[".infrastructureReady"] = diffInfrastructureReady
	}

	if diffNodeInfo := deep.Equal(a.NodeInfo, b.NodeInfo); len(diffNodeInfo) > 0 {
		diff[".nodeInfo"] = diffNodeInfo
	}

	if diffConditions := compareCAPIV1Beta1Conditions(a.Conditions, b.Conditions); len(diffConditions) > 0 {
		diff[".conditions"] = diffConditions
	}

	// Compare the v1beta2 fields.
	if a.V1Beta2 == nil {
		a.V1Beta2 = &clusterv1beta1.MachineV1Beta2Status{}
	}

	if b.V1Beta2 == nil {
		b.V1Beta2 = &clusterv1beta1.MachineV1Beta2Status{}
	}

	if diffConditions := compareCAPIV1Beta2Conditions(a.V1Beta2.Conditions, b.V1Beta2.Conditions); len(diffConditions) > 0 {
		diff[".v1beta2.conditions"] = diffConditions
	}

	return diff
}

// MAPIMachineStatusEqual compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising CAPI and MAPI Machines.
func MAPIMachineStatusEqual(a, b mapiv1beta1.MachineStatus) map[string]any {
	diff := map[string]any{}

	if diffConditions := compareMAPIV1Beta1Conditions(a.Conditions, b.Conditions); len(diffConditions) > 0 {
		diff[".conditions"] = diffConditions
	}

	if diffErrorReason := deep.Equal(a.ErrorReason, b.ErrorReason); len(diffErrorReason) > 0 {
		diff[".errorReason"] = diffErrorReason
	}

	if diffErrorMessage := deep.Equal(a.ErrorMessage, b.ErrorMessage); len(diffErrorMessage) > 0 {
		diff[".errorMessage"] = diffErrorMessage
	}

	if diffPhase := deep.Equal(a.Phase, b.Phase); len(diffPhase) > 0 {
		diff[".phase"] = diffPhase
	}

	if diffAddresses := deep.Equal(a.Addresses, b.Addresses); len(diffAddresses) > 0 {
		diff[".addresses"] = diffAddresses
	}

	if diffNodeRef := deep.Equal(a.NodeRef, b.NodeRef); len(diffNodeRef) > 0 {
		diff[".nodeRef"] = diffNodeRef
	}

	if diffLastUpdated := deep.Equal(a.LastUpdated, b.LastUpdated); len(diffLastUpdated) > 0 {
		diff[".lastUpdated"] = diffLastUpdated
	}

	return diff
}

// compareCAPIV1Beta1Conditions compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising CAPI v1beta1 and MAPI.
func compareCAPIV1Beta1Conditions(a, b []clusterv1beta1.Condition) []string {
	diff := []string{}
	// Compare the conditions one by one.
	// Ignore the differences in LastTransitionTime.
	for _, condition := range a {
		for _, conditionB := range b {
			if condition.Type == conditionB.Type {
				if condition.Status != conditionB.Status ||
					condition.Severity != conditionB.Severity ||
					condition.Reason != conditionB.Reason ||
					condition.Message != conditionB.Message {
					diff = append(diff, deep.Equal(condition, conditionB)...)
				}

				break // Break out of the inner loop once we have found a match.
			}
		}
	}

	return diff
}

// compareMAPIV1Beta1Conditions compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising MAPI v1beta1 and CAPI.
func compareMAPIV1Beta1Conditions(a, b []mapiv1beta1.Condition) []string {
	diff := []string{}
	// Compare the conditions one by one.
	// Ignore the differences in LastTransitionTime.
	for _, condition := range a {
		for _, conditionB := range b {
			if condition.Type == conditionB.Type {
				if condition.Status != conditionB.Status ||
					condition.Severity != conditionB.Severity ||
					condition.Reason != conditionB.Reason ||
					condition.Message != conditionB.Message {
					diff = append(diff, deep.Equal(condition, conditionB)...)
				}

				break // Break out of the inner loop once we have found a match.
			}
		}
	}

	return diff
}

// compareCAPIV1Beta2Conditions compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising CAPI v1beta2 and MAPI.
func compareCAPIV1Beta2Conditions(a, b []metav1.Condition) []string {
	diff := []string{}
	// Compare the conditions one by one.
	// Ignore the differences in LastTransitionTime.
	for _, condition := range a {
		for _, conditionB := range b {
			if condition.Type == conditionB.Type {
				if condition.Status != conditionB.Status ||
					condition.Reason != conditionB.Reason ||
					condition.Message != conditionB.Message {
					diff = append(diff, deep.Equal(condition, conditionB)...)
				}

				break // Break out of the inner loop once we have found a match.
			}
		}
	}

	return diff
}

// MAPIMachineSetStatusEqual compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising MAPI and CAPI Machines.
func MAPIMachineSetStatusEqual(a, b mapiv1beta1.MachineSetStatus) map[string]any {
	diff := map[string]any{}

	if diffReadyReplicas := deep.Equal(a.ReadyReplicas, b.ReadyReplicas); len(diffReadyReplicas) > 0 {
		diff[".readyReplicas"] = diffReadyReplicas
	}

	if diffAvailableReplicas := deep.Equal(a.AvailableReplicas, b.AvailableReplicas); len(diffAvailableReplicas) > 0 {
		diff[".availableReplicas"] = diffAvailableReplicas
	}

	if diffFullyLabeledReplicas := deep.Equal(a.FullyLabeledReplicas, b.FullyLabeledReplicas); len(diffFullyLabeledReplicas) > 0 {
		diff[".fullyLabeledReplicas"] = diffFullyLabeledReplicas
	}

	if diffErrorReason := deep.Equal(a.ErrorReason, b.ErrorReason); len(diffErrorReason) > 0 {
		diff[".errorReason"] = diffErrorReason
	}

	if diffErrorMessage := deep.Equal(a.ErrorMessage, b.ErrorMessage); len(diffErrorMessage) > 0 {
		diff[".errorMessage"] = diffErrorMessage
	}

	if diffConditions := compareMAPIV1Beta1Conditions(a.Conditions, b.Conditions); len(diffConditions) > 0 {
		diff[".conditions"] = diffConditions
	}

	return diff
}

// GetResourceVersion returns the object ResourceVersion or the zero value for it.
func GetResourceVersion(obj client.Object) string {
	if obj == nil || reflect.ValueOf(obj).IsNil() {
		return "0"
	}

	return obj.GetResourceVersion()
}

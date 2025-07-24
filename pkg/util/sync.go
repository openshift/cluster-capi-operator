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
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetLastTransitionTime determines if the last transition time should be set or updated for a given condition type.
func SetLastTransitionTime(condType machinev1beta1.ConditionType, conditions []machinev1beta1.Condition, conditionAc *machinev1applyconfigs.ConditionApplyConfiguration) {
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
func hasSameState(i *machinev1beta1.Condition, j *machinev1applyconfigs.ConditionApplyConfiguration) bool {
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

	if diffAnnotations := deep.Equal(a.Annotations, b.Annotations); len(diffAnnotations) > 0 {
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
func CAPIMachineSetStatusEqual(a, b clusterv1.MachineSetStatus) map[string]any { //nolint:dupl
	diff := map[string]any{}

	if diffConditions := compareCAPIMachineSetConditions(a.Conditions, b.Conditions); len(diffConditions) > 0 {
		diff[".conditions"] = diffConditions
	}

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

	return diff
}

func compareCAPIMachineSetConditions(a, b []clusterv1.Condition) []string {
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
			}
		}
	}

	return diff
}

func compareMAPIMachineSetConditions(a, b []machinev1beta1.Condition) []string {
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
			}
		}
	}

	return diff
}

// MAPIMachineSetStatusEqual compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising MAPI and CAPI Machines.
func MAPIMachineSetStatusEqual(a, b machinev1beta1.MachineSetStatus) map[string]any { //nolint:dupl
	diff := map[string]any{}

	if diffConditions := compareMAPIMachineSetConditions(a.Conditions, b.Conditions); len(diffConditions) > 0 {
		diff[".conditions"] = diffConditions
	}

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

	return diff
}

// GetResourceVersion returns the object ResourceVersion or the zero value for it.
func GetResourceVersion(obj client.Object) string {
	if obj == nil || reflect.ValueOf(obj).IsNil() {
		return "0"
	}

	return obj.GetResourceVersion()
}

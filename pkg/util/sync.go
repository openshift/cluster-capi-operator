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

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
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
func ObjectMetaEqual(a, b metav1.ObjectMeta) (DiffResult, error) {
	differ := NewDiffer(
		// Special handling for CAPI's conversion-data label.
		WithIgnoreField("annotations", "cluster.x-k8s.io/conversion-data"),

		WithIgnoreField("name"),
		WithIgnoreField("generateName"),
		WithIgnoreField("namespace"),
		WithIgnoreField("selfLink"),
		WithIgnoreField("uid"),
		WithIgnoreField("resourceVersion"),
		WithIgnoreField("generation"),
		WithIgnoreField("creationTimestamp"),
		WithIgnoreField("deletionTimestamp"),
		WithIgnoreField("deletionGracePeriodSeconds"),
		WithIgnoreField("finalizers"),
		WithIgnoreField("managedFields"),
	)

	return differ.Diff(a, b)
}

// CAPIMachineSetStatusEqual compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising MAPI and CAPI Machines.
func CAPIMachineSetStatusEqual(a, b clusterv1.MachineSetStatus) (DiffResult, error) {
	differ := NewDiffer(
		// Changes to a condition's LastTransitionTime should not cause updates because it should only change
		// if the status itself changes.
		WithIgnoreConditionsLastTransitionTime(),
	)

	return differ.Diff(a, b)
}

// CAPIMachineStatusEqual compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising CAPI and MAPI Machines.
func CAPIMachineStatusEqual(a, b clusterv1.MachineStatus) (DiffResult, error) {
	differ := NewDiffer(
		// Changes to a condition's LastTransitionTime should not cause updates because it should only change
		// if the status itself changes.
		WithIgnoreConditionsLastTransitionTime(),
	)

	return differ.Diff(a, b)
}

// MAPIMachineStatusEqual compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising CAPI and MAPI Machines.
func MAPIMachineStatusEqual(a, b mapiv1beta1.MachineStatus) (DiffResult, error) {
	differ := NewDiffer(
		// Changes to a condition's LastTransitionTime should not cause updates because it should only change
		// if the status itself changes.
		WithIgnoreConditionsLastTransitionTime(),

		WithIgnoreField("providerStatus"),
		WithIgnoreField("lastOperation"),
		WithIgnoreField("authoritativeAPI"),
		WithIgnoreField("synchronizedGeneration"),
	)

	return differ.Diff(a, b)
}

// MAPIMachineSetStatusEqual compares variables a and b,
// and returns a list of differences, or nil if there are none,
// for the fields we care about when synchronising MAPI and CAPI Machines.
func MAPIMachineSetStatusEqual(a, b mapiv1beta1.MachineSetStatus) (DiffResult, error) {
	differ := NewDiffer(
		// Changes to a condition's LastTransitionTime should not cause updates because it should only change
		// if the status itself changes.
		WithIgnoreConditionsLastTransitionTime(),

		WithIgnoreField("replicas"),
		WithIgnoreField("observedGeneration"),
		WithIgnoreField("authoritativeAPI"),
		WithIgnoreField("synchronizedGeneration"),
	)

	return differ.Diff(a, b)
}

// GetResourceVersion returns the object ResourceVersion or the zero value for it.
func GetResourceVersion(obj client.Object) string {
	if obj == nil || reflect.ValueOf(obj).IsNil() {
		return "0"
	}

	return obj.GetResourceVersion()
}

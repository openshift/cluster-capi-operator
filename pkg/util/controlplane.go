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

package util

import (
	"strings"

	mapiv1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	// ControlPlaneMachineSetKind is the kind used by the ControlPlaneMachineSet resource.
	ControlPlaneMachineSetKind = "ControlPlaneMachineSet"
)

// IsControlPlaneMAPIMachine returns true if the given MAPI Machine is a control plane machine.
func IsControlPlaneMAPIMachine(machine *mapiv1.Machine) bool {
	if machine == nil {
		return false
	}

	if hasOwnerKind(machine.OwnerReferences, ControlPlaneMachineSetKind) {
		return true
	}

	if role, exists := machine.Labels["machine.openshift.io/cluster-api-machine-role"]; exists && role == "master" {
		return true
	}

	return false
}

// IsControlPlaneCAPIMachine returns true if the given CAPI Machine is a control plane machine.
func IsControlPlaneCAPIMachine(machine *clusterv1.Machine) bool {
	if machine == nil {
		return false
	}

	for labelKey := range machine.Labels {
		if strings.HasPrefix(labelKey, clusterv1.NodeRoleLabelPrefix) {
			if _, role, found := strings.Cut(labelKey, "/"); found && (role == "master" || role == "control-plane") {
				return true
			}
		}
		// Also accept the exact control-plane label key with empty value
		if labelKey == "node-role.kubernetes.io/control-plane" {
			return true
		}
	}

	return false
}

func hasOwnerKind(refs []metav1.OwnerReference, kind string) bool {
	for _, ownerRef := range refs {
		if ownerRef.Kind == kind {
			return true
		}
	}
	return false
}

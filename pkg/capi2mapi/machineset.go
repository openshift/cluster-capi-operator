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
	"strings"

	mapiv1 "github.com/openshift/api/machine/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	mapiMachineSetAPIVersion = "machine.openshift.io/v1beta1"
	mapiMachineSetKind       = "MachineSet"
)

// fromMachineSetToMachineSet takes a CAPI MachineSet and returns a converted MAPI MachineSet.
func fromMachineSetToMachineSet(capiMachineSet *capiv1.MachineSet) *mapiv1.MachineSet {
	mapiMachineSet := &mapiv1.MachineSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       mapiMachineSetKind,
			APIVersion: mapiMachineSetAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        capiMachineSet.Name,
			Namespace:   capiMachineSet.Namespace,
			Labels:      capiMachineSet.Labels,
			Annotations: capiMachineSet.Annotations,
		},
	}

	mapiMachineSet.Spec.Selector = capiMachineSet.Spec.Selector
	mapiMachineSet.Spec.Template.Labels = capiMachineSet.Spec.Template.Labels
	mapiMachineSet.Spec.Replicas = capiMachineSet.Spec.Replicas

	for k, v := range capiMachineSet.Spec.Template.Labels {
		// Only CAPI managed labels are propagated down to the kubernetes nodes.
		// So only put those back to the MAPI Machine's Spec.ObjectMeta.Labels.
		// See: https://github.com/kubernetes-sigs/cluster-api/pull/7173
		// and: https://github.com/fabriziopandini/cluster-api/blob/main/docs/proposals/20220927-label-sync-between-machine-and-nodes.md
		if strings.HasPrefix(k, capiv1.NodeRoleLabelPrefix) || k == capiv1.ManagedNodeLabelDomain || k == capiv1.NodeRestrictionLabelDomain {
			if mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels == nil {
				mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels = map[string]string{}
			}

			mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels[k] = v
		}
	}

	setCAPIManagedNodeLabelsToMAPINodeLabels(capiMachineSet.Spec.Template.Labels, mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels)

	return mapiMachineSet
}

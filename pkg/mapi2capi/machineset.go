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
	mapiv1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// fromMachineSetToMachineSet takes a MAPI MachineSet and returns a converted CAPI MachineSet.
func fromMachineSetToMachineSet(mapiMachineSet *mapiv1.MachineSet) *capiv1.MachineSet {
	capiMachineSet := &capiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        mapiMachineSet.Name,
			Namespace:   mapiMachineSet.Namespace,
			Labels:      mapiMachineSet.Labels,
			Annotations: mapiMachineSet.Annotations,
		},
	}

	capiMachineSet.Spec.Selector = mapiMachineSet.Spec.Selector
	capiMachineSet.Spec.Template.Labels = mapiMachineSet.Spec.Template.Labels
	capiMachineSet.Spec.Template.Spec.ProviderID = mapiMachineSet.Spec.Template.Spec.ProviderID
	// capiMachineSet.Spec.ClusterName // populated by higher level functions
	capiMachineSet.Spec.Replicas = mapiMachineSet.Spec.Replicas
	// capiMachineSet.Spec.Template.Spec.ClusterName // populated by higher level functions
	capiMachineSet.Spec.Template.Spec.InfrastructureRef = corev1.ObjectReference{
		APIVersion: awsTemplateAPIVersion,
		Kind:       awsTemplateKind,
		Name:       mapiMachineSet.Name,
	}

	setMAPINodeLabelsToCAPIManagedNodeLabels(mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels, capiMachineSet.Spec.Template.ObjectMeta.Labels)

	return capiMachineSet
}

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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// fromCAPIMachineSetToMAPIMachineSet takes a CAPI MachineSet and returns a converted MAPI MachineSet.
func fromCAPIMachineSetToMAPIMachineSet(capiMachineSet *capiv1.MachineSet) (*mapiv1.MachineSet, error) {
	errs := field.ErrorList{}

	mapiMachineSet := &mapiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            capiMachineSet.Name,
			Namespace:       capiMachineSet.Namespace,
			Labels:          convertCAPILabelsToMAPILabels(capiMachineSet.Labels),
			Annotations:     convertCAPIAnnotationsToMAPIAnnotations(capiMachineSet.Annotations),
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
	}

	// Unused fields - Below this line are fields not used from the CAPI Machine.
	// metadata.OwnerReferences - handled by the machineSetSync controller.
	// capiMachineSet.Spec.ClusterName - Ignore this as it can be reconstructed from the infra object.
	// capiMachineSet.Spec.Template.Spec - Ignore as we convert this at a higher level using the Machine conversion logic.

	if len(errs) > 0 {
		// Return the mapiMachine so that the logic continues and collects all possible conversion errors.
		return mapiMachineSet, errs.ToAggregate()
	}

	return mapiMachineSet, nil
}

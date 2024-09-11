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
	"k8s.io/apimachinery/pkg/util/validation/field"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	mapiMachineSetAPIVersion = "machine.openshift.io/v1beta1"
	mapiMachineSetKind       = "MachineSet"
)

// fromCAPIMachineSetToMAPIMachineSet takes a CAPI MachineSet and returns a converted MAPI MachineSet.
func fromCAPIMachineSetToMAPIMachineSet(capiMachineSet *capiv1.MachineSet) (*mapiv1.MachineSet, error) {
	errs := field.ErrorList{}

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
			// OwnerReferences: There shouldn't be any OwnerReferences on a MachineSet.
		},
		Spec: mapiv1.MachineSetSpec{
			Selector:        capiMachineSet.Spec.Selector,
			Replicas:        capiMachineSet.Spec.Replicas,
			MinReadySeconds: capiMachineSet.Spec.MinReadySeconds,
			DeletePolicy:    capiMachineSet.Spec.DeletePolicy,
			Template: mapiv1.MachineTemplateSpec{
				ObjectMeta: mapiv1.ObjectMeta{
					Labels:      capiMachineSet.Spec.Template.Labels,
					Annotations: capiMachineSet.Spec.Template.Annotations,
				},
			},
		},
	}

	if len(capiMachineSet.OwnerReferences) > 0 {
		// TODO(OCPCLOUD-XXXX): We should prevent ownerreferences on MachineSets until such a time that we need to support them.
		errs = append(errs, field.Invalid(field.NewPath("metadata", "ownerReferences"), capiMachineSet.OwnerReferences, "ownerReferences are not supported"))
	}

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

	// Unusued fields - Below this line are fields not used from the CAPI Machine.

	// capiMachineSet.Spec.ClusterName - Ignore this as it can be reconstructed from the infra object.
	// capiMachineSet.Spec.Template.Spec - Ignore as we convert this at a higher level using the Machine conversion logic.

	if len(errs) > 0 {
		// Return the mapiMachine so that the logic continues and collects all possible conversion errors.
		return mapiMachineSet, errs.ToAggregate()
	}

	return mapiMachineSet, nil
}

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
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// fromMAPIMachineToCAPIMachine translates a MAPI Machine to its Core CAPI Machine correspondent.
func fromMAPIMachineToCAPIMachine(mapiMachine *mapiv1beta1.Machine, apiVersion, kind string) (*capiv1.Machine, field.ErrorList) {
	var errs field.ErrorList

	capiMachine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:        mapiMachine.Name,
			Namespace:   capiNamespace,
			Labels:      mapiMachine.Labels,
			Annotations: mapiMachine.Annotations,
			// OwnerReferences: TODO(OCPCLOUD-2716): These need to be converted so that any MachineSet owning a Machine is represented with the correct owner reference between the two APIs.
		},
		Spec: capiv1.MachineSpec{
			InfrastructureRef: corev1.ObjectReference{
				APIVersion: apiVersion,
				Kind:       kind,
				Name:       mapiMachine.Name,
				Namespace:  capiNamespace,
			},
			ProviderID: mapiMachine.Spec.ProviderID,

			// Version: TODO(OCPCLOUD-2714): To be prevented by VAP.
			// ClusterName: populated by higher level functions.

			// TODO(OCPCLOUD-2715): These are not present on the MAPI API, we should implement them for feature parity.
			// NodeDrainTimeout: ,
			// NodeVolumeDetachTimeout: ,
			// NodeDeletionTimeout: ,
		},
	}

	// lifecycleHooks are handled via an annotation in Cluster API.
	lifecycleAnnotations := getCAPILifecycleHookAnnotations(mapiMachine.Spec.LifecycleHooks)
	if capiMachine.Annotations == nil {
		capiMachine.Annotations = lifecycleAnnotations
	} else {
		for key, value := range lifecycleAnnotations {
			capiMachine.Annotations[key] = value
		}
	}

	if capiMachine.Labels == nil {
		capiMachine.Labels = map[string]string{}
	}

	errs = append(errs, setMAPINodeLabelsToCAPIManagedNodeLabels(field.NewPath("spec", "metadata", "labels"), mapiMachine.Spec.ObjectMeta.Labels, capiMachine.Labels)...)

	// Unused fields - Below this line are fields not used from the MAPI Machine.

	if len(mapiMachine.OwnerReferences) > 0 {
		// TODO(OCPCLOUD-2716): We should support converting CAPI MachineSet ORs to MAPI MachineSet ORs. NB working out the UID will be hard.
		errs = append(errs, field.Invalid(field.NewPath("metadata", "ownerReferences"), mapiMachine.OwnerReferences, "ownerReferences are not supported"))
	}

	// mapiMachine.Spec.AuthoritativeAPI - Ignore as this is part of the conversion mechanism.

	// metadata.labels - needs special handling
	// metadata.annotations - needs special handling

	errs = append(errs, handleUnsupportedMachineFields(mapiMachine.Spec)...)

	return capiMachine, errs
}

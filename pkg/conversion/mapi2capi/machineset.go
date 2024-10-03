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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// fromMAPIMachineSetToCAPIMachineSet takes a MAPI MachineSet and returns a converted CAPI MachineSet.
func fromMAPIMachineSetToCAPIMachineSet(mapiMachineSet *mapiv1.MachineSet) (*capiv1.MachineSet, utilerrors.Aggregate) {
	var errs field.ErrorList

	capiMachineSet := &capiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        mapiMachineSet.Name,
			Namespace:   mapiMachineSet.Namespace,
			Labels:      mapiMachineSet.Labels,
			Annotations: mapiMachineSet.Annotations,
			// OwnerReferences - There shouldn't be any ownerreferences on a MachineSet.
		},
		Spec: capiv1.MachineSetSpec{
			Selector: mapiMachineSet.Spec.Selector,
			Replicas: mapiMachineSet.Spec.Replicas,
			// ClusterName // populated by higher level functions
			MinReadySeconds: mapiMachineSet.Spec.MinReadySeconds,
			DeletePolicy:    mapiMachineSet.Spec.DeletePolicy,
			Template: capiv1.MachineTemplateSpec{
				ObjectMeta: capiv1.ObjectMeta{
					Labels:      mapiMachineSet.Spec.Template.Labels,
					Annotations: mapiMachineSet.Spec.Template.Annotations,
				},
				// Spec // Populated by higher level functions.
			},
		},
	}

	if len(mapiMachineSet.OwnerReferences) > 0 {
		// TODO(OCPCLOUD-2748): Users may already have OwnerReferences on their MachineSets, where they do have them, we should work out how to translate them.
		errs = append(errs, field.Invalid(field.NewPath("metadata", "ownerReferences"), mapiMachineSet.OwnerReferences, "ownerReferences are not supported"))
	}

	// Unused fields - Below this line are fields not used from the MAPI MachineSet.

	errs = append(errs, handleUnsupportedMAPIObjectMetaFields(field.NewPath("spec", "template", "metadata"), mapiMachineSet.Spec.Template.ObjectMeta)...)

	// AuthoritativeAPI - Ignore, this is part of the conversion mechanism.

	return capiMachineSet, errs.ToAggregate()
}

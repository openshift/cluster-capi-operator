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
	"cmp"

	mapiv1 "github.com/openshift/api/machine/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// fromMAPIMachineSetToCAPIMachineSet takes a MAPI MachineSet and returns a converted CAPI MachineSet.
func fromMAPIMachineSetToCAPIMachineSet(mapiMachineSet *mapiv1.MachineSet) (*clusterv1.MachineSet, utilerrors.Aggregate) {
	var errs field.ErrorList

	capiMachineSet := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            mapiMachineSet.Name,
			Namespace:       mapiMachineSet.Namespace,
			Labels:          convertMAPILabelsToCAPI(mapiMachineSet.Labels),
			Annotations:     convertMAPIAnnotationsToCAPI(mapiMachineSet.Annotations),
			Finalizers:      nil, // The CAPI MachineSet finalizer is managed by the CAPI machineset controller.
			OwnerReferences: nil, // OwnerReferences not populated here. They are added later by the machineSetSync controller.
		},
		Spec: clusterv1.MachineSetSpec{
			Selector: convertMAPIMachineSetSelectorToCAPI(mapiMachineSet.Spec.Selector),
			Replicas: mapiMachineSet.Spec.Replicas,
			// ClusterName: // ClusterName not populated here. It is added later by higher level functions
			MinReadySeconds: mapiMachineSet.Spec.MinReadySeconds,
			DeletePolicy:    cmp.Or(mapiMachineSet.Spec.DeletePolicy, string(clusterv1.RandomMachineSetDeletePolicy)), // CAPI defaults to Random if empty.
			Template: clusterv1.MachineTemplateSpec{
				ObjectMeta: clusterv1.ObjectMeta{
					Labels:      convertMAPILabelsToCAPI(mapiMachineSet.Spec.Template.Labels),
					Annotations: convertMAPIAnnotationsToCAPI(mapiMachineSet.Spec.Template.Annotations),
				},
				// Spec: // Spec not populated here. It is added later by higher level functions.
			},
			// AuthoritativeAPI: // Ignore, this is part of the conversion mechanism.
		},
	}

	errs = append(errs, handleUnsupportedMAPIObjectMetaFields(field.NewPath("spec", "template", "metadata"), mapiMachineSet.Spec.Template.ObjectMeta)...)

	return capiMachineSet, errs.ToAggregate()
}

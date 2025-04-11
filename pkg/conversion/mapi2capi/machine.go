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
	"time"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	capiNamespace            = "openshift-cluster-api"
	workerUserDataSecretName = "worker-user-data"
)

// fromMAPIMachineToCAPIMachine translates a MAPI Machine to its Core CAPI Machine correspondent.
func fromMAPIMachineToCAPIMachine(mapiMachine *mapiv1beta1.Machine, apiVersion, kind string) (*capiv1.Machine, field.ErrorList) {
	var errs field.ErrorList

	capiMachine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:            mapiMachine.Name,
			Namespace:       capiNamespace,
			Labels:          convertMAPILabelsToCAPI(mapiMachine.Labels),
			Annotations:     convertMAPIAnnotationsToCAPI(mapiMachine.Annotations),
			Finalizers:      []string{capiv1.MachineFinalizer},
			OwnerReferences: nil, // OwnerReferences not populated here. They are added later by the machineSync controller.
		},
		Spec: capiv1.MachineSpec{
			InfrastructureRef: corev1.ObjectReference{
				APIVersion: apiVersion,
				Kind:       kind,
				Name:       mapiMachine.Name,
				Namespace:  capiNamespace,
			},
			ProviderID: mapiMachine.Spec.ProviderID,
			// ClusterName: // ClusterName not populated here. It is added by higher level functions.
			// AuthoritativeAPI: // AuthoritativeAPI not populated here. Ignore as this is part of the conversion mechanism.

			// Version:        // TODO(OCPCLOUD-2714): To be prevented by VAP.
			// ReadinessGates: // TODO(OCPCLOUD-2714): To be prevented by VAP.
			// NodeDrainTimeout:        // TODO(OCPCLOUD-2715): not present on the MAPI API, we should implement them for feature parity.
			// NodeVolumeDetachTimeout: // TODO(OCPCLOUD-2715): not present on the MAPI API, we should implement them for feature parity.
			// NodeDeletionTimeout:     // TODO(OCPCLOUD-2715): not present on the MAPI API, we should implement them for feature parity.
			NodeDeletionTimeout: &metav1.Duration{Duration: time.Second * 10}, // Hardcode it to the CAPI default value until this is implemented in MAPI.
		},
	}

	// Node labels in MAPI are stored under .spec.metadata.labels and then propagated down to the node,
	// whereas in CAPI they are stored in the top level .labels and later propagated down to the node.
	setMAPINodeLabelsToCAPINodeLabels(mapiMachine.Spec.ObjectMeta.Labels, capiMachine)

	// Node annotations in MAPI are stored under .spec.metadata.annotations and then propagated down to the node,
	// whereas in CAPI they are stored in the top level .annotations and later propagated down to the node.
	setMAPINodeAnnotationsToCAPINodeAnnotations(mapiMachine.Spec.ObjectMeta.Annotations, capiMachine)

	// LifecycleHooks in MAPI are a special field (.spec.lifecycleHooks),
	// whereas in CAPI they are defined via special annotations.
	setCAPILifecycleHookAnnotations(mapiMachine.Spec.LifecycleHooks, capiMachine)

	errs = append(errs, handleUnsupportedMachineFields(mapiMachine.Spec)...)

	return capiMachine, errs
}

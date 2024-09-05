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
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	mapiNamespace            = "openshift-machine-api"
	workerUserDataSecretName = "worker-user-data"
)

// fromMAPIMachineToCAPIMachine translates a MAPI Machine to its Core CAPI Machine correspondent.
func fromMAPIMachineToCAPIMachine(capiMachine *capiv1.Machine) *mapiv1.Machine {
	mapiMachine := &mapiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:        capiMachine.Name,
			Namespace:   mapiNamespace,
			Labels:      capiMachine.Labels,
			Annotations: capiMachine.Annotations,
		},
		Spec: mapiv1.MachineSpec{
			ObjectMeta: mapiv1.ObjectMeta{
				Name:            capiMachine.ObjectMeta.Name,
				GenerateName:    capiMachine.ObjectMeta.GenerateName,
				Namespace:       capiMachine.ObjectMeta.Namespace,
				Annotations:     capiMachine.ObjectMeta.Annotations,
				OwnerReferences: capiMachine.OwnerReferences,
			},
			ProviderID: capiMachine.Spec.ProviderID,
			// LifecycleHooks: // TODO: lossy: find alternative in CAPI for this.
			// Taints: // TODO: lossy: Not Present on CAPI Machines, only done via BootstrapProvider?

			// ProviderSpec: this MUST NOT be populated here. It will get populated later by higher level fuctions.
		},
	}

	setCAPIManagedNodeLabelsToMAPINodeLabels(capiMachine.Labels, mapiMachine.Spec.ObjectMeta.Labels)

	return mapiMachine
}

func setCAPIManagedNodeLabelsToMAPINodeLabels(capiNodeLabels map[string]string, mapiNodeLabels map[string]string) {
	// FYI: Not all the labels on the CAPI Machine are propagated down to the corresponding CAPI Node, only the "CAPI Managed ones" are.
	// See: https://github.com/kubernetes-sigs/cluster-api/pull/7173
	// and: https://github.com/fabriziopandini/cluster-api/blob/main/docs/proposals/20220927-label-sync-between-machine-and-nodes.md
	// In this case we are converting CAPI -> MAPI, so there are not going to be issues.
	if mapiNodeLabels == nil {
		mapiNodeLabels = map[string]string{}
	}

	for k, v := range capiNodeLabels {
		mapiNodeLabels[k] = v
	}
}

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
	"errors"
	"fmt"

	mapiv1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	capiNamespace            = "openshift-cluster-api"
	workerUserDataSecretName = "worker-user-data"
	awsTemplateAPIVersion    = "infrastructure.cluster.x-k8s.io/v1beta2"
	awsTemplateKind          = "AWSMachineTemplate"
)

var (
	errFieldUnsupportedByCAPI = errors.New("error field unsupported by Cluster API")
)

// fromMAPIMachineToCAPIMachine translates a MAPI Machine to its Core CAPI Machine correspondent.
func fromMAPIMachineToCAPIMachine(mapiMachine *mapiv1.Machine) (*capiv1.Machine, error) {
	var errs []error

	// Taints are not supported by CAPI.
	// TODO: add support for them via CAPI BootstrapConfig + minimal bootstrap controller?
	if len(mapiMachine.Spec.Taints) > 0 {
		errs = append(errs, fmt.Errorf("%w: %q", errFieldUnsupportedByCAPI, ".spec.taints"))
	}

	capiMachine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:        mapiMachine.Name,
			Namespace:   capiNamespace,
			Labels:      mapiMachine.Labels,
			Annotations: mapiMachine.Annotations,
		},
		Spec: capiv1.MachineSpec{
			InfrastructureRef: corev1.ObjectReference{
				APIVersion: awsTemplateAPIVersion,
				Kind:       awsTemplateKind,
				Name:       mapiMachine.Name,
			},
			ProviderID: mapiMachine.Spec.ProviderID,

			// Version: not necessary (optionally used by bootstrap providers).
			// FailureDomain: populated by higher level functions.
			// ClusterName: populated by higher level functions.

			// TODO: These are not present on the MAPI API, figure out if we need to
			// deal with this discrepancy:
			// NodeDrainTimeout: ,
			// NodeVolumeDetachTimeout: ,
			// NodeDeletionTimeout: ,
		},
	}

	setMAPINodeLabelsToCAPIManagedNodeLabels(mapiMachine.Spec.ObjectMeta.Labels, capiMachine.Labels)

	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}

	return capiMachine, nil
}

func setMAPINodeLabelsToCAPIManagedNodeLabels(mapiNodeLabels map[string]string, capiNodeLabels map[string]string) {
	// TODO: FYI: Not all the labels on the CAPI Machine are propagated down to the corresponding CAPI Node, only the "CAPI Managed ones" are.
	// See: https://github.com/kubernetes-sigs/cluster-api/pull/7173
	// and: https://github.com/fabriziopandini/cluster-api/blob/main/docs/proposals/20220927-label-sync-between-machine-and-nodes.md
	// Here we copy all the labels anyway, but these won't be propagated downwards to the Node.
	// We will track this feature GAP to cover.
	for k, v := range mapiNodeLabels {
		if capiNodeLabels == nil {
			capiNodeLabels = map[string]string{}
		}

		capiNodeLabels[k] = v
	}
}

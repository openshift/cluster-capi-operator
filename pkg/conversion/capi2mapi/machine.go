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
	"encoding/json"
	"fmt"
	"strings"

	mapiv1 "github.com/openshift/api/machine/v1beta1"
	conversionutil "github.com/openshift/cluster-capi-operator/pkg/conversion/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	mapiNamespace = "openshift-machine-api"
)

// fromCAPIMachineToMAPIMachine translates a core CAPI Machine to its MAPI Machine correspondent.
//
//nolint:funlen
func fromCAPIMachineToMAPIMachine(capiMachine *capiv1.Machine) (*mapiv1.Machine, field.ErrorList) {
	errs := field.ErrorList{}

	mapiMachine := &mapiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:        capiMachine.Name,
			Namespace:   mapiNamespace,
			Labels:      capiMachine.Labels,
			Annotations: capiMachine.Annotations,
			// OwnerReferences: TODO(OCPCLOUD-2716): These need to be converted so that any MachineSet owning a Machine is represented with the correct owner reference between the two APIs.
		},
		Spec: mapiv1.MachineSpec{
			ObjectMeta: mapiv1.ObjectMeta{
				// TODO(OCPCLOUD-2680): Fix CAPI metadata support to mirror MAPI.
				// Labels: We only expect labels and annotations to be present, but, they have nowhere to go on a CAPI Machine at present.
				// Annotations: We only expect labels and annotations to be present, but, they have nowhere to go on a CAPI Machine at present.
			},
			ProviderID:     capiMachine.Spec.ProviderID,
			LifecycleHooks: getMAPILifecycleHooks(capiMachine),
			// Taints: // TODO: lossy: Not Present on CAPI Machines, only done via BootstrapProvider?

			// ProviderSpec: this MUST NOT be populated here. It will get populated later by higher level fuctions.
		},
	}

	if len(capiMachine.OwnerReferences) > 0 {
		// TODO(OCPCLOUD-2716): We should support converting CAPI MachineSet ORs to MAPI MachineSet ORs. NB working out the UID will be hard.
		errs = append(errs, field.Invalid(field.NewPath("metadata", "ownerReferences"), capiMachine.OwnerReferences, "ownerReferences are not supported"))
	}

	// Make sure the machine has a label map.
	mapiMachine.Spec.ObjectMeta.Labels = map[string]string{}
	setCAPIManagedNodeLabelsToMAPINodeLabels(capiMachine.Labels, mapiMachine.Spec.ObjectMeta.Labels)

	// Unusued fields - Below this line are fields not used from the CAPI Machine.

	// capiMachine.Spec.ClusterName - Ignore this as it can be reconstructed from the infra object.
	// capiMachine.Spec.Bootstrap.ConfigRef - Ignore as we use DataSecretName for the MAPI side.
	// capiMachine.Spec.InfrastructureRef - Ignore as this is the split between 1 to 2 resources from MAPI to CAPI.
	// capiMachine.Spec.FailureDomain - Ignore because we use this to populate the providerSpec.

	if capiMachine.Spec.Version != nil {
		// TODO(OCPCLOUD-2714): We should prevent this using a VAP until and unless we need to support the field.
		errs = append(errs, field.Invalid(field.NewPath("spec", "version"), capiMachine.Spec.Version, "version is not supported"))
	}

	if capiMachine.Spec.NodeDrainTimeout != nil {
		// TODO(OCPCLOUD-2715): We should implement this within MAPI to create feature parity.
		errs = append(errs, field.Invalid(field.NewPath("spec", "nodeDrainTimeout"), capiMachine.Spec.NodeDrainTimeout, "nodeDrainTimeout is not supported"))
	}

	if capiMachine.Spec.NodeVolumeDetachTimeout != nil {
		// TODO(OCPCLOUD-2715): We should implement this within MAPI to create feature parity.
		errs = append(errs, field.Invalid(field.NewPath("spec", "nodeVolumeDetachTimeout"), capiMachine.Spec.NodeVolumeDetachTimeout, "nodeVolumeDetachTimeout is not supported"))
	}

	if capiMachine.Spec.NodeDeletionTimeout != nil {
		// TODO(OCPCLOUD-2715): We should implement this within MAPI to create feature parity.
		errs = append(errs, field.Invalid(field.NewPath("spec", "nodeDeletionTimeout"), capiMachine.Spec.NodeDeletionTimeout, "nodeDeletionTimeout is not supported"))
	}

	if len(errs) > 0 {
		// Return the mapiMachine so that the logic continues and collects all possible conversion errors.
		return mapiMachine, errs
	}

	return mapiMachine, nil
}

func setCAPIManagedNodeLabelsToMAPINodeLabels(capiNodeLabels map[string]string, mapiNodeLabels map[string]string) {
	// TODO(OCPCLOUD-2680): Not all the labels on the CAPI Machine are propagated down to the corresponding CAPI Node, only the "CAPI Managed ones" are.
	// These are those prefix by "node-role.kubernetes.io" or in the domains of "node-restriction.kubernetes.io" and "node.cluster.x-k8s.io".
	// See: https://github.com/kubernetes-sigs/cluster-api/pull/7173
	// and: https://github.com/fabriziopandini/cluster-api/blob/main/docs/proposals/20220927-label-sync-between-machine-and-nodes.md
	// We should only copy these into the labels to be propagated to the Node.
	if mapiNodeLabels == nil {
		mapiNodeLabels = map[string]string{}
	}

	for k, v := range capiNodeLabels {
		if conversionutil.IsCAPIManagedLabel(k) {
			mapiNodeLabels[k] = v

			delete(capiNodeLabels, k)
		}
	}
}

const (
	// Note the trailing slash here is important when we are trimming the prefix.
	capiPreDrainAnnotationPrefix     = capiv1.PreDrainDeleteHookAnnotationPrefix + "/"
	capiPreTerminateAnnotationPrefix = capiv1.PreTerminateDeleteHookAnnotationPrefix + "/"
)

// getMAPILifecycleHooks extracts the lifecycle hooks from the CAPI Machine annotations.
func getMAPILifecycleHooks(capiMachine *capiv1.Machine) mapiv1.LifecycleHooks {
	hooks := mapiv1.LifecycleHooks{}

	for k, v := range capiMachine.Annotations {
		switch {
		case strings.HasPrefix(k, capiPreDrainAnnotationPrefix):
			hooks.PreDrain = append(hooks.PreDrain, mapiv1.LifecycleHook{
				Name:  strings.TrimPrefix(k, capiPreDrainAnnotationPrefix),
				Owner: v,
			})

			delete(capiMachine.Annotations, k)
		case strings.HasPrefix(k, capiPreTerminateAnnotationPrefix):
			hooks.PreTerminate = append(hooks.PreTerminate, mapiv1.LifecycleHook{
				Name:  strings.TrimPrefix(k, capiPreTerminateAnnotationPrefix),
				Owner: v,
			})

			delete(capiMachine.Annotations, k)
		}
	}

	return hooks
}

// RawExtensionFromProviderSpec marshals the machine provider spec.
func RawExtensionFromProviderSpec(spec interface{}) (*runtime.RawExtension, error) {
	if spec == nil {
		return &runtime.RawExtension{}, nil
	}

	rawBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("error marshalling providerSpec: %w", err)
	}

	return &runtime.RawExtension{
		Raw: rawBytes,
	}, nil
}

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
	"time"

	mapiv1 "github.com/openshift/api/machine/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	mapiNamespace = "openshift-machine-api"
)

// fromCAPIMachineToMAPIMachine translates a core CAPI Machine to its MAPI Machine correspondent.
//
//nolint:funlen
func fromCAPIMachineToMAPIMachine(capiMachine *clusterv1.Machine) (*mapiv1.Machine, field.ErrorList) {
	errs := field.ErrorList{}

	lifecycleHooks, capiMachineNonHookAnnotations := convertCAPILifecycleHookAnnotationsToMAPILifecycleHooksAndAnnotations(capiMachine.Annotations)

	mapiMachine := &mapiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:            capiMachine.Name,
			Namespace:       mapiNamespace,
			Labels:          convertCAPILabelsToMAPILabels(capiMachine.Labels),
			Annotations:     convertCAPIAnnotationsToMAPIAnnotations(capiMachineNonHookAnnotations),
			Finalizers:      []string{mapiv1.MachineFinalizer},
			OwnerReferences: nil, // OwnerReferences not populated here. They are added later by the machineSync controller.
		},
		Spec: mapiv1.MachineSpec{
			ObjectMeta: mapiv1.ObjectMeta{
				Labels:      convertCAPIMachineLabelsToMAPIMachineSpecObjectMetaLabels(capiMachine.Labels),
				Annotations: convertCAPIMachineAnnotationsToMAPIMachineSpecObjectMetaAnnotations(capiMachineNonHookAnnotations),
			},
			ProviderID:     capiMachine.Spec.ProviderID,
			LifecycleHooks: lifecycleHooks,
			// ProviderSpec: // ProviderSpec MUST NOT be populated here. It is added later by higher level fuctions.
			// Taints: // TODO(OCPCLOUD-2861): Taint propagation from Machines to Nodes is not yet implemented in CAPI.
		},
	}

	// Unused fields - Below this line are fields not used from the CAPI Machine.
	// capiMachine.ObjectMeta.OwnerReferences - handled by the machineSync controller.

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
		// TODO(docs): document this.
		// We tolerate if the NodeDeletionTimeout is set to the CAPI default of 10s,
		// as CAPI automatically sets this on the machine when we convert MAPI->CAPI.
		// Otherwise if it is set to a non-default value we fail.
		if *capiMachine.Spec.NodeDeletionTimeout != (metav1.Duration{Duration: time.Second * 10}) {
			// TODO(OCPCLOUD-2715): We should implement this within MAPI to create feature parity.
			errs = append(errs, field.Invalid(field.NewPath("spec", "nodeDeletionTimeout"), capiMachine.Spec.NodeDeletionTimeout, "nodeDeletionTimeout is not supported"))
		}
	}

	if len(errs) > 0 {
		// Return the mapiMachine so that the logic continues and collects all possible conversion errors.
		return mapiMachine, errs
	}

	return mapiMachine, nil
}

const (
	// Note the trailing slash here is important when we are trimming the prefix.
	capiPreDrainAnnotationPrefix     = clusterv1.PreDrainDeleteHookAnnotationPrefix + "/"
	capiPreTerminateAnnotationPrefix = clusterv1.PreTerminateDeleteHookAnnotationPrefix + "/"
)

// convertCAPILifecycleHookAnnotationsToMAPILifecycleHooksAndAnnotations extracts the lifecycle hooks from the CAPI Machine annotations.
func convertCAPILifecycleHookAnnotationsToMAPILifecycleHooksAndAnnotations(capiAnnotations map[string]string) (mapiv1.LifecycleHooks, map[string]string) {
	hooks := mapiv1.LifecycleHooks{}
	newAnnotations := make(map[string]string)

	for k, v := range capiAnnotations {
		switch {
		case strings.HasPrefix(k, capiPreDrainAnnotationPrefix):
			hooks.PreDrain = append(hooks.PreDrain, mapiv1.LifecycleHook{
				Name:  strings.TrimPrefix(k, capiPreDrainAnnotationPrefix),
				Owner: v,
			})
		case strings.HasPrefix(k, capiPreTerminateAnnotationPrefix):
			hooks.PreTerminate = append(hooks.PreTerminate, mapiv1.LifecycleHook{
				Name:  strings.TrimPrefix(k, capiPreTerminateAnnotationPrefix),
				Owner: v,
			})
		default:
			// Carry over only the non lifecycleHooks annotations.
			newAnnotations[k] = v
		}
	}

	return hooks, newAnnotations
}

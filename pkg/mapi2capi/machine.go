package mapi2capi

import (
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	corev1 "k8s.io/api/core/v1"
)

const (
	capiNamespace            = "openshift-cluster-api"
	capiMachineAPIVersion    = "cluster.x-k8s.io/v1beta1"
	capiMachineKind          = "Machine"
	workerUserDataSecretName = "worker-user-data"
	awsTemplateAPIVersion    = "infrastructure.cluster.x-k8s.io/v1beta2"
	awsTemplateKind          = "AWSMachineTemplate"
)

// fromMachineToMachine translates a MAPI Machine to its Core CAPI Machine correspondent.
func fromMachineToMachine(mapiMachine *mapiv1.Machine) (capiv1.Machine, []string, error) {
	capiMachine := capiv1.Machine{}
	capiMachine.ObjectMeta = metav1.ObjectMeta{
		Name:        mapiMachine.Name,
		Namespace:   capiNamespace,
		Labels:      mapiMachine.Labels,
		Annotations: mapiMachine.Annotations,
	}
	capiMachine.TypeMeta = metav1.TypeMeta{
		Kind:       capiMachineKind,
		APIVersion: capiMachineAPIVersion,
	}

	capiMachine.Spec = capiv1.MachineSpec{
		InfrastructureRef: corev1.ObjectReference{
			APIVersion: awsTemplateAPIVersion,
			Kind:       awsTemplateKind,
			Name:       mapiMachine.Name,
		},
		ProviderID: mapiMachine.Spec.ProviderID,

		// Version defines the desired Kubernetes version.
		// This field is meant to be optionally used by bootstrap providers.
		// Version: , not necessary for MAPI.

		// FailureDomain: populated by higher level functions.
		// ClusterName: populated by higher level functions.

		// TODO:
		// NodeDrainTimeout: ,
		// NodeVolumeDetachTimeout: ,
		// NodeDeletionTimeout: ,
	}

	setMAPINodeLabelsToCAPIManagedNodeLabels(mapiMachine.Spec.ObjectMeta.Labels, capiMachine.Labels)

	return capiMachine, nil, nil
}

func setMAPINodeLabelsToCAPIManagedNodeLabels(mapiNodeLabels map[string]string, capiNodeLabels map[string]string) {
	// TODO: lossy. not all Node Labels are propagated (only the "CAPI Managed ones"), figure out how to do this in CAPI.

	// Add MAPI Machine's Spec.ObjectMeta.Labels, meant to be propagated to kubernetes nodes,
	// as CAPI Machine's ObjectMeta.Labels, as CAPI stores them together with the other Machine labels,
	// and by default propagates the ones that are "CAPI Managed" (only those, for design reasons) to the corresponding Kubernetes Node.
	// See: https://github.com/kubernetes-sigs/cluster-api/pull/7173
	// and: https://github.com/fabriziopandini/cluster-api/blob/main/docs/proposals/20220927-label-sync-between-machine-and-nodes.md
	for k, v := range mapiNodeLabels {
		if capiNodeLabels == nil {
			capiNodeLabels = map[string]string{}
		}
		capiNodeLabels[k] = v
	}
}

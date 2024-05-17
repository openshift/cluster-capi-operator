package mapi2capi

import (
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/utils/ptr"
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
func fromMachineToMachine(m *mapiv1.Machine) (capiv1.Machine, []string, error) {
	capiMachine := capiv1.Machine{}
	capiMachine.ObjectMeta = metav1.ObjectMeta{
		Name:        m.Name,
		Namespace:   capiNamespace,
		Labels:      m.Labels,
		Annotations: m.Annotations,
	}
	capiMachine.TypeMeta = metav1.TypeMeta{
		Kind:       capiMachineKind,
		APIVersion: capiMachineAPIVersion,
	}

	capiMachine.Spec = capiv1.MachineSpec{
		Bootstrap: capiv1.Bootstrap{
			DataSecretName: ptr.To(workerUserDataSecretName),
		},
		InfrastructureRef: corev1.ObjectReference{
			APIVersion: awsTemplateAPIVersion,
			Kind:       awsTemplateKind,
			Name:       m.Name,
		},
		ProviderID: m.Spec.ProviderID,

		// Version defines the desired Kubernetes version.
		// This field is meant to be optionally used by bootstrap providers.
		// Version: , not necessary for MAPI.

		// FailureDomain: populated by higher level functions.
		// ClusterName: populated by higher level functions, TODO: ensure this is done.

		// TODO:
		// NodeDrainTimeout: ,
		// NodeVolumeDetachTimeout: ,
		// NodeDeletionTimeout: ,
	}

	return capiMachine, nil, nil
}

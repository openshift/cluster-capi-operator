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
	capiMachineAPIVersion    = "cluster.x-k8s.io"
	capiMachineKind          = "Machine"
	workerUserDataSecretName = "worker-user-data"
	awsTemplateAPIVersion    = "infrastructure.cluster.x-k8s.io/v1beta1"
	awsTemplateKind          = "AWSMachineTemplate"
)

// FromMachineToMachine translates a MAPI Machine to its Core CAPI Machine correspondent.
func FromMachineToMachine(m *mapiv1.Machine) (*capiv1.Machine, []string, error) {
	capiMachine := &capiv1.Machine{}
	capiMachine.ObjectMeta = metav1.ObjectMeta{
		Name:      m.Name,
		Namespace: capiNamespace,
	}
	capiMachine.TypeMeta = metav1.TypeMeta{
		Kind:       capiMachineKind,
		APIVersion: capiMachineAPIVersion,
	}

	capiMachine.Spec = capiv1.MachineSpec{
		ClusterName: "TODO-TODO", //TODO
		Bootstrap: capiv1.Bootstrap{
			DataSecretName: ptr.To(workerUserDataSecretName),
		},
		InfrastructureRef: corev1.ObjectReference{
			APIVersion: awsTemplateAPIVersion,
			Kind:       awsTemplateKind,
			Name:       m.Name,
			//TODO: what to do with it? Is it ok to just populate it later?
			//ProviderID:
			//FailureDomain:
		},
	}

	return capiMachine, nil, nil
}

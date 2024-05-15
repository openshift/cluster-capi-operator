package capi2mapi

import (
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	mapiNamespace            = "openshift-machine-api"
	mapiMachineAPIVersion    = "machine.openshift.io"
	mapiMachineKind          = "Machine"
	workerUserDataSecretName = "worker-user-data"
)

// FromMachineToMachine translates a MAPI Machine to its Core CAPI Machine correspondent.
func FromMachineToMachine(m *capiv1.Machine) (*mapiv1.Machine, []string, error) {
	mapiMachine := &mapiv1.Machine{}
	mapiMachine.ObjectMeta = metav1.ObjectMeta{
		Name:      m.Name,
		Namespace: mapiNamespace,
	}
	mapiMachine.TypeMeta = metav1.TypeMeta{
		Kind:       mapiMachineKind,
		APIVersion: mapiMachineAPIVersion,
	}

	mapiMachine.Spec = mapiv1.MachineSpec{
		// TODO: ProviderID: ,
	}

	return mapiMachine, nil, nil
}

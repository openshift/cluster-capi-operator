package capi2mapi

import (
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	mapiNamespace            = "openshift-machine-api"
	mapiMachineAPIVersion    = "machine.openshift.io/v1beta1"
	mapiMachineKind          = "Machine"
	workerUserDataSecretName = "worker-user-data"
)

// fromMachineToMachine translates a MAPI Machine to its Core CAPI Machine correspondent.
func fromMachineToMachine(m *capiv1.Machine) (mapiv1.Machine, []string, error) {
	mapiMachine := mapiv1.Machine{}
	mapiMachine.ObjectMeta = metav1.ObjectMeta{
		Name:        m.Name,
		Namespace:   mapiNamespace,
		Labels:      m.Labels,
		Annotations: m.Annotations,
	}
	mapiMachine.TypeMeta = metav1.TypeMeta{
		Kind:       mapiMachineKind,
		APIVersion: mapiMachineAPIVersion,
	}

	mapiMachine.Spec = mapiv1.MachineSpec{
		ObjectMeta: mapiv1.ObjectMeta{
			Name:            m.ObjectMeta.Name,
			GenerateName:    m.ObjectMeta.GenerateName,
			Namespace:       m.ObjectMeta.Namespace,
			Labels:          m.ObjectMeta.Labels,
			Annotations:     m.ObjectMeta.Annotations,
			OwnerReferences: m.OwnerReferences,
		},
		ProviderID: m.Spec.ProviderID,

		// LifecycleHooks: TODO: find alternative in CAPI for this
		// Taints: , // TODO: Not Present on CAPI Machines, only on Bootstrap?
		// ProviderSpec: this will get populated by higher level fuctions.
	}

	return mapiMachine, nil, nil
}

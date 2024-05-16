package capi2mapi

import (
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	mapiMachineSetAPIVersion = "machine.openshift.io"
	mapiMachineSetKind       = "MachineSet"
)

func FromMachineSetToMachineSet(capiMachineSet *capiv1.MachineSet) (mapiv1.MachineSet, []string, error) {
	mapiMachineSet := mapiv1.MachineSet{}
	mapiMachineSet.ObjectMeta = metav1.ObjectMeta{
		Name:      mapiMachineSet.Name,
		Namespace: mapiMachineSet.Namespace,
	}
	mapiMachineSet.TypeMeta = metav1.TypeMeta{
		Kind:       mapiMachineSetKind,
		APIVersion: mapiMachineSetAPIVersion,
	}
	mapiMachineSet.Spec.Selector = capiMachineSet.Spec.Selector
	mapiMachineSet.Spec.Template.Labels = capiMachineSet.Spec.Template.Labels
	mapiMachineSet.Spec.Replicas = capiMachineSet.Spec.Replicas

	return mapiMachineSet, nil, nil
}

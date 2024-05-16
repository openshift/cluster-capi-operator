package mapi2capi

import (
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/utils/ptr"
)

const (
	capiMachineSetAPIVersion = "cluster.x-k8s.io"
	capiMachineSetKind       = "MachineSet"
)

func FromMachineSetToMachineSet(mapiMachineSet *mapiv1.MachineSet) (capiv1.MachineSet, []string, error) {
	capiMachineSet := capiv1.MachineSet{}
	capiMachineSet.ObjectMeta = metav1.ObjectMeta{
		Name:      mapiMachineSet.Name,
		Namespace: mapiMachineSet.Namespace,
	}
	capiMachineSet.TypeMeta = metav1.TypeMeta{
		Kind:       capiMachineSetKind,
		APIVersion: capiMachineSetAPIVersion,
	}
	capiMachineSet.Spec.Selector = mapiMachineSet.Spec.Selector
	capiMachineSet.Spec.Template.Labels = mapiMachineSet.Spec.Template.Labels
	capiMachineSet.Spec.ClusterName = "" // TODO: this should be fetched from infra object
	capiMachineSet.Spec.Replicas = mapiMachineSet.Spec.Replicas
	capiMachineSet.Spec.Template.Spec.Bootstrap = capiv1.Bootstrap{
		DataSecretName: ptr.To(workerUserDataSecretName),
	}
	capiMachineSet.Spec.Template.Spec.ClusterName = "TODO-TODO" // TODO: this should be fetched from infra object
	capiMachineSet.Spec.Template.Spec.InfrastructureRef = corev1.ObjectReference{
		APIVersion: awsTemplateAPIVersion,
		Kind:       awsTemplateKind,
		Name:       mapiMachineSet.Name,
	}

	return capiMachineSet, nil, nil
}

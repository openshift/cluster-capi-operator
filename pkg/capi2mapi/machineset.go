package capi2mapi

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	mapiv1 "github.com/openshift/api/machine/v1beta1"
)

const (
	mapiMachineSetAPIVersion = "machine.openshift.io/v1beta1"
	mapiMachineSetKind       = "MachineSet"
)

func FromMachineSetToMachineSet(capiMachineSet *capiv1.MachineSet) (mapiv1.MachineSet, []string, error) {
	mapiMachineSet := mapiv1.MachineSet{}
	mapiMachineSet.ObjectMeta = metav1.ObjectMeta{
		Name:        capiMachineSet.Name,
		Namespace:   capiMachineSet.Namespace,
		Labels:      capiMachineSet.Labels,
		Annotations: capiMachineSet.Annotations,
	}
	mapiMachineSet.TypeMeta = metav1.TypeMeta{
		Kind:       mapiMachineSetKind,
		APIVersion: mapiMachineSetAPIVersion,
	}
	mapiMachineSet.Spec.Selector = capiMachineSet.Spec.Selector
	mapiMachineSet.Spec.Template.Labels = capiMachineSet.Spec.Template.Labels
	mapiMachineSet.Spec.Replicas = capiMachineSet.Spec.Replicas

	for k, v := range capiMachineSet.Spec.Template.Labels {
		// Only CAPI managed labels are propagated down to the kubernetes nodes.
		// So only put those back to the MAPI Machine's Spec.ObjectMeta.Labels.
		// See: https://github.com/kubernetes-sigs/cluster-api/pull/7173
		// and: https://github.com/fabriziopandini/cluster-api/blob/main/docs/proposals/20220927-label-sync-between-machine-and-nodes.md
		if strings.HasPrefix(k, capiv1.NodeRoleLabelPrefix) || k == capiv1.ManagedNodeLabelDomain || k == capiv1.NodeRestrictionLabelDomain {
			if mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels == nil {
				mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels = map[string]string{}
			}
			mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels[k] = v
		}
	}
	setCAPIManagedNodeLabelsToMAPINodeLabels(capiMachineSet.Spec.Template.Labels, mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels)

	return mapiMachineSet, nil, nil
}

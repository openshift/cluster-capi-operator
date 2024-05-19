package capi2mapi

import (
	"strings"

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
func fromMachineToMachine(capiMachine *capiv1.Machine) (mapiv1.Machine, []string, error) {
	mapiMachine := mapiv1.Machine{}
	mapiMachine.ObjectMeta = metav1.ObjectMeta{
		Name:        capiMachine.Name,
		Namespace:   mapiNamespace,
		Labels:      capiMachine.Labels,
		Annotations: capiMachine.Annotations,
	}
	mapiMachine.TypeMeta = metav1.TypeMeta{
		Kind:       mapiMachineKind,
		APIVersion: mapiMachineAPIVersion,
	}

	mapiMachine.Spec = mapiv1.MachineSpec{
		ObjectMeta: mapiv1.ObjectMeta{
			Name:            capiMachine.ObjectMeta.Name,
			GenerateName:    capiMachine.ObjectMeta.GenerateName,
			Namespace:       capiMachine.ObjectMeta.Namespace,
			Labels:          capiMachine.ObjectMeta.Labels,
			Annotations:     capiMachine.ObjectMeta.Annotations,
			OwnerReferences: capiMachine.OwnerReferences,
		},
		ProviderID: capiMachine.Spec.ProviderID,

		// LifecycleHooks: // TODO: lossy: find alternative in CAPI for this
		// Taints: // TODO: lossy: Not Present on CAPI Machines, only done via BootstrapProvider?

		// ProviderSpec: this must not be populated here. It will get populated later by higher level fuctions.
	}

	setCAPIManagedNodeLabelsToMAPINodeLabels(capiMachine.Labels, mapiMachine.Spec.ObjectMeta.Labels)

	return mapiMachine, nil, nil
}

func setCAPIManagedNodeLabelsToMAPINodeLabels(capiNodeLabels map[string]string, mapiNodeLabels map[string]string) {
	// TODO: lossy. not all Node Labels are propagated (only the "CAPI Managed ones"), figure out how to do this in CAPI.
	for k, v := range capiNodeLabels {
		// Only CAPI managed labels are propagated down to the kubernetes nodes.
		// So only put those back to the MAPI Machine's Spec.ObjectMeta.Labels.
		// See: https://github.com/kubernetes-sigs/cluster-api/pull/7173
		// and: https://github.com/fabriziopandini/cluster-api/blob/main/docs/proposals/20220927-label-sync-between-machine-and-nodes.md
		if strings.HasPrefix(k, capiv1.NodeRoleLabelPrefix) || k == capiv1.ManagedNodeLabelDomain || k == capiv1.NodeRestrictionLabelDomain {
			if mapiNodeLabels == nil {
				mapiNodeLabels = map[string]string{}
			}
			mapiNodeLabels[k] = v
		}
	}
}

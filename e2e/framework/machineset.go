package framework

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

type machineSetParams struct {
	msName            string
	clusterName       string
	failureDomain     string
	replicas          int32
	infrastructureRef corev1.ObjectReference
	userDataSecret    string
}

const machineSetOpenshiftLabelKey = "machine.openshift.io/cluster-api-machineset"

// NewMachineSetParams returns a new machineSetParams object.
func NewMachineSetParams(msName, clusterName, failureDomain string, replicas int32, infrastructureRef corev1.ObjectReference, userDataSecretName string) machineSetParams {
	Expect(msName).ToNot(BeEmpty())
	Expect(clusterName).ToNot(BeEmpty())
	Expect(infrastructureRef.APIVersion).ToNot(BeEmpty())
	Expect(infrastructureRef.Kind).ToNot(BeEmpty())
	Expect(infrastructureRef.Name).ToNot(BeEmpty())

	return machineSetParams{
		msName:            msName,
		clusterName:       clusterName,
		replicas:          replicas,
		infrastructureRef: infrastructureRef,
		failureDomain:     failureDomain,
		userDataSecret:    userDataSecretName,
	}
}

// CreateMachineSet creates a new CAPI MachineSet resource.
func CreateMachineSet(ctx context.Context, cl client.Client, params machineSetParams) *clusterv1beta1.MachineSet {
	By(fmt.Sprintf("Creating MachineSet %q", params.msName))

	ms := &clusterv1beta1.MachineSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "MachineSet",
			APIVersion: "cluster.x-k8s.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.msName,
			Namespace: CAPINamespace,
		},
		Spec: clusterv1beta1.MachineSetSpec{
			Replicas:    &params.replicas,
			ClusterName: params.clusterName,
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"machine.openshift.io/cluster-api-cluster": params.clusterName,
					machineSetOpenshiftLabelKey:                params.msName,
				},
			},
			Template: clusterv1beta1.MachineTemplateSpec{
				ObjectMeta: clusterv1beta1.ObjectMeta{
					Labels: map[string]string{
						"machine.openshift.io/cluster-api-cluster": params.clusterName,
						machineSetOpenshiftLabelKey:                params.msName,
					},
				},
				Spec: clusterv1beta1.MachineSpec{
					Bootstrap: clusterv1beta1.Bootstrap{
						DataSecretName: &params.userDataSecret,
					},
					ClusterName:       params.clusterName,
					InfrastructureRef: params.infrastructureRef,
				},
			},
		},
	}

	if params.failureDomain != "" {
		ms.Spec.Template.Spec.FailureDomain = &params.failureDomain
	}

	Expect(cl.Create(ctx, ms)).To(Succeed(), "Should have successfully created the CAPI MachineSet")
	return ms
}

// WaitForMachineSetsDeleted polls until the given MachineSets are not found, and
// there are zero Machines found matching the MachineSet's label selector.
func WaitForMachineSetsDeleted(cl client.Client, machineSets ...*clusterv1beta1.MachineSet) {
	for _, ms := range machineSets {
		By(fmt.Sprintf("Waiting for MachineSet %q to be deleted", ms.GetName()))

		// Wait for all machines to be deleted
		Eventually(func() int {
			selector := ms.Spec.Selector
			machines := GetMachines(cl, &selector)
			return len(machines)
		}, WaitLong, RetryMedium).Should(BeZero(), "Should have deleted all MachineSet's machines")

		// Wait for MachineSet to be deleted
		Eventually(komega.Get(&clusterv1beta1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ms.GetName(),
				Namespace: ms.GetNamespace(),
			},
		}), WaitLong, RetryMedium).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "Should have deleted MachineSet %s/%s", ms.GetNamespace(), ms.GetName())
	}
}

func DeleteMachineSets(ctx context.Context, cl client.Client, machineSets ...*clusterv1beta1.MachineSet) {
	for _, ms := range machineSets {
		if ms == nil {
			continue
		}
		By(fmt.Sprintf("Deleting MachineSet %q", ms.GetName()))
		Eventually(func() error {
			return cl.Delete(ctx, ms)
		}, time.Minute, RetryShort).Should(SatisfyAny(
			Succeed(),
			WithTransform(apierrors.IsNotFound, BeTrue()),
		), "Should have successfully deleted machineSet %s/%s, or machineSet should not be found.",
			ms.Namespace, ms.Name)
	}
}

// WaitForMachineSet waits for the all Machines belonging to the named
// MachineSet to enter the "Running" phase, and for all nodes belonging to those
// Machines to be ready.
func WaitForMachineSet(cl client.Client, name string, namespace string) {
	By(fmt.Sprintf("Waiting for MachineSet machines %q to enter Running phase", name))

	machineSet := GetMachineSet(cl, name, namespace)

	Eventually(func() error {
		machines := GetMachinesFromMachineSet(cl, machineSet)

		replicas := pointer.Int32PtrDerefOr(machineSet.Spec.Replicas, 0)

		if len(machines) != int(replicas) {
			return fmt.Errorf("%q: found %d Machines, but MachineSet has %d replicas",
				name, len(machines), int(replicas))
		}

		running := FilterRunningMachines(machines)

		// This could probably be smarter, but seems fine for now.
		if len(running) != len(machines) {
			return fmt.Errorf("%q: not all Machines are running: %d of %d",
				name, len(running), len(machines))
		}

		for _, m := range running {
			node, err := GetNodeForMachine(ctx, cl, m)
			if err != nil {
				return err
			}

			if !isNodeReady(node) {
				return fmt.Errorf("%s: node is not ready", node.Name)
			}
		}

		return nil
	}, WaitOverLong, RetryMedium).Should(Succeed())
}

// GetMachineSet gets a machineset by its name.
func GetMachineSet(cl client.Client, name string, namespace string) *clusterv1beta1.MachineSet {
	machineSet := &clusterv1beta1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	Eventually(komega.Get(machineSet), time.Minute, RetryShort).Should(Succeed(), "Should have successfully retrieved machineset %s/%s.", machineSet.Namespace, machineSet.Name)

	return machineSet
}

// GetMachinesFromMachineSet returns an array of machines owned by a given machineSet
func GetMachinesFromMachineSet(cl client.Client, machineSet *clusterv1beta1.MachineSet) []*clusterv1beta1.Machine {
	machines := GetMachines(cl)

	var machinesForSet []*clusterv1beta1.Machine
	for key := range machines {
		if metav1.IsControlledBy(machines[key], machineSet) {
			machinesForSet = append(machinesForSet, machines[key])
		}
	}

	return machinesForSet
}

// GetNewestMachineFromMachineSet returns the new created machine by a given machineSet.
func GetNewestMachineFromMachineSet(cl client.Client, machineSet *clusterv1beta1.MachineSet) *clusterv1beta1.Machine {
	machines := GetMachinesFromMachineSet(cl, machineSet)
	Expect(machines).ToNot(BeEmpty(), "Should have found machines for MachineSet %s/%s", machineSet.Namespace, machineSet.Name)

	var machine *clusterv1beta1.Machine

	t := time.Date(0001, 01, 01, 00, 00, 00, 00, time.UTC)

	for key := range machines {
		createTime := machines[key].CreationTimestamp.Time
		if createTime.After(t) {
			t = createTime
			machine = machines[key]
		}
	}

	return machine
}

// ScaleCAPIMachineSet scales a machineSet with a given name to the given number of replicas.
func ScaleCAPIMachineSet(name string, replicas int32, namespace string) {
	machineSet := &clusterv1beta1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	Eventually(komega.Update(machineSet, func() {
		machineSet.Spec.Replicas = &replicas
	}), WaitShort, RetryShort).Should(Succeed(), "Should have successfully updated MachineSet %s replicas to %d", machineSet.Name, replicas)
}

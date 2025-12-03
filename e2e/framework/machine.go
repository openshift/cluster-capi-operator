package framework

import (
	"context"
	"time"

	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

// GetMachines gets a list of machines from the default cluster API namespace.
// Optionally, labels may be used to constrain listed machines.
func GetMachines(cl client.Client, selectors ...*metav1.LabelSelector) []*clusterv1beta1.Machine {
	machineList := &clusterv1beta1.MachineList{}

	listOpts := append([]client.ListOption{},
		client.InNamespace(CAPINamespace),
	)

	for _, selector := range selectors {
		s, err := metav1.LabelSelectorAsSelector(selector)
		Expect(err).ToNot(HaveOccurred(), "Should have valid label selector")

		listOpts = append(listOpts,
			client.MatchingLabelsSelector{Selector: s},
		)
	}

	Eventually(komega.List(machineList, listOpts...)).
		Should(Succeed(), "Should have successfully listed machineList in namespace %s", CAPINamespace)

	var machines []*clusterv1beta1.Machine

	for i := range machineList.Items {
		machines = append(machines, &machineList.Items[i])
	}

	return machines
}

// FilterRunningMachines returns a slice of only those Machines in the input
// that are in the "Running" phase.
func FilterRunningMachines(machines []*clusterv1beta1.Machine) []*clusterv1beta1.Machine {
	var result []*clusterv1beta1.Machine

	for _, m := range machines {
		if m.Status.Phase == string(clusterv1beta1.MachinePhaseRunning) {
			result = append(result, m)
		}
	}

	return result
}

// GetAWSMachine gets an AWSMachine by its name.
func GetAWSMachine(cl client.Client, name string, namespace string) *awsv1.AWSMachine {
	machine := &awsv1.AWSMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	Eventually(komega.Get(machine), time.Minute, RetryShort).Should(Succeed(), "Should have successfully retrieved awsmachine %s/%s.", machine.Namespace, machine.Name)

	return machine
}

// GetMachine gets a machine by its name.
func GetMachine(cl client.Client, name string, namespace string) *clusterv1beta1.Machine {
	machine := &clusterv1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	Eventually(komega.Get(machine), time.Minute, RetryShort).Should(Succeed(), "Should have successfully retrieved machine %s/%s.", machine.Namespace, machine.Name)

	return machine
}

// DeleteMachines deletes the specified machines.
func DeleteMachines(ctx context.Context, cl client.Client, namespace string, machines ...*clusterv1beta1.Machine) {
	// 1. delete all machines
	for _, machine := range machines {
		if machine == nil {
			continue
		}
		Eventually(func() error {
			return cl.Delete(ctx, machine)
		}, time.Minute, RetryShort).Should(SatisfyAny(
			Succeed(),
			WithTransform(apierrors.IsNotFound, BeTrue()),
		), "Should have successfully deleted machine %s/%s, or machine should not be found.",
			machine.Namespace, machine.Name)
	}

	// 2. waiting for all machines to be deleted
	machineNames := []string{}
	for _, machine := range machines {
		machineNames = append(machineNames, machine.Name)
	}

	machineList := &clusterv1beta1.MachineList{}
	Eventually(komega.ObjectList(machineList, client.InNamespace(namespace)), WaitLong, RetryMedium).Should(
		WithTransform(func(list *clusterv1beta1.MachineList) []clusterv1beta1.Machine {
			return list.Items
		}, Not(ContainElements(
			HaveField("ObjectMeta.Name", BeElementOf(machineNames)),
		))),
		"Should have successfully deleted machines %v in namespace %s", machineNames, namespace,
	)
}

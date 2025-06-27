package framework

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// GetMachines gets a list of machines from the default cluster API namespace.
// Optionaly, labels may be used to constrain listed machinesets.
func GetMachines(cl client.Client, selectors ...*metav1.LabelSelector) ([]*clusterv1.Machine, error) {
	machineList := &clusterv1.MachineList{}

	listOpts := append([]client.ListOption{},
		client.InNamespace(CAPINamespace),
	)

	for _, selector := range selectors {
		s, err := metav1.LabelSelectorAsSelector(selector)
		if err != nil {
			return nil, err
		}

		listOpts = append(listOpts,
			client.MatchingLabelsSelector{Selector: s},
		)
	}

	if err := cl.List(ctx, machineList, listOpts...); err != nil {
		return nil, fmt.Errorf("error querying api for machineList object: %w", err)
	}

	var machines []*clusterv1.Machine

	for i := range machineList.Items {
		machines = append(machines, &machineList.Items[i])
	}

	return machines, nil
}

// GetMachine get a machine by its name from the cluster API namespace.
func GetMachine(cl client.Client, name string) (*clusterv1.Machine, error) {
	machine := &clusterv1.Machine{}
	key := client.ObjectKey{Namespace: CAPINamespace, Name: name}

	if err := cl.Get(context.Background(), key, machine); err != nil {
		return nil, fmt.Errorf("error querying api for machine object: %w", err)
	}

	return machine, nil
}

// GetAWSMachine get a awsmachine by its name from the cluster API namespace.
func GetAWSMachine(cl client.Client, name string) (*awsv1.AWSMachine, error) {
	machine := &awsv1.AWSMachine{}
	key := client.ObjectKey{Namespace: CAPINamespace, Name: name}

	if err := cl.Get(context.Background(), key, machine); err != nil {
		return nil, fmt.Errorf("error querying api for awsmachine object: %w", err)
	}

	return machine, nil
}

// FilterRunningMachines returns a slice of only those Machines in the input
// that are in the "Running" phase.
func FilterRunningMachines(machines []*clusterv1.Machine) []*clusterv1.Machine {
	var result []*clusterv1.Machine

	for i, m := range machines {
		if m.Status.Phase == string(clusterv1.MachinePhaseRunning) {
			result = append(result, machines[i])
		}
	}

	return result
}

// DeleteMachines deletes the specified machines and returns an error on failure.
func DeleteMachines(cl client.Client, machines ...*clusterv1.Machine) error {
	return wait.PollUntilContextTimeout(ctx, RetryShort, time.Minute, true, func(ctx context.Context) (bool, error) {
		for _, machine := range machines {
			if err := cl.Delete(ctx, machine); err != nil {
				klog.Errorf("Error querying api for machine object %q: %v, retrying...", machine.Name, err)
				return false, err
			}
		}

		return true, nil
	})
}

// WaitForMachinesDeleted polls until the given Machines are not found.
func WaitForMachinesDeleted(cl client.Client, machines ...*clusterv1.Machine) {
	Eventually(func() bool {
		for _, m := range machines {
			if err := cl.Get(context.Background(), runtimeclient.ObjectKey{
				Name:      m.GetName(),
				Namespace: m.GetNamespace(),
			}, &clusterv1.Machine{}); !apierrors.IsNotFound(err) {
				return false // Not deleted, or other error.
			}
		}

		return true // Everything was deleted.
	}, WaitLong, RetryMedium).Should(BeTrue(), "error encountered while waiting for Machines to be deleted.")
}

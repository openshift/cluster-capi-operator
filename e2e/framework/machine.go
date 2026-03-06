// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package framework

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

// GetMachines gets a list of machines from the default cluster API namespace.
// Optionally, labels may be used to constrain listed machines.
func GetMachines(selectors ...*metav1.LabelSelector) []*clusterv1.Machine {
	GinkgoHelper()

	machineList := &clusterv1.MachineList{}

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

	var machines []*clusterv1.Machine

	for i := range machineList.Items {
		machines = append(machines, &machineList.Items[i])
	}

	return machines
}

// GetAWSMachineWithRetry gets an AWSMachine by its name, retrying until found or timeout.
func GetAWSMachineWithRetry(name string, namespace string) *awsv1.AWSMachine {
	GinkgoHelper()

	machine := &awsv1.AWSMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	Eventually(komega.Get(machine), time.Minute, RetryShort).Should(Succeed(), "Should have successfully retrieved awsmachine %s/%s.", machine.Namespace, machine.Name)

	return machine
}

// GetAWSMachine gets an AWSMachine by its name.
func GetAWSMachine(name string, namespace string) (*awsv1.AWSMachine, error) {
	machine := &awsv1.AWSMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	if err := komega.Get(machine)(); err != nil {
		return nil, err
	}

	return machine, nil
}

// GetMachineWithRetry gets a machine by its name, retrying until found or timeout.
func GetMachineWithRetry(name string, namespace string) *clusterv1.Machine {
	GinkgoHelper()

	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	Eventually(komega.Get(machine), time.Minute, RetryShort).Should(Succeed(), "Should have successfully retrieved machine %s/%s.", machine.Namespace, machine.Name)

	return machine
}

// GetMachine gets a machine by its name.
func GetMachine(name string, namespace string) (*clusterv1.Machine, error) {
	machine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	if err := komega.Get(machine)(); err != nil {
		return nil, err
	}

	return machine, nil
}

// DeleteMachines deletes the specified machines.
func DeleteMachines(ctx context.Context, cl client.Client, namespace string, machines ...*clusterv1.Machine) {
	GinkgoHelper()

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
		if machine == nil {
			continue
		}

		machineNames = append(machineNames, machine.Name)
	}

	machineList := &clusterv1.MachineList{}
	Eventually(komega.ObjectList(machineList, client.InNamespace(namespace)), WaitLong, RetryMedium).Should(
		WithTransform(func(list *clusterv1.MachineList) []clusterv1.Machine {
			return list.Items
		}, Not(ContainElements(
			HaveField("ObjectMeta.Name", BeElementOf(machineNames)),
		))),
		"Should have successfully deleted machines %v in namespace %s", machineNames, namespace,
	)
}

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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

type machineSetParams struct {
	msName            string
	clusterName       string
	failureDomain     string
	replicas          int32
	infrastructureRef clusterv1.ContractVersionedObjectReference
	userDataSecret    string
}

const machineSetOpenshiftLabelKey = "machine.openshift.io/cluster-api-machineset"

// NewMachineSetParams returns a new machineSetParams object.
func NewMachineSetParams(msName, clusterName, failureDomain string, replicas int32, infrastructureRef clusterv1.ContractVersionedObjectReference, userDataSecretName string) machineSetParams {
	GinkgoHelper()

	Expect(msName).ToNot(BeEmpty(), "msName cannot be empty")
	Expect(clusterName).ToNot(BeEmpty(), "clusterName cannot be empty")
	Expect(infrastructureRef.APIGroup).ToNot(BeEmpty(), "infrastructureRef.APIGroup cannot be empty")
	Expect(infrastructureRef.Kind).ToNot(BeEmpty(), "infrastructureRef.Kind cannot be empty")
	Expect(infrastructureRef.Name).ToNot(BeEmpty(), "infrastructureRef.Name cannot be empty")

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
func CreateMachineSet(ctx context.Context, cl client.Client, params machineSetParams) *clusterv1.MachineSet {
	GinkgoHelper()

	By(fmt.Sprintf("Creating MachineSet %q", params.msName))

	ms := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.msName,
			Namespace: CAPINamespace,
		},
		Spec: clusterv1.MachineSetSpec{
			Replicas:    &params.replicas,
			ClusterName: params.clusterName,
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"machine.openshift.io/cluster-api-cluster": params.clusterName,
					machineSetOpenshiftLabelKey:                params.msName,
				},
			},
			Template: clusterv1.MachineTemplateSpec{
				ObjectMeta: clusterv1.ObjectMeta{
					Labels: map[string]string{
						"machine.openshift.io/cluster-api-cluster": params.clusterName,
						machineSetOpenshiftLabelKey:                params.msName,
					},
				},
				Spec: clusterv1.MachineSpec{
					Bootstrap: clusterv1.Bootstrap{
						DataSecretName: &params.userDataSecret,
					},
					ClusterName:       params.clusterName,
					InfrastructureRef: params.infrastructureRef,
				},
			},
		},
	}

	if params.failureDomain != "" {
		ms.Spec.Template.Spec.FailureDomain = params.failureDomain
	}

	Expect(cl.Create(ctx, ms)).To(Succeed(), "Should have successfully created the CAPI MachineSet")

	return ms
}

// WaitForMachineSetsDeleted polls until the given MachineSets are not found, and
// there are zero Machines found matching the MachineSet's label selector.
func WaitForMachineSetsDeleted(machineSets ...*clusterv1.MachineSet) {
	GinkgoHelper()

	for _, ms := range machineSets {
		By(fmt.Sprintf("Waiting for MachineSet %q to be deleted", ms.GetName()))

		// Wait for all machines to be deleted.
		// Uses a direct List instead of GetMachines to avoid nested Eventually.
		selector, err := metav1.LabelSelectorAsSelector(&ms.Spec.Selector)
		Expect(err).ToNot(HaveOccurred(), "invalid label selector on MachineSet %q", ms.GetName())

		Eventually(func() (int, error) {
			machineList := &clusterv1.MachineList{}
			if err := komega.List(machineList,
				client.InNamespace(CAPINamespace),
				client.MatchingLabelsSelector{Selector: selector},
			)(); err != nil {
				return 0, fmt.Errorf("list machines: %w", err)
			}

			return len(machineList.Items), nil
		}, WaitLong, RetryMedium).Should(Equal(0), "Should have deleted all machines for MachineSet %q", ms.GetName())

		// Wait for MachineSet to be deleted
		Eventually(komega.Get(&clusterv1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ms.GetName(),
				Namespace: ms.GetNamespace(),
			},
		}), WaitLong, RetryMedium).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "Should have deleted MachineSet %s/%s", ms.GetNamespace(), ms.GetName())
	}
}

func DeleteMachineSets(ctx context.Context, cl client.Client, machineSets ...*clusterv1.MachineSet) {
	GinkgoHelper()

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

// WaitForMachineSet waits for all Machines belonging to the named MachineSet
// to enter the "Running" phase, and for all nodes belonging to those Machines
// to be ready.
func WaitForMachineSet(ctx context.Context, cl client.Client, name string, namespace string, timeout ...time.Duration) {
	GinkgoHelper()

	By(fmt.Sprintf("Waiting for CAPI MachineSet %q machines to enter Running phase", name))

	machineSet := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}

	Eventually(func() error {
		// Refetch MachineSet each iteration so replicas count is fresh.
		if err := komega.Get(machineSet)(); err != nil {
			return fmt.Errorf("get MachineSet: %w", err)
		}

		selector, err := metav1.LabelSelectorAsSelector(&machineSet.Spec.Selector)
		if err != nil {
			return fmt.Errorf("invalid label selector on MachineSet %q: %w", name, err)
		}

		machineList := &clusterv1.MachineList{}
		if err := komega.List(machineList,
			client.InNamespace(namespace),
			client.MatchingLabelsSelector{Selector: selector},
		)(); err != nil {
			return fmt.Errorf("list machines: %w", err)
		}

		replicas := ptr.Deref(machineSet.Spec.Replicas, 0)

		if len(machineList.Items) != int(replicas) {
			return fmt.Errorf("%q: found %d machines, want %d replicas",
				name, len(machineList.Items), replicas)
		}

		for i := range machineList.Items {
			m := &machineList.Items[i]
			if m.Status.Phase != string(clusterv1.MachinePhaseRunning) {
				return fmt.Errorf("%q: machine %s in phase %q, want Running",
					name, m.Name, m.Status.Phase)
			}

			node, err := GetNodeForMachine(ctx, cl, m)
			if err != nil {
				return fmt.Errorf("%q: machine %s: %w", name, m.Name, err)
			}

			if !isNodeReady(node) {
				return fmt.Errorf("%q: node %s for machine %s is not ready",
					name, node.Name, m.Name)
			}
		}

		return nil
	}, resolveTimeout(WaitOverLong, timeout), RetryMedium).Should(Succeed(),
		"MachineSet %q machines should be Running with ready nodes", name)
}

// GetMachineSet gets a machineset by its name.
func GetMachineSet(name string, namespace string) *clusterv1.MachineSet {
	GinkgoHelper()

	machineSet := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	Eventually(komega.Get(machineSet), time.Minute, RetryShort).Should(Succeed(), "Should have successfully retrieved machineset %s/%s.", machineSet.Namespace, machineSet.Name)

	return machineSet
}

// GetMachinesFromMachineSet returns an array of machines owned by a given machineSet.
func GetMachinesFromMachineSet(machineSet *clusterv1.MachineSet) []*clusterv1.Machine {
	GinkgoHelper()

	machines := GetMachines()

	var machinesForSet []*clusterv1.Machine

	for key := range machines {
		if metav1.IsControlledBy(machines[key], machineSet) {
			machinesForSet = append(machinesForSet, machines[key])
		}
	}

	return machinesForSet
}

// GetNewestMachineFromMachineSet returns the new created machine by a given machineSet.
func GetNewestMachineFromMachineSet(machineSet *clusterv1.MachineSet) *clusterv1.Machine {
	GinkgoHelper()

	machines := GetMachinesFromMachineSet(machineSet)
	Expect(machines).ToNot(BeEmpty(), "Should have found machines for MachineSet %s/%s", machineSet.Namespace, machineSet.Name)

	var machine *clusterv1.Machine

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
	GinkgoHelper()

	machineSet := &clusterv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	Eventually(komega.Update(machineSet, func() {
		machineSet.Spec.Replicas = &replicas
	}), WaitShort, RetryShort).Should(Succeed(), "Should have successfully updated MachineSet %s replicas to %d", machineSet.Name, replicas)
}

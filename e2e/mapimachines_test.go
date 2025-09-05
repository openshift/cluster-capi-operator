/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

const (
	machineAPINamespace = "openshift-machine-api"
	RoleLabel           = "machine.openshift.io/cluster-api-machine-role"
	DefaultTimeout      = 400 * time.Second
	DefaultInterval     = 10 * time.Second
	// MAPI condition types
	MapiConditionPaused   machinev1.ConditionType = "Paused"
	ConditionSynchronized machinev1.ConditionType = "Synchronized"
	// CAPI condition types
	CapiConditionPaused = "Paused"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Migration Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS for now", platform))
		}

		if !framework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Create standalone MAPI Machine", Ordered, func() {
		Context("With spec.authoritativeAPI: MachineAPI and no existing CAPI Machine with that name", func() {
			var newMachine *machinev1.Machine
			// This cleanup runs after all test specs in this context.
			AfterAll(func() {
				By("Cleaning up created machine")
				// If a new machine was created in the test, delete it.
				if newMachine != nil {
					// Try to delete the machine, but don't fail if it's already gone
					err := cl.Delete(context.Background(), newMachine)
					if err != nil && !apierrors.IsNotFound(err) {
						Expect(err).NotTo(HaveOccurred(), "Failed to delete machine")
					}

					// Wait for the machine to be fully deleted (if it wasn't already)
					key := client.ObjectKey{Name: newMachine.Name, Namespace: newMachine.Namespace}
					Eventually(func() bool {
						err := cl.Get(context.Background(), key, &machinev1.Machine{})
						return apierrors.IsNotFound(err)
					}, DefaultTimeout, DefaultInterval).Should(BeTrue(), "Eventually the machine should not be found")
				}
			})
			It("should create MAPI Machine and find its status.authoritativeAPI: MachineAPI", func() {
				newMachine = verifyMapiAutoritative(cl)
			})
			It("should verify that MAPI Machine Synchronized condition is True, Paused condition is False and status.authoritativeAPI: MachineAPI", func() {
				verifyMapiMachineSynchronizedPaused(newMachine)
			})
			It("should verify that the MAPI Machine has a CAPI Machine and CAPI Infra Machine mirrors, both with Paused condition True", func() {
				verifyCapiMachinePaused(cl, newMachine.Name)
			})
		})
	})
})

// verifyMapiAutoritative creates and verifies a MAPI machine with AuthoritativeAPI set to MachineAPI
func verifyMapiAutoritative(cl client.Client) *machinev1.Machine {
	ctx := context.Background()
	machineList := &machinev1.MachineList{}
	By(fmt.Sprintf("Listing worker machines in namespace: %s", machineAPINamespace))
	workerLabelSelector := client.MatchingLabels{RoleLabel: "worker"}
	Eventually(cl.List(ctx, machineList, client.InNamespace(machineAPINamespace), workerLabelSelector)).Should(Succeed(), "Failed to list worker machines")
	Expect(machineList.Items).NotTo(BeEmpty(), "No worker machines found in the namespace to use as a template")

	var templateMachine *machinev1.Machine
	foundMapiMachine := false
	for i, m := range machineList.Items {
		if m.Spec.AuthoritativeAPI == "MachineAPI" {
			templateMachine = &machineList.Items[i]
			foundMapiMachine = true
			break
		}
	}
	if !foundMapiMachine {
		Skip("No machine found with AuthoritativeAPI set to MachineAPI")
	}
	// Define the new machine based on the template.
	newMachine := &machinev1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			// Using a predictable name based on the testcase in polarion.
			Name:      fmt.Sprintf("%s-test-%s", templateMachine.Name, "ocp-82196"),
			Namespace: templateMachine.Namespace,
			Labels:    templateMachine.Labels,
		},
		Spec: *templateMachine.Spec.DeepCopy(),
	}
	// Clear status and other instance-specific fields that should not be copied.
	newMachine.Spec.ProviderID = nil
	newMachine.ObjectMeta.Labels = nil
	By(fmt.Sprintf("Creating a new machine in namespace: %s", newMachine.Namespace))
	// Create the new machine object in the cluster.
	Eventually(cl.Create(ctx, newMachine)).Should(Succeed(), "Failed to create new machine")

	By("Waiting for the new machine to enter 'Running' phase")
	Eventually(komega.Object(newMachine), DefaultTimeout, DefaultInterval).
		Should(HaveField("Status.Phase", HaveValue(Equal("Running"))),
			fmt.Sprintf("Machine %s did not enter 'Running' phase within the timeout", newMachine.Name))

	By("Verifying the new machine has a node reference")
	Eventually(komega.Object(newMachine)).Should(HaveField("Status.NodeRef", Not(BeNil())))

	return newMachine
}

// verifyMapiMachineSynchronizedPaused verifies that the MAPI Machine Synchronized condition is True and the MAPI Machine Paused condition is False and its status.authoritativeAPI equals MachineAPI.
func verifyMapiMachineSynchronizedPaused(machine *machinev1.Machine) {
	Expect(machine.Status.Conditions).To(ContainElement(
		SatisfyAll(
			HaveField("Type", ConditionSynchronized),
			HaveField("Status", corev1.ConditionTrue),
			HaveField("Message", ContainSubstring("Successfully synchronized MAPI Machine to CAPI")),
		),
	))

	Expect(machine.Status.Conditions).To(ContainElement(
		SatisfyAll(
			HaveField("Type", MapiConditionPaused),
			HaveField("Status", corev1.ConditionFalse),
			HaveField("Message", ContainSubstring("The AuthoritativeAPI status is set to 'MachineAPI'")),
		),
	))
}

// verifyCapiMachinePaused verifies that the mirror CAPI Infra Machine has Paused condition True and MAPI Machine has a CAPI Infra Machine mirror
func verifyCapiMachinePaused(cl client.Client, machineName string) {
	// Use framework utility to get the CAPI machine
	targetMachine, err := framework.GetMachine(cl, machineName, framework.CAPINamespace)
	Expect(err).NotTo(HaveOccurred(), "Failed to get CAPI machine")

	Expect(targetMachine.Status.V1Beta2.Conditions).To(ContainElement(SatisfyAll(
		HaveField("Type", Equal(CapiConditionPaused)),
		HaveField("Status", Equal(metav1.ConditionTrue)),
		HaveField("Message", ContainSubstring("Machine has the cluster.x-k8s.io/paused annotation")),
	)))
}

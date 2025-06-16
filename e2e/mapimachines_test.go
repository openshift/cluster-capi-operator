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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

const (
	machineAPINamespace = "openshift-machine-api"
	clusterAPINamespace = "openshift-cluster-api"
	RoleLabel           = "machine.openshift.io/cluster-api-machine-role"
	DefaultTimeout      = 5 * time.Minute
	DefaultInterval     = 10 * time.Second
	// MAPI condition types
	MapiConditionPaused   machinev1.ConditionType = "Paused"
	ConditionSynchronized machinev1.ConditionType = "Synchronized"
	// CAPI condition types
	CapiConditionPaused = "Paused"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machines Migration Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}

		if !framework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Mapi machine creation E2E", Ordered, func() {
		var newMachine *machinev1.Machine
		// This cleanup runs after each test spec.
		AfterEach(func() {
			By("Cleaning up created machine")
			// If a new machine was created in the test, delete it.
			if newMachine != nil {
				err := cl.Delete(context.Background(), newMachine)
				Expect(client.IgnoreNotFound(err)).To(Succeed(), "Failed to delete the new machine")
				// Wait for the machine to be fully deleted.
				Eventually(func() error {
					key := client.ObjectKey{Name: newMachine.Name, Namespace: newMachine.Namespace}
					err := cl.Get(context.Background(), key, &machinev1.Machine{})
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}, DefaultTimeout, DefaultInterval).Should(Succeed(), "New machine was not deleted")
			}
		})

		It("should create a new machine from an existing one,when NO existing CAPI machine and authoritativeAPI is mapi", func() {
			ctx := context.Background()
			machineList := &machinev1.MachineList{}
			By(fmt.Sprintf("Listing worker machines in namespace: %s", machineAPINamespace))
			workerLabelSelector := client.MatchingLabels{RoleLabel: "worker"}
			err := cl.List(ctx, machineList, client.InNamespace(machineAPINamespace), workerLabelSelector)
			Expect(err).NotTo(HaveOccurred(), "Failed to list worker machines")
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
			newMachine = &machinev1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					// Using a predictable name based on the testcase in polarion.
					Name:      fmt.Sprintf("%s-test-%s", templateMachine.Name, "ocp-81829"),
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
			err = cl.Create(ctx, newMachine)
			Expect(err).NotTo(HaveOccurred(), "Failed to create new machine")
			By("Waiting for the new machine to enter 'Running' phase")
			Eventually(komega.Object(newMachine), DefaultTimeout, DefaultInterval).
				Should(HaveField("Status.Phase", HaveValue(Equal("Running"))),
					fmt.Sprintf("Machine %s did not enter 'Running' phase within the timeout", newMachine.Name))

			By("Verifying the new machine has a node reference")
			Eventually(komega.Object(newMachine)).Should(HaveField("Status.NodeRef", Not(BeNil())))

			By("Verifying status of mapi machine for synchronisation done successfully")
			Expect(newMachine.Status.Conditions).To(ContainElement(
				SatisfyAll(
					HaveField("Type", ConditionSynchronized),
					HaveField("Status", corev1.ConditionTrue),
					HaveField("Message", ContainSubstring("Successfully synchronized MAPI Machine to CAPI")),
				),
			))

			Expect(newMachine.Status.Conditions).To(ContainElement(
				SatisfyAll(
					HaveField("Type", MapiConditionPaused),
					HaveField("Status", corev1.ConditionFalse),
					HaveField("Message", ContainSubstring("The AuthoritativeAPI is set to MachineAPI")),
				),
			))

			By("Verifying the corresponding machine in openshift-cluster-api is paused")
			// Get all CAPI machines.
			allMachines, err := framework.GetMachines(cl)
			Expect(err).NotTo(HaveOccurred(), "Failed to get CAPI machines")
			// Find the CAPI machine that corresponds to our new MAPI machine by name.
			var targetMachine *clusterv1.Machine
			for _, capiMachine := range allMachines {
				if capiMachine.Name == newMachine.Name {
					targetMachine = capiMachine
					break
				}
			}
			pausedFound := false
			for _, cond := range targetMachine.Status.V1Beta2.Conditions {
				if cond.Type == CapiConditionPaused &&
					string(cond.Status) == "True" &&
					strings.Contains(cond.Message, "cluster.x-k8s.io/paused annotation") {
					pausedFound = true
					break
				}
			}
			Expect(pausedFound).To(BeTrue(), "Expected Paused condition with Status=True and correct Message substring")
		})
	})
})

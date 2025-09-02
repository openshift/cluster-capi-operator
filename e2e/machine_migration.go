// Copyright 2024 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration][platform:aws][Disruptive] Machine Migration Tests", Ordered, Label("Conformance"), Label("Serial"), func() {
	BeforeAll(func() {
		InitCommonVariables()
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Create MAPI Machine", Ordered, func() {
		var mapiMachineAuthCAPIName = "machine-authoritativeapi-capi"
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *machinev1beta1.Machine
		var err error

		Context("with spec.authoritativeAPI: ClusterAPI and already existing CAPI Machine with same name", func() {
			BeforeAll(func() {
				newCapiMachine = createCAPIMachine(ctx, cl, mapiMachineAuthCAPIName)
				newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPIName, machinev1beta1.MachineAuthorityClusterAPI)

				DeferCleanup(func() {
					By("Cleaning up machine resources")
					cleanupMachineResources(
						ctx,
						cl,
						[]*clusterv1.Machine{newCapiMachine},
						[]*machinev1beta1.Machine{newMapiMachine},
					)
				})
			})

			It("should find MAPI Machine .status.authoritativeAPI to equal ClusterAPI", func() {
				verifyMachineAuthoritative(newMapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})
			// there is a bug for this https://issues.redhat.com/browse/OCPBUGS-54703
			PIt("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMachineSynchronizedCondition(newMapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify MAPI Machine Paused condition is True", func() {
				verifyMAPIMachinePausedCondition(newMapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify CAPI Machine Paused condition is False", func() {
				verifyCAPIMachinePausedCondition(newCapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})
		})

		Context("with spec.authoritativeAPI: ClusterAPI and no existing CAPI Machine with same name", func() {
			BeforeAll(func() {
				newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPIName, machinev1beta1.MachineAuthorityClusterAPI)

				DeferCleanup(func() {
					By("Cleaning up machine resources")
					cleanupMachineResources(
						ctx,
						cl,
						[]*clusterv1.Machine{},
						[]*machinev1beta1.Machine{newMapiMachine},
					)
				})
			})

			It("should verify CAPI Machine gets created and becomes Running", func() {
				verifyMachineRunning(cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should find MAPI Machine .status.authoritativeAPI to equal ClusterAPI", func() {
				verifyMachineAuthoritative(newMapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})
			// there is a bug for this https://issues.redhat.com/browse/OCPBUGS-54703
			PIt("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMachineSynchronizedCondition(newMapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify MAPI Machine Paused condition is True", func() {
				verifyMAPIMachinePausedCondition(newMapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that the non-authoritative MAPI Machine has an authoritative CAPI Machine mirror", func() {
				Eventually(func() error {
					newCapiMachine, err = capiframework.GetMachine(cl, mapiMachineAuthCAPIName, capiframework.CAPINamespace)
					if err != nil {
						return fmt.Errorf("failed to get CAPI Machine: %w", err)
					}

					return nil
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI Machine should exist")
			})

			It("should verify CAPI Machine Paused condition is False", func() {
				verifyCAPIMachinePausedCondition(newCapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	var _ = Describe("Deleting MAPI/CAPI Machines", Ordered, func() {
		var mapiMachineAuthCAPINameDeletion = "machine-authoritativeapi-capi-deletion"
		var mapiMachineAuthMAPINameDeleteMAPIMachine = "machine-authoritativeapi-mapi-delete-mapi"
		var mapiMachineAuthMAPINameDeleteCAPIMachine = "machine-authoritativeapi-mapi-delete-capi"
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *machinev1beta1.Machine
		var err error

		Context("with spec.authoritativeAPI: ClusterAPI", func() {
			Context("when deleting the non-authoritative MAPI Machine", func() {
				BeforeAll(func() {
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPINameDeletion, machinev1beta1.MachineAuthorityClusterAPI)
					verifyMachineRunning(cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)

					DeferCleanup(func() {
						By("Cleaning up machine resources")
						cleanupMachineResources(
							ctx,
							cl,
							[]*clusterv1.Machine{newCapiMachine},
							[]*machinev1beta1.Machine{newMapiMachine},
						)
					})
				})
				It("should delete MAPI Machine", func() {
					Expect(mapiframework.DeleteMachines(ctx, cl, newMapiMachine)).To(Succeed(), "Should be able to delete the new mapi Machine")
					mapiframework.WaitForMachinesDeleted(cl, newMapiMachine)
				})

				It("should verify the CAPI machine is deleted", func() {
					verifyCAPIMachineRemoved(mapiMachineAuthCAPINameDeletion)
				})
				It("should verify the AWS machine is deleted", func() {
					verifyAWSMachineRemoved(mapiMachineAuthCAPINameDeletion)
				})
			})
			Context("when deleting the authoritative CAPI Machine", func() {
				BeforeAll(func() {
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPINameDeletion, machinev1beta1.MachineAuthorityClusterAPI)
					verifyMachineRunning(cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)
					newCapiMachine, err = capiframework.GetMachine(cl, newMapiMachine.Name, capiframework.CAPINamespace)
					Expect(err).NotTo(HaveOccurred(), "Failed to get capi machine")

					DeferCleanup(func() {
						By("Cleaning up machine resources")
						cleanupMachineResources(
							ctx,
							cl,
							[]*clusterv1.Machine{newCapiMachine},
							[]*machinev1beta1.Machine{newMapiMachine},
						)
					})
				})
				It("should delete CAPI Machine", func() {
					capiframework.DeleteMachines(ctx, cl, capiframework.CAPINamespace, newCapiMachine)
				})

				It("should verify the MAPI machine is deleted", func() {
					verifyMAPIMachineRemoved(mapiMachineAuthCAPINameDeletion)
				})
				It("should verify the AWS machine is deleted", func() {
					verifyAWSMachineRemoved(mapiMachineAuthCAPINameDeletion)
				})
			})
		})
		Context("with spec.authoritativeAPI: MachineAPI", func() {
			Context("when deleting the authoritative MAPI Machine", func() {
				BeforeAll(func() {
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthMAPINameDeleteMAPIMachine, machinev1beta1.MachineAuthorityMachineAPI)
					verifyMachineRunning(cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityMachineAPI)

					DeferCleanup(func() {
						By("Cleaning up machine resources")
						cleanupMachineResources(
							ctx,
							cl,
							[]*clusterv1.Machine{newCapiMachine},
							[]*machinev1beta1.Machine{newMapiMachine},
						)
					})
				})
				It("should delete MAPI Machine", func() {
					Expect(mapiframework.DeleteMachines(ctx, cl, newMapiMachine)).To(Succeed(), "Should be able to delete the new mapi Machine")
					mapiframework.WaitForMachinesDeleted(cl, newMapiMachine)
				})

				It("should verify the CAPI machine is deleted", func() {
					verifyCAPIMachineRemoved(mapiMachineAuthMAPINameDeleteMAPIMachine)
				})
				It("should verify the AWS machine is deleted", func() {
					verifyAWSMachineRemoved(mapiMachineAuthMAPINameDeleteMAPIMachine)
				})
			})
			Context("when deleting the non-authoritative CAPI Machine", func() {
				BeforeAll(func() {
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthMAPINameDeleteCAPIMachine, machinev1beta1.MachineAuthorityMachineAPI)
					verifyMachineRunning(cl, newMapiMachine.Name, machinev1beta1.MachineAuthorityMachineAPI)
					newCapiMachine, err = capiframework.GetMachine(cl, newMapiMachine.Name, capiframework.CAPINamespace)
					Expect(err).NotTo(HaveOccurred(), "Failed to get capi machine")

					DeferCleanup(func() {
						By("Cleaning up machine resources")
						cleanupMachineResources(
							ctx,
							cl,
							[]*clusterv1.Machine{newCapiMachine},
							[]*machinev1beta1.Machine{newMapiMachine},
						)
					})
				})
				It("should delete CAPI Machine", func() {
					capiframework.DeleteMachines(ctx, cl, capiframework.CAPINamespace, newCapiMachine)
				})

				It("should verify the MAPI machine is deleted", func() {
					verifyMAPIMachineRemoved(mapiMachineAuthMAPINameDeleteCAPIMachine)
				})
				It("should verify the AWS machine is deleted", func() {
					verifyAWSMachineRemoved(mapiMachineAuthMAPINameDeleteCAPIMachine)
				})
			})
		})
	})
})

func createCAPIMachine(ctx context.Context, cl client.Client, machineName string) *clusterv1.Machine {
	capiMachineList, err := capiframework.GetMachines(ctx, cl)
	Expect(err).NotTo(HaveOccurred(), "Failed to list CAPI machines")
	// The test requires at least one existing CAPI machine to act as a reference for creating a new one.
	Expect(capiMachineList).NotTo(BeEmpty(), "No CAPI machines found in the openshift-cluster-api namespace to use as a reference for creating a new one")

	// Select the first machine from the list as our reference.
	referenceCapiMachine := capiMachineList[0]
	By(fmt.Sprintf("Using CAPI machine %s as a reference", referenceCapiMachine.Name))

	// Define the new machine based on the reference.
	newCapiMachine := &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: referenceCapiMachine.Namespace,
		},
		Spec: *referenceCapiMachine.Spec.DeepCopy(),
	}

	// Clear status and other instance-specific fields that should not be copied.
	newCapiMachine.Spec.ProviderID = nil
	newCapiMachine.Spec.InfrastructureRef.Name = machineName
	newCapiMachine.ObjectMeta.Labels = nil
	newCapiMachine.Status = clusterv1.MachineStatus{}

	By(fmt.Sprintf("Creating a new CAPI machine in namespace: %s", newCapiMachine.Namespace))
	Eventually(func() error {
		return cl.Create(ctx, newCapiMachine)
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Failed to create CAPI machine %s/%s", newCapiMachine.Namespace, newCapiMachine.Name)

	referenceAWSMachine, err := capiframework.GetAWSMachine(cl, referenceCapiMachine.Name, capiframework.CAPINamespace)
	Expect(err).NotTo(HaveOccurred(), "Failed to get AWSMachine")
	// Define the new awsmachine based on the reference.
	newAWSMachine := &awsv1.AWSMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: referenceAWSMachine.Namespace,
		},
		Spec: *referenceAWSMachine.Spec.DeepCopy(),
	}

	// Clear status and other instance-specific fields that should not be copied.
	newAWSMachine.Spec.ProviderID = nil
	newAWSMachine.Spec.InstanceID = nil
	newAWSMachine.ObjectMeta.Labels = nil
	newAWSMachine.Status = awsv1.AWSMachineStatus{}

	By(fmt.Sprintf("Creating a new awsmachine in namespace: %s", newAWSMachine.Namespace))
	Eventually(func() error {
		return cl.Create(ctx, newAWSMachine)
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Failed to create AWSmachine %s/%s", newAWSMachine.Namespace, newAWSMachine.Name)

	verifyMachineRunning(cl, newCapiMachine.Name, machinev1beta1.MachineAuthorityClusterAPI)

	return newCapiMachine
}

func createMAPIMachineWithAuthority(ctx context.Context, cl client.Client, machineName string, authority machinev1beta1.MachineAuthority) *machinev1beta1.Machine {
	workerLabelSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			"machine.openshift.io/cluster-api-machine-role": "worker",
		},
	}
	machineList, err := mapiframework.GetMachines(ctx, cl, &workerLabelSelector)

	Expect(err).NotTo(HaveOccurred(), "Failed to list MAPI machines")
	// The test requires at least one existing MAPI machine to act as a reference for creating a new one.
	Expect(machineList).NotTo(BeEmpty(), "No MAPI machines found in the openshift-machine-api namespace to use as a reference for creating a new one")

	// Select the first machine from the list as our reference.
	referenceMachine := machineList[0]
	By(fmt.Sprintf("Using MAPI machine %s as a reference", referenceMachine.Name))

	// Define the new machine based on the reference.
	newMachine := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: referenceMachine.Namespace,
		},
		Spec: *referenceMachine.Spec.DeepCopy(),
	}

	// Clear status and other instance-specific fields that should not be copied.
	newMachine.Spec.ProviderID = nil
	newMachine.ObjectMeta.Labels = nil
	newMachine.Status = machinev1beta1.MachineStatus{}
	newMachine.Spec.AuthoritativeAPI = authority
	By(fmt.Sprintf("Creating a new MAPI machine with AuthoritativeAPI: %s in namespace: %s", authority, newMachine.Namespace))
	Eventually(func() error {
		return cl.Create(ctx, newMachine)
	}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Failed to create MAPI machine %s with AuthoritativeAPI: %s", newMachine.Name, authority)

	return newMachine
}

func verifyMachineRunning(cl client.Client, machineName string, authority machinev1beta1.MachineAuthority) {
	Eventually(func() string {
		//nolint:exhaustive
		switch authority {
		case machinev1beta1.MachineAuthorityClusterAPI:
			By("Verify the CAPI Machine is Running")
			capiMachine, err := capiframework.GetMachine(cl, machineName, capiframework.CAPINamespace)
			if err != nil {
				return ""
			}

			return capiMachine.Status.Phase
		case machinev1beta1.MachineAuthorityMachineAPI:
			By("Verify the MAPI Machine is Running")
			mapiMachine, err := mapiframework.GetMachine(cl, machineName)
			if err != nil {
				return ""
			}
			if mapiMachine.Status.Phase != nil {
				return *mapiMachine.Status.Phase
			}

			return ""
		default:
			Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
			return ""
		}
	}, capiframework.WaitLong, capiframework.RetryLong).Should(Equal("Running"), "%s Machine did not get Running", authority)
}

func verifyMachineAuthoritative(mapiMachine *machinev1beta1.Machine, authority machinev1beta1.MachineAuthority) {
	By(fmt.Sprintf("Verify the Machine authoritative is %s", authority))
	Eventually(komega.Object(mapiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.AuthoritativeAPI", Equal(authority)),
		fmt.Sprintf("Expected Machine with correct status.AuthoritativeAPI %s", authority),
	)
}

func verifyMachineSynchronizedCondition(mapiMachine *machinev1beta1.Machine, authority machinev1beta1.MachineAuthority) {
	By("Verify the MAPI Machine synchronized condition is True")

	var expectedMessage string
	//nolint:exhaustive
	switch authority {
	case machinev1beta1.MachineAuthorityMachineAPI:
		expectedMessage = "Successfully synchronized MAPI Machine to CAPI"
	case machinev1beta1.MachineAuthorityClusterAPI:
		expectedMessage = "Successfully synchronized CAPI Machine to MAPI"
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(mapiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		WithTransform(
			func(m *machinev1beta1.Machine) []machinev1beta1.Condition {
				return m.Status.Conditions
			},
			ContainElement(
				SatisfyAll(
					HaveField("Type", Equal(SynchronizedCondition)),
					HaveField("Status", Equal(corev1.ConditionTrue)),
					HaveField("Reason", Equal("ResourceSynchronized")),
					HaveField("Message", Equal(expectedMessage)),
				),
			),
		),
		fmt.Sprintf("Expected Synchronized condition for %s not found or incorrect", authority),
	)
}

func verifyMAPIMachinePausedCondition(mapiMachine *machinev1beta1.Machine, authority machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	//nolint:exhaustive
	switch authority {
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verify the MAPI Machine is Unpaused")

		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(MAPIPausedCondition)),
			HaveField("Status", Equal(corev1.ConditionFalse)),
			HaveField("Reason", Equal("AuthoritativeAPIMachineAPI")),
			HaveField("Message", ContainSubstring("MachineAPI")),
		)
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verify the MAPI Machine is Paused")

		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(MAPIPausedCondition)),
			HaveField("Status", Equal(corev1.ConditionTrue)),
			HaveField("Reason", Equal("AuthoritativeAPINotMachineAPI")),
			HaveField("Message", ContainSubstring("ClusterAPI")),
		)
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(mapiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.Conditions", ContainElement(conditionMatcher)),
		fmt.Sprintf("Expected MAPI Machine with correct paused condition for %s", authority),
	)
}

func verifyCAPIMachinePausedCondition(capiMachine *clusterv1.Machine, authority machinev1beta1.MachineAuthority) {
	var conditionMatcher types.GomegaMatcher

	//nolint:exhaustive
	switch authority {
	case machinev1beta1.MachineAuthorityClusterAPI:
		By("Verify the CAPI Machine is Unpaused")

		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(CAPIPausedCondition)),
			HaveField("Status", Equal(metav1.ConditionFalse)),
			HaveField("Reason", Equal("NotPaused")),
		)
	case machinev1beta1.MachineAuthorityMachineAPI:
		By("Verify the CAPI Machine is Paused")

		conditionMatcher = SatisfyAll(
			HaveField("Type", Equal(CAPIPausedCondition)),
			HaveField("Status", Equal(metav1.ConditionTrue)),
			HaveField("Reason", Equal("Paused")),
		)
	default:
		Fail(fmt.Sprintf("unknown authoritativeAPI type: %v", authority))
	}

	Eventually(komega.Object(capiMachine), capiframework.WaitMedium, capiframework.RetryMedium).Should(
		HaveField("Status.V1Beta2.Conditions", ContainElement(conditionMatcher)),
		fmt.Sprintf("Expected CAPI Machine with correct paused condition for %s", authority),
	)
}

func verifyCAPIMachineRemoved(machineName string) {
	By(fmt.Sprintf("Verifying the CAPI Machine %s is removed", machineName))
	Eventually(komega.Get(&clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: capiframework.CAPINamespace,
		},
	}), time.Minute).Should(WithTransform(apierrors.IsNotFound, BeTrue()))
}

func verifyAWSMachineRemoved(machineName string) {
	By(fmt.Sprintf("Verifying the AWSMachine %s is removed", machineName))
	Eventually(komega.Get(&awsv1.AWSMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: capiframework.CAPINamespace,
		},
	}), time.Minute).Should(WithTransform(apierrors.IsNotFound, BeTrue()))
}

func verifyMAPIMachineRemoved(machineName string) {
	By(fmt.Sprintf("Verifying the MAPI Machine %s is removed", machineName))
	Eventually(komega.Get(&machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: capiframework.MAPINamespace,
		},
	}), time.Minute).Should(WithTransform(apierrors.IsNotFound, BeTrue()))
}

func cleanupMachineResources(ctx context.Context, cl client.Client, capiMachines []*clusterv1.Machine, mapiMachines []*machinev1beta1.Machine) {
	for _, m := range capiMachines {
		if m == nil {
			continue
		}

		By(fmt.Sprintf("Deleting CAPI Machine %s", m.Name))
		capiframework.DeleteMachines(ctx, cl, capiframework.CAPINamespace, m)
	}

	for _, m := range mapiMachines {
		if m == nil {
			continue
		}

		By(fmt.Sprintf("Deleting MAPI Machine %s", m.Name))
		Expect(mapiframework.DeleteMachines(ctx, cl, m)).To(Succeed(), "Should be able to delete all Machines")
		mapiframework.WaitForMachinesDeleted(cl, m)
	}
}

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	corev1 "k8s.io/api/core/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// Test constants for field modification tests
const (
	testAMIID                 = "ami-different123"
	testSubnetID              = "subnet-different123"
	testSecurityGroupID       = "sg-different123"
	testCapacityReservationID = "cr-different123456789"
	testAvailabilityZone      = "us-east-1b"
	testInstanceType          = "m5.xlarge"
	testVolumeSize            = int64(200)
	testVolumeType            = "io1"
	testTagName               = "test-tag"
	testTagValue              = "test-value"
	testAnnotationKey         = "machine.openshift.io/test-annotation"
	testAnnotationValue       = "test-value"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Migration Tests", Ordered, func() {
	BeforeAll(func() {
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
			//there is a bug for this https://issues.redhat.com/browse/OCPBUGS-54703
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
			//there is a bug for this https://issues.redhat.com/browse/OCPBUGS-54703
			PIt("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMachineSynchronizedCondition(newMapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify MAPI Machine Paused condition is True", func() {
				verifyMAPIMachinePausedCondition(newMapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that the non-authoritative MAPI Machine has an authoritative CAPI Machine mirror", func() {
				newCapiMachine, err = capiframework.GetMachine(cl, mapiMachineAuthCAPIName, capiframework.CAPINamespace)
				Expect(err).ToNot(HaveOccurred(), "CAPI Machine should exist")
			})

			It("should verify CAPI Machine Paused condition is False", func() {
				verifyCAPIMachinePausedCondition(newCapiMachine, machinev1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	var _ = Describe("Update MAPI Machine", Ordered, func() {
		Context("with spec.authoritativeAPI modification", func() {
			It("should allow modification of authoritativeAPI from ClusterAPI to MachineAPI", func() {
				By("Attempting to modify authoritativeAPI from ClusterAPI to MachineAPI")

				// Create a new machine for this test to avoid affecting other tests
				testMachineName := "machine-auth-change-83955"
				testMachine := createMAPIMachineWithAuthority(ctx, cl, testMachineName, machinev1beta1.MachineAuthorityClusterAPI)

				_, err := capiframework.GetMachine(cl, testMachineName, capiframework.CAPINamespace)
				Expect(err).ToNot(HaveOccurred(), "CAPI Machine should be created")
				// Ensure any pending updates are processed
				Eventually(func() error {
					// Get the latest version again right before update
					currentMachine, err := mapiframework.GetMachine(cl, testMachineName)
					if err != nil {
						return err
					}

					updatedMachine := currentMachine.DeepCopy()
					updatedMachine.Spec.AuthoritativeAPI = machinev1beta1.MachineAuthorityMachineAPI

					err = cl.Update(ctx, updatedMachine)
					return err
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should allow modification of authoritativeAPI")

				// Verify the machine still exists after the change (don't wait for specific status)
				_, err = mapiframework.GetMachine(cl, testMachineName)
				Expect(err).ToNot(HaveOccurred(), "Machine should still exist after authoritativeAPI change")

				// Clean up the test machine
				DeferCleanup(func() {
					By("Cleaning up test machine")
					// Try to delete the MAPI machine first
					if testMachine != nil {
						mapiframework.DeleteMachines(ctx, cl, testMachine)
					}
					// Try to delete the CAPI machine as well
					capiMachine := &clusterv1.Machine{}
					err := cl.Get(ctx, client.ObjectKey{Name: testMachineName, Namespace: capiframework.CAPINamespace}, capiMachine)
					if err == nil {
						cl.Delete(ctx, capiMachine)
					}
					// Don't wait for deletion to complete - just attempt it
				})
			})
		})

		Context("with spec.authoritativeAPI: ClusterAPI", func() {
			Context("with Validating Admission Policies in place preventing changes to non-authoritative Machines except from the sync controller", func() {
				var testMachineName = "machine-vap-83955"
				var testMapiMachine *machinev1beta1.Machine
				var testCapiMachine *clusterv1.Machine

				BeforeAll(func() {
					testMapiMachine = createMAPIMachineWithAuthority(ctx, cl, testMachineName, machinev1beta1.MachineAuthorityClusterAPI)

					// Ensure CAPI Machine is created and running as a prerequisite
					verifyMachineRunning(cl, testMachineName, machinev1beta1.MachineAuthorityClusterAPI)

					DeferCleanup(func() {
						By("Cleaning up machine resources")
						cleanupMachineResources(
							ctx,
							cl,
							[]*clusterv1.Machine{testCapiMachine},
							[]*machinev1beta1.Machine{testMapiMachine},
						)
					})
				})

				It("should verify that the non-authoritative MAPI Machine has an authoritative CAPI Machine mirror", func() {
					verifyCAPIMachineExists(cl, testMachineName)
				})

				It("should prevent modification of InstanceType in non-authoritative MAPI Machine", func() {
					verifyFieldModificationPrevented(cl, testMapiMachine.Name, "InstanceType", func(machine *machinev1beta1.Machine) {
						Expect(updateAWSMachineProviderSpec(machine, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
							providerSpec.InstanceType = testInstanceType
						})).To(Succeed(), "Failed to update InstanceType in provider spec")
					})
				})

				It("should prevent removal of labels in non-authoritative MAPI Machine", func() {
					verifyFieldModificationPrevented(cl, testMapiMachine.Name, "labels", func(machine *machinev1beta1.Machine) {
						machine.Labels = nil
					})
				})

				It("should prevent addition of annotations machine.openshift.io. in non-authoritative MAPI Machine", func() {
					verifyAnnotationModificationPrevented(cl, testMapiMachine.Name)
				})

				It("should prevent modification of AMI ID in non-authoritative MAPI Machine", func() {
					verifyFieldModificationPrevented(cl, testMapiMachine.Name, "AMI ID", func(machine *machinev1beta1.Machine) {
						Expect(updateAWSMachineProviderSpec(machine, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
							providerSpec.AMI.ID = ptr.To(testAMIID)
						})).To(Succeed(), "Failed to update AMI ID in provider spec")
					})
				})

				It("should prevent modification of encryption for block devices in non-authoritative MAPI Machine", func() {
					verifyFieldModificationPrevented(cl, testMapiMachine.Name, "block device encryption", func(machine *machinev1beta1.Machine) {
						Expect(updateAWSMachineProviderSpec(machine, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
							if len(providerSpec.BlockDevices) > 0 && providerSpec.BlockDevices[0].EBS != nil {
								providerSpec.BlockDevices[0].EBS.Encrypted = ptr.To(false)
							}
						})).To(Succeed(), "Failed to update block device encryption in provider spec")
					})
				})

				It("should prevent modification of VolumeSize in non-authoritative MAPI Machine", func() {
					verifyFieldModificationPrevented(cl, testMapiMachine.Name, "VolumeSize", func(machine *machinev1beta1.Machine) {
						Expect(updateAWSMachineProviderSpec(machine, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
							if len(providerSpec.BlockDevices) > 0 && providerSpec.BlockDevices[0].EBS != nil {
								providerSpec.BlockDevices[0].EBS.VolumeSize = ptr.To(testVolumeSize)
							}
						})).To(Succeed(), "Failed to update VolumeSize in provider spec")
					})
				})

				It("should prevent modification of VolumeType in non-authoritative MAPI Machine", func() {
					verifyFieldModificationPrevented(cl, testMapiMachine.Name, "VolumeType", func(machine *machinev1beta1.Machine) {
						Expect(updateAWSMachineProviderSpec(machine, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
							if len(providerSpec.BlockDevices) > 0 && providerSpec.BlockDevices[0].EBS != nil {
								providerSpec.BlockDevices[0].EBS.VolumeType = ptr.To(testVolumeType)
							}
						})).To(Succeed(), "Failed to update VolumeType in provider spec")
					})
				})

				It("should prevent modification of AvailabilityZone in non-authoritative MAPI Machine", func() {
					verifyFieldModificationPrevented(cl, testMapiMachine.Name, "AvailabilityZone", func(machine *machinev1beta1.Machine) {
						Expect(updateAWSMachineProviderSpec(machine, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
							providerSpec.Placement.AvailabilityZone = testAvailabilityZone
						})).To(Succeed(), "Failed to update AvailabilityZone in provider spec")
					})
				})

				It("should prevent modification of Subnet in non-authoritative MAPI Machine", func() {
					verifyFieldModificationPrevented(cl, testMapiMachine.Name, "Subnet", func(machine *machinev1beta1.Machine) {
						Expect(updateAWSMachineProviderSpec(machine, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
							providerSpec.Subnet.ID = ptr.To(testSubnetID)
						})).To(Succeed(), "Failed to update Subnet in provider spec")
					})
				})

				It("should prevent modification of SecurityGroups in non-authoritative MAPI Machine", func() {
					verifyFieldModificationPrevented(cl, testMapiMachine.Name, "SecurityGroups", func(machine *machinev1beta1.Machine) {
						Expect(updateAWSMachineProviderSpec(machine, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
							providerSpec.SecurityGroups = append(providerSpec.SecurityGroups, machinev1beta1.AWSResourceReference{
								ID: ptr.To(testSecurityGroupID),
							})
						})).To(Succeed(), "Failed to update SecurityGroups in provider spec")
					})
				})

				It("should prevent modification of Tags in non-authoritative MAPI Machine", func() {
					verifyFieldModificationPrevented(cl, testMapiMachine.Name, "Tags", func(machine *machinev1beta1.Machine) {
						Expect(updateAWSMachineProviderSpec(machine, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
							providerSpec.Tags = append(providerSpec.Tags, machinev1beta1.TagSpecification{
								Name:  testTagName,
								Value: testTagValue,
							})
						})).To(Succeed(), "Failed to update Tags in provider spec")
					})
				})

				It("should prevent modification of capacityReservationId in non-authoritative MAPI Machine", func() {
					verifyFieldModificationPrevented(cl, testMapiMachine.Name, "capacityReservationId", func(machine *machinev1beta1.Machine) {
						Expect(updateAWSMachineProviderSpec(machine, func(providerSpec *machinev1beta1.AWSMachineProviderConfig) {
							providerSpec.CapacityReservationID = testCapacityReservationID
						})).To(Succeed(), "Failed to update capacityReservationId in provider spec")
					})
				})
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
					mapiframework.DeleteMachines(ctx, cl, newMapiMachine)
					mapiframework.WaitForMachinesDeleted(cl, newMapiMachine)
				})

				It("should verify the CAPI machine is deleted", func() {
					verifyCAPIMachineRemoved(cl, mapiMachineAuthCAPINameDeletion)
				})
				It("should verify the AWS machine is deleted", func() {
					verifyAWSMachineRemoved(cl, mapiMachineAuthCAPINameDeletion)
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
					capiframework.DeleteMachines(cl, capiframework.CAPINamespace, newCapiMachine)
				})

				It("should verify the MAPI machine is deleted", func() {
					verifyMAPIMachineRemoved(cl, mapiMachineAuthCAPINameDeletion)
				})
				It("should verify the AWS machine is deleted", func() {
					verifyAWSMachineRemoved(cl, mapiMachineAuthCAPINameDeletion)
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
					mapiframework.DeleteMachines(ctx, cl, newMapiMachine)
					mapiframework.WaitForMachinesDeleted(cl, newMapiMachine)
				})

				It("should verify the CAPI machine is deleted", func() {
					verifyCAPIMachineRemoved(cl, mapiMachineAuthMAPINameDeleteMAPIMachine)
				})
				It("should verify the AWS machine is deleted", func() {
					verifyAWSMachineRemoved(cl, mapiMachineAuthMAPINameDeleteMAPIMachine)
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
					capiframework.DeleteMachines(cl, capiframework.CAPINamespace, newCapiMachine)
				})

				It("should verify the MAPI machine is deleted", func() {
					verifyMAPIMachineRemoved(cl, mapiMachineAuthMAPINameDeleteCAPIMachine)
				})
				It("should verify the AWS machine is deleted", func() {
					verifyAWSMachineRemoved(cl, mapiMachineAuthMAPINameDeleteCAPIMachine)
				})
			})
		})
	})
})

func createCAPIMachine(ctx context.Context, cl client.Client, machineName string) *clusterv1.Machine {
	capiMachineList, err := capiframework.GetMachines(cl)
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
		switch authority {
		case machinev1beta1.MachineAuthorityClusterAPI:
			By("Verify the CAPI Machine is Running")
			capiMachine, err := capiframework.GetMachine(cl, machineName, capiframework.CAPINamespace)
			Expect(err).NotTo(HaveOccurred(), "Failed to get CAPI Machine Running %s", machineName)
			return string(capiMachine.Status.Phase)
		case machinev1beta1.MachineAuthorityMachineAPI:
			By("Verify the MAPI Machine is Running")
			mapiMachine, err := mapiframework.GetMachine(cl, machineName)
<<<<<<< HEAD
			if err != nil {
				return ""
			}
			if mapiMachine.Status.Phase != nil {
				return string(*mapiMachine.Status.Phase)
			}
			return ""
=======
			Expect(err).NotTo(HaveOccurred(), "Failed to get MAPI Machine Running %s", machineName)
			return string(*mapiMachine.Status.Phase)
>>>>>>> d835b87f (simplified pattern ,for assertion.)
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

func verifyCAPIMachineRemoved(cl client.Client, machineName string) {
	By(fmt.Sprintf("Verifying the CAPI Machine %s is removed", machineName))
	Eventually(komega.Get(&clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: capiframework.CAPINamespace,
		},
	}), time.Minute).Should(WithTransform(apierrors.IsNotFound, BeTrue()))
}

func verifyAWSMachineRemoved(cl client.Client, machineName string) {
	By(fmt.Sprintf("Verifying the AWSMachine %s is removed", machineName))
	Eventually(komega.Get(&awsv1.AWSMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      machineName,
			Namespace: capiframework.CAPINamespace,
		},
	}), time.Minute).Should(WithTransform(apierrors.IsNotFound, BeTrue()))
}

func verifyMAPIMachineRemoved(cl client.Client, machineName string) {
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
		capiframework.DeleteMachines(cl, capiframework.CAPINamespace, m)
	}

	for _, m := range mapiMachines {
		if m == nil {
			continue
		}
		By(fmt.Sprintf("Deleting MAPI Machine %s", m.Name))
		mapiframework.DeleteMachines(ctx, cl, m)
		mapiframework.WaitForMachinesDeleted(cl, m)
	}
}

func verifyCAPIMachineExists(cl client.Client, machineName string) {
	_, err := capiframework.GetMachine(cl, machineName, capiframework.CAPINamespace)
	Expect(err).ToNot(HaveOccurred(), "CAPI Machine should exist")
}

func verifyFieldModificationPrevented(cl client.Client, machineName string, fieldName string, modifyFunc func(*machinev1beta1.Machine)) {
	By(fmt.Sprintf("Attempting to modify %s in MAPI Machine", fieldName))

	currentMachine, err := mapiframework.GetMachine(cl, machineName)
	Expect(err).NotTo(HaveOccurred(), "Failed to get current machine")
	updatedMachine := currentMachine.DeepCopy()
	modifyFunc(updatedMachine)

	err = cl.Update(ctx, updatedMachine)
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
}

func verifyAnnotationModificationPrevented(cl client.Client, machineName string) {
	By("Attempting to add annotations machine.openshift.io. to MAPI Machine")

	currentMachine, err := mapiframework.GetMachine(cl, machineName)
	Expect(err).NotTo(HaveOccurred(), "Failed to get current machine")
	updatedMachine := currentMachine.DeepCopy()
	if updatedMachine.Annotations == nil {
		updatedMachine.Annotations = make(map[string]string)
	}
	updatedMachine.Annotations[testAnnotationKey] = testAnnotationValue

	err = cl.Update(ctx, updatedMachine)
	Expect(err).To(HaveOccurred())
	Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
	Expect(err.Error()).To(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* annotation"))
}

// getAWSProviderSpecFromMachine extracts and unmarshals the AWS provider spec from a machine
func getAWSProviderSpecFromMachine(machine *machinev1beta1.Machine) (*machinev1beta1.AWSMachineProviderConfig, error) {
	if machine.Spec.ProviderSpec.Value == nil {
		return nil, fmt.Errorf("provider spec value is nil")
	}

	// Use the existing AWSProviderSpecFromRawExtension function to avoid code duplication
	providerSpec, err := mapi2capi.AWSProviderSpecFromRawExtension(machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to extract AWS provider spec: %w", err)
	}

	return &providerSpec, nil
}

// updateAWSMachineProviderSpec updates the machine's AWS provider spec with the given function
func updateAWSMachineProviderSpec(machine *machinev1beta1.Machine, updateFunc func(*machinev1beta1.AWSMachineProviderConfig)) error {
	providerSpec, err := getAWSProviderSpecFromMachine(machine)
	if err != nil {
		return err
	}

	updateFunc(providerSpec)

	rawProviderSpec, err := json.Marshal(providerSpec)
	if err != nil {
		return fmt.Errorf("failed to marshal provider spec: %w", err)
	}

	machine.Spec.ProviderSpec.Value.Raw = rawProviderSpec
	return nil
}

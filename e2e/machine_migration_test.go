package e2e

import (
	"context"
	"encoding/json"
	"fmt"

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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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
				Eventually(func() error {
					newCapiMachine, err = capiframework.GetMachine(cl, mapiMachineAuthCAPIName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI Machine should exist")
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

				// Wait for the CAPI machine to be created (any status is fine for this test)
				Eventually(func() error {
					_, err := capiframework.GetMachine(cl, testMachineName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitLong, capiframework.RetryLong).Should(Succeed(), "CAPI Machine should be created")

				// Add a small delay to ensure any pending updates are processed
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
				Eventually(func() error {
					_, err := mapiframework.GetMachine(cl, testMachineName)
					return err
				}, capiframework.WaitLong, capiframework.RetryLong).Should(Succeed(), "Machine should still exist after authoritativeAPI change")

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

		Context("with spec.authoritativeAPI: ClusterAPI, Prevent changes to non-authoritative Machines except from sync controller", func() {
			var testMachineName = "machine-vap-83955"
			var testMapiMachine *machinev1beta1.Machine
			var testCapiMachine *clusterv1.Machine

			BeforeAll(func() {
				testMapiMachine = createMAPIMachineWithAuthority(ctx, cl, testMachineName, machinev1beta1.MachineAuthorityClusterAPI)

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

			It("should verify CAPI Machine gets created and becomes Running", func() {
				verifyMachineRunning(cl, testMachineName, machinev1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that the non-authoritative MAPI Machine has an authoritative CAPI Machine mirror", func() {
				Eventually(func() error {
					_, err := capiframework.GetMachine(cl, testMachineName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitLong, capiframework.RetryLong).Should(Succeed(), "CAPI Machine should exist")
			})

			It("should prevent modification of InstanceType in non-authoritative MAPI Machine", func() {
				By("Attempting to modify InstanceType in MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				updatedMachine := currentMachine.DeepCopy()
				// Modify the InstanceType in the provider spec
				if updatedMachine.Spec.ProviderSpec.Value != nil {
					var providerSpec machinev1beta1.AWSMachineProviderConfig
					if err := json.Unmarshal(updatedMachine.Spec.ProviderSpec.Value.Raw, &providerSpec); err != nil {
						Fail(fmt.Sprintf("Failed to unmarshal provider spec: %v", err))
					}
					providerSpec.InstanceType = "m5.xlarge" // Change from original
					rawProviderSpec, err := json.Marshal(providerSpec)
					if err != nil {
						Fail(fmt.Sprintf("Failed to marshal provider spec: %v", err))
					}
					updatedMachine.Spec.ProviderSpec.Value.Raw = rawProviderSpec
				}

				err = cl.Update(ctx, updatedMachine)
				if err != nil {
					fmt.Printf("Update error: %v\n", err.Error())
				}
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
			})

			It("should prevent removal of labels in non-authoritative MAPI Machine", func() {
				By("Attempting to remove labels from MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				updatedMachine := currentMachine.DeepCopy()
				updatedMachine.Labels = nil
				err = cl.Update(ctx, updatedMachine)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
			})

			It("should prevent addition of annotations machine.openshift.io. in non-authoritative MAPI Machine", func() {
				By("Attempting to add annotations machine.openshift.io. to MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				// Try to add a test annotation that should be protected
				updatedMachine := currentMachine.DeepCopy()
				if updatedMachine.Annotations == nil {
					updatedMachine.Annotations = make(map[string]string)
				}

				// Add a test annotation that should be protected
				updatedMachine.Annotations["machine.openshift.io/test-annotation"] = "test-value"
				fmt.Printf("Attempting to add test annotation: machine.openshift.io/test-annotation\n")

				fmt.Printf("Updated machine annotations: %+v\n", updatedMachine.Annotations)

				err = cl.Update(ctx, updatedMachine)
				if err != nil {
					fmt.Printf("Update error: %v\n", err.Error())
				}
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
				Expect(err.Error()).To(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* annotation"))
			})

			It("should prevent modification of AMI ID in non-authoritative MAPI Machine", func() {
				By("Attempting to modify AMI ID in MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				updatedMachine := currentMachine.DeepCopy()
				if updatedMachine.Spec.ProviderSpec.Value != nil {
					var providerSpec machinev1beta1.AWSMachineProviderConfig
					if err := json.Unmarshal(updatedMachine.Spec.ProviderSpec.Value.Raw, &providerSpec); err != nil {
						Fail(fmt.Sprintf("Failed to unmarshal provider spec: %v", err))
					}
					providerSpec.AMI.ID = ptr.To("ami-different123")
					rawProviderSpec, err := json.Marshal(providerSpec)
					if err != nil {
						Fail(fmt.Sprintf("Failed to marshal provider spec: %v", err))
					}
					updatedMachine.Spec.ProviderSpec.Value.Raw = rawProviderSpec
				}
				err = cl.Update(ctx, updatedMachine)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
			})

			It("should prevent modification of encryption for block devices in non-authoritative MAPI Machine", func() {
				By("Attempting to modify encryption for block devices in MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				updatedMachine := currentMachine.DeepCopy()
				if updatedMachine.Spec.ProviderSpec.Value != nil {
					var providerSpec machinev1beta1.AWSMachineProviderConfig
					if err := json.Unmarshal(updatedMachine.Spec.ProviderSpec.Value.Raw, &providerSpec); err != nil {
						Fail(fmt.Sprintf("Failed to unmarshal provider spec: %v", err))
					}
					// Modify encryption setting for block devices
					if len(providerSpec.BlockDevices) > 0 && providerSpec.BlockDevices[0].EBS != nil {
						providerSpec.BlockDevices[0].EBS.Encrypted = ptr.To(false)
					}
					rawProviderSpec, err := json.Marshal(providerSpec)
					if err != nil {
						Fail(fmt.Sprintf("Failed to marshal provider spec: %v", err))
					}
					updatedMachine.Spec.ProviderSpec.Value.Raw = rawProviderSpec
				}
				err = cl.Update(ctx, updatedMachine)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
			})

			It("should prevent modification of VolumeSize in non-authoritative MAPI Machine", func() {
				By("Attempting to modify VolumeSize in MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				updatedMachine := currentMachine.DeepCopy()
				if updatedMachine.Spec.ProviderSpec.Value != nil {
					var providerSpec machinev1beta1.AWSMachineProviderConfig
					if err := json.Unmarshal(updatedMachine.Spec.ProviderSpec.Value.Raw, &providerSpec); err != nil {
						Fail(fmt.Sprintf("Failed to unmarshal provider spec: %v", err))
					}
					// Modify volume size for block devices
					if len(providerSpec.BlockDevices) > 0 && providerSpec.BlockDevices[0].EBS != nil {
						providerSpec.BlockDevices[0].EBS.VolumeSize = ptr.To(int64(200))
					}
					rawProviderSpec, err := json.Marshal(providerSpec)
					if err != nil {
						Fail(fmt.Sprintf("Failed to marshal provider spec: %v", err))
					}
					updatedMachine.Spec.ProviderSpec.Value.Raw = rawProviderSpec
				}
				err = cl.Update(ctx, updatedMachine)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
			})

			It("should prevent modification of VolumeType in non-authoritative MAPI Machine", func() {
				By("Attempting to modify VolumeType in MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				updatedMachine := currentMachine.DeepCopy()
				if updatedMachine.Spec.ProviderSpec.Value != nil {
					var providerSpec machinev1beta1.AWSMachineProviderConfig
					if err := json.Unmarshal(updatedMachine.Spec.ProviderSpec.Value.Raw, &providerSpec); err != nil {
						Fail(fmt.Sprintf("Failed to unmarshal provider spec: %v", err))
					}
					// Modify volume type for block devices
					if len(providerSpec.BlockDevices) > 0 && providerSpec.BlockDevices[0].EBS != nil {
						providerSpec.BlockDevices[0].EBS.VolumeType = ptr.To("io1")
					}
					rawProviderSpec, err := json.Marshal(providerSpec)
					if err != nil {
						Fail(fmt.Sprintf("Failed to marshal provider spec: %v", err))
					}
					updatedMachine.Spec.ProviderSpec.Value.Raw = rawProviderSpec
				}
				err = cl.Update(ctx, updatedMachine)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
			})

			It("should prevent modification of AvailabilityZone in non-authoritative MAPI Machine", func() {
				By("Attempting to modify AvailabilityZone in MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				updatedMachine := currentMachine.DeepCopy()
				if updatedMachine.Spec.ProviderSpec.Value != nil {
					var providerSpec machinev1beta1.AWSMachineProviderConfig
					if err := json.Unmarshal(updatedMachine.Spec.ProviderSpec.Value.Raw, &providerSpec); err != nil {
						Fail(fmt.Sprintf("Failed to unmarshal provider spec: %v", err))
					}
					providerSpec.Placement.AvailabilityZone = "us-east-1b"
					rawProviderSpec, err := json.Marshal(providerSpec)
					if err != nil {
						Fail(fmt.Sprintf("Failed to marshal provider spec: %v", err))
					}
					updatedMachine.Spec.ProviderSpec.Value.Raw = rawProviderSpec
				}
				err = cl.Update(ctx, updatedMachine)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
			})

			It("should prevent modification of Subnet in non-authoritative MAPI Machine", func() {
				By("Attempting to modify Subnet in MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				updatedMachine := currentMachine.DeepCopy()
				if updatedMachine.Spec.ProviderSpec.Value != nil {
					var providerSpec machinev1beta1.AWSMachineProviderConfig
					if err := json.Unmarshal(updatedMachine.Spec.ProviderSpec.Value.Raw, &providerSpec); err != nil {
						Fail(fmt.Sprintf("Failed to unmarshal provider spec: %v", err))
					}
					providerSpec.Subnet.ID = ptr.To("subnet-different123")
					rawProviderSpec, err := json.Marshal(providerSpec)
					if err != nil {
						Fail(fmt.Sprintf("Failed to marshal provider spec: %v", err))
					}
					updatedMachine.Spec.ProviderSpec.Value.Raw = rawProviderSpec
				}
				err = cl.Update(ctx, updatedMachine)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
			})

			It("should prevent modification of SecurityGroups in non-authoritative MAPI Machine", func() {
				By("Attempting to modify SecurityGroups in MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				updatedMachine := currentMachine.DeepCopy()
				if updatedMachine.Spec.ProviderSpec.Value != nil {
					var providerSpec machinev1beta1.AWSMachineProviderConfig
					if err := json.Unmarshal(updatedMachine.Spec.ProviderSpec.Value.Raw, &providerSpec); err != nil {
						Fail(fmt.Sprintf("Failed to unmarshal provider spec: %v", err))
					}
					// Add a different security group
					providerSpec.SecurityGroups = append(providerSpec.SecurityGroups, machinev1beta1.AWSResourceReference{
						ID: ptr.To("sg-different123"),
					})
					rawProviderSpec, err := json.Marshal(providerSpec)
					if err != nil {
						Fail(fmt.Sprintf("Failed to marshal provider spec: %v", err))
					}
					updatedMachine.Spec.ProviderSpec.Value.Raw = rawProviderSpec
				}
				err = cl.Update(ctx, updatedMachine)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
			})

			It("should prevent modification of Tags in non-authoritative MAPI Machine", func() {
				By("Attempting to modify Tags in MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				updatedMachine := currentMachine.DeepCopy()
				if updatedMachine.Spec.ProviderSpec.Value != nil {
					var providerSpec machinev1beta1.AWSMachineProviderConfig
					if err := json.Unmarshal(updatedMachine.Spec.ProviderSpec.Value.Raw, &providerSpec); err != nil {
						Fail(fmt.Sprintf("Failed to unmarshal provider spec: %v", err))
					}
					// Add a new tag
					providerSpec.Tags = append(providerSpec.Tags, machinev1beta1.TagSpecification{
						Name:  "test-tag",
						Value: "test-value",
					})
					rawProviderSpec, err := json.Marshal(providerSpec)
					if err != nil {
						Fail(fmt.Sprintf("Failed to marshal provider spec: %v", err))
					}
					updatedMachine.Spec.ProviderSpec.Value.Raw = rawProviderSpec
				}
				err = cl.Update(ctx, updatedMachine)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
			})

			It("should prevent modification of capacityReservationId in non-authoritative MAPI Machine", func() {
				By("Attempting to modify capacityReservationId in MAPI Machine")

				currentMachine, err := mapiframework.GetMachine(cl, testMapiMachine.Name)
				if err != nil {
					Fail(fmt.Sprintf("Failed to get current machine: %v", err))
				}
				updatedMachine := currentMachine.DeepCopy()
				if updatedMachine.Spec.ProviderSpec.Value != nil {
					var providerSpec machinev1beta1.AWSMachineProviderConfig
					if err := json.Unmarshal(updatedMachine.Spec.ProviderSpec.Value.Raw, &providerSpec); err != nil {
						Fail(fmt.Sprintf("Failed to unmarshal provider spec: %v", err))
					}
					providerSpec.CapacityReservationID = "cr-different123456789"
					rawProviderSpec, err := json.Marshal(providerSpec)
					if err != nil {
						Fail(fmt.Sprintf("Failed to marshal provider spec: %v", err))
					}
					updatedMachine.Spec.ProviderSpec.Value.Raw = rawProviderSpec
				}
				err = cl.Update(ctx, updatedMachine)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ValidatingAdmissionPolicy 'machine-api-machine-vap' with binding 'machine-api-machine-vap' denied request"))
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
			if err != nil {
				return ""
			}
			return string(capiMachine.Status.Phase)
		case machinev1beta1.MachineAuthorityMachineAPI:
			By("Verify the MAPI Machine is Running")
			mapiMachine, err := mapiframework.GetMachine(cl, machineName)
			if err != nil {
				return ""
			}
			return string(*mapiMachine.Status.Phase)
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

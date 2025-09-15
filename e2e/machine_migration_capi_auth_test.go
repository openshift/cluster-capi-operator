package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Migration CAPI Authoritative Tests", Ordered, func() {
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
		var newMapiMachine *mapiv1beta1.Machine
		var err error

		Context("with spec.authoritativeAPI: ClusterAPI and already existing CAPI Machine with same name", func() {
			BeforeAll(func() {
				newCapiMachine = createCAPIMachine(ctx, cl, mapiMachineAuthCAPIName)
				newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPIName, mapiv1beta1.MachineAuthorityClusterAPI)

				DeferCleanup(func() {
					By("Cleaning up machine resources")
					cleanupMachineResources(
						ctx,
						cl,
						[]*clusterv1.Machine{newCapiMachine},
						[]*mapiv1beta1.Machine{newMapiMachine},
					)
				})
			})

			It("should find MAPI Machine .status.authoritativeAPI to equal ClusterAPI", func() {
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify MAPI Machine Paused condition is True", func() {
				verifyMAPIMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify CAPI Machine Paused condition is False", func() {
				verifyCAPIMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})

		Context("with spec.authoritativeAPI: ClusterAPI and no existing CAPI Machine with same name", func() {
			BeforeAll(func() {
				newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPIName, mapiv1beta1.MachineAuthorityClusterAPI)

				DeferCleanup(func() {
					By("Cleaning up machine resources")
					cleanupMachineResources(
						ctx,
						cl,
						[]*clusterv1.Machine{},
						[]*mapiv1beta1.Machine{newMapiMachine},
					)
				})
			})

			It("should verify CAPI Machine gets created and becomes Running", func() {
				verifyMachineRunning(cl, newMapiMachine.Name, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should find MAPI Machine .status.authoritativeAPI to equal ClusterAPI", func() {
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify MAPI Machine Paused condition is True", func() {
				verifyMAPIMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that the non-authoritative MAPI Machine has an authoritative CAPI Machine mirror", func() {
				Eventually(func() error {
					newCapiMachine, err = capiframework.GetMachine(cl, mapiMachineAuthCAPIName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI Machine should exist")
			})

			It("should verify CAPI Machine Paused condition is False", func() {
				verifyCAPIMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	var _ = Describe("Deleting MAPI/CAPI Machines", Ordered, func() {
		var mapiMachineAuthCAPINameDeletion = "machine-authoritativeapi-capi-deletion"
		var mapiMachineAuthMAPINameDeleteMAPIMachine = "machine-authoritativeapi-mapi-delete-mapi"
		var mapiMachineAuthMAPINameDeleteCAPIMachine = "machine-authoritativeapi-mapi-delete-capi"
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *mapiv1beta1.Machine
		var err error

		Context("with spec.authoritativeAPI: ClusterAPI", func() {
			Context("when deleting the non-authoritative MAPI Machine", func() {
				BeforeAll(func() {
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPINameDeletion, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMachineRunning(cl, newMapiMachine.Name, mapiv1beta1.MachineAuthorityClusterAPI)

					DeferCleanup(func() {
						By("Cleaning up machine resources")
						cleanupMachineResources(
							ctx,
							cl,
							[]*clusterv1.Machine{newCapiMachine},
							[]*mapiv1beta1.Machine{newMapiMachine},
						)
					})
				})
				It("should delete MAPI Machine", func() {
					mapiframework.DeleteMachines(ctx, cl, newMapiMachine)
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
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPINameDeletion, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMachineRunning(cl, newMapiMachine.Name, mapiv1beta1.MachineAuthorityClusterAPI)
					newCapiMachine, err = capiframework.GetMachine(cl, newMapiMachine.Name, capiframework.CAPINamespace)
					Expect(err).NotTo(HaveOccurred(), "Failed to get capi machine")

					DeferCleanup(func() {
						By("Cleaning up machine resources")
						cleanupMachineResources(
							ctx,
							cl,
							[]*clusterv1.Machine{newCapiMachine},
							[]*mapiv1beta1.Machine{newMapiMachine},
						)
					})
				})
				It("should delete CAPI Machine", func() {
					capiframework.DeleteMachines(cl, capiframework.CAPINamespace, newCapiMachine)
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
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthMAPINameDeleteMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
					verifyMachineRunning(cl, newMapiMachine.Name, mapiv1beta1.MachineAuthorityMachineAPI)

					DeferCleanup(func() {
						By("Cleaning up machine resources")
						cleanupMachineResources(
							ctx,
							cl,
							[]*clusterv1.Machine{newCapiMachine},
							[]*mapiv1beta1.Machine{newMapiMachine},
						)
					})
				})
				It("should delete MAPI Machine", func() {
					mapiframework.DeleteMachines(ctx, cl, newMapiMachine)
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
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthMAPINameDeleteCAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
					verifyMachineRunning(cl, newMapiMachine.Name, mapiv1beta1.MachineAuthorityMachineAPI)
					newCapiMachine, err = capiframework.GetMachine(cl, newMapiMachine.Name, capiframework.CAPINamespace)
					Expect(err).NotTo(HaveOccurred(), "Failed to get capi machine")

					DeferCleanup(func() {
						By("Cleaning up machine resources")
						cleanupMachineResources(
							ctx,
							cl,
							[]*clusterv1.Machine{newCapiMachine},
							[]*mapiv1beta1.Machine{newMapiMachine},
						)
					})
				})
				It("should delete CAPI Machine", func() {
					capiframework.DeleteMachines(cl, capiframework.CAPINamespace, newCapiMachine)
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

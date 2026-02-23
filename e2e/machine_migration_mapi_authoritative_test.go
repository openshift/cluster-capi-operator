package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Migration MAPI Authoritative Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Create standalone MAPI Machine", Ordered, func() {
		var mapiMachineAuthMAPIName = "machine-authoritativeapi-mapi"
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *mapiv1beta1.Machine

		Context("With spec.authoritativeAPI: MachineAPI and existing CAPI Machine with that name", func() {
			BeforeAll(func() {
				newCapiMachine = createCAPIMachine(ctx, cl, mapiMachineAuthMAPIName)

				DeferCleanup(func() {
					By("Cleaning up machine resources")
					cleanupMachineResources(
						ctx,
						cl,
						[]*clusterv1.Machine{newCapiMachine},
						[]*mapiv1beta1.Machine{},
					)
				})
			})

			It("should reject creation of MAPI Machine with same name as existing CAPI Machine", func() {
				createSameNameMachineBlockedByVAPAuthMapi(ctx, cl, mapiMachineAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, "a Cluster API Machine with the same name already exists")
			})
		})

		Context("With spec.authoritativeAPI: MachineAPI and no existing CAPI Machine with that name", func() {
			BeforeAll(func() {
				newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI)

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

			It("should find MAPI Machine .status.authoritativeAPI to equal MachineAPI", func() {
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
			It("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
			It("should verify MAPI Machine Paused condition is False", func() {
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
			It("should verify that the MAPI Machine has a CAPI Machine", func() {
				newCapiMachine = capiframework.GetMachine(cl, mapiMachineAuthMAPIName, capiframework.CAPINamespace)
			})
			It("should verify CAPI Machine Paused condition is True", func() {
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})
	})

	var _ = Describe("Deleting MAPI Machines", Ordered, func() {
		var mapiMachineAuthMAPINameDeleteMAPIMachine = "machine-authoritativeapi-mapi-delete-mapi"
		var mapiMachineAuthMAPINameDeleteCAPIMachine = "machine-authoritativeapi-mapi-delete-capi"
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *mapiv1beta1.Machine

		Context("with spec.authoritativeAPI: MachineAPI", func() {
			Context("when deleting the authoritative MAPI Machine", func() {
				BeforeAll(func() {
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthMAPINameDeleteMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
					newCapiMachine = capiframework.GetMachine(cl, newMapiMachine.Name, capiframework.CAPINamespace)
					verifyMachineRunning(cl, newMapiMachine)

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
					verifyResourceRemoved(newCapiMachine)
				})
				It("should verify the AWS machine is deleted", func() {
					verifyResourceRemoved(&awsv1.AWSMachine{
						TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
						ObjectMeta: metav1.ObjectMeta{Name: mapiMachineAuthMAPINameDeleteMAPIMachine, Namespace: capiframework.CAPINamespace},
					})
				})
			})
			Context("when deleting the non-authoritative CAPI Machine", func() {
				BeforeAll(func() {
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthMAPINameDeleteCAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
					verifyMachineRunning(cl, newMapiMachine)
					newCapiMachine = capiframework.GetMachine(cl, newMapiMachine.Name, capiframework.CAPINamespace)

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
					capiframework.DeleteMachines(ctx, cl, capiframework.CAPINamespace, newCapiMachine)
				})

				It("should verify the MAPI machine is deleted", func() {
					verifyResourceRemoved(newMapiMachine)
				})
				It("should verify the AWS machine is deleted", func() {
					verifyResourceRemoved(&awsv1.AWSMachine{
						TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
						ObjectMeta: metav1.ObjectMeta{Name: mapiMachineAuthMAPINameDeleteCAPIMachine, Namespace: capiframework.CAPINamespace},
					})
				})
			})
		})
	})

	var _ = Describe("Machine Migration Round Trip Tests", Ordered, func() {
		var mapiCapiMapiRoundTripName = "machine-mapi-capi-mapi-roundtrip"
		var newMapiMachine *mapiv1beta1.Machine
		var newCapiMachine *clusterv1.Machine

		Context("MAPI -> CAPI -> MAPI round trip", func() {
			BeforeAll(func() {
				By("Creating a MAPI machine with spec.authoritativeAPI: MachineAPI")
				newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiCapiMapiRoundTripName, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineRunning(cl, newMapiMachine)

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

			It("should create a CAPI mirror machine", func() {
				newCapiMachine = capiframework.GetMachine(cl, mapiCapiMapiRoundTripName, capiframework.CAPINamespace)
			})

			It("should set the paused conditions and synchronised generation correctly", func() {
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSynchronizedGeneration(cl, newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should set the paused conditions and synchronised generation correctly after changing spec.authoritativeAPI: ClusterAPI", func() {
				By("Updating spec.authoritativeAPI: ClusterAPI")
				updateMachineAuthoritativeAPI(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineRunning(cl, newCapiMachine)
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSynchronizedGeneration(cl, newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should set the paused conditions and synchronised generation correctly after changing back spec.authoritativeAPI: MachineAPI", func() {
				By("Updating spec.authoritativeAPI: MachineAPI")
				updateMachineAuthoritativeAPI(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineRunning(cl, newMapiMachine)
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSynchronizedGeneration(cl, newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify mirror machines are deleted when deleting MAPI machine", func() {
				By("Deleting MAPI machine")
				mapiframework.DeleteMachines(ctx, cl, newMapiMachine)
				mapiframework.WaitForMachinesDeleted(cl, newMapiMachine)
				verifyResourceRemoved(newMapiMachine)
				verifyResourceRemoved(newCapiMachine)
				verifyResourceRemoved(&awsv1.AWSMachine{
					TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
					ObjectMeta: metav1.ObjectMeta{Name: mapiCapiMapiRoundTripName, Namespace: capiframework.CAPINamespace},
				})
			})
		})
	})
})

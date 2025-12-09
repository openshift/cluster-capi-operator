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

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Migration CAPI Authoritative Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Create MAPI Machine", Ordered, func() {
		var mapiMachineAuthCAPIName = "machine-authoritativeapi-capi"
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *mapiv1beta1.Machine

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
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify MAPI Machine Paused condition is True", func() {
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify CAPI Machine Paused condition is False", func() {
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
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
				newCapiMachine = capiframework.GetMachine(cl, newMapiMachine.Name, capiframework.CAPINamespace)
				verifyMachineRunning(cl, newCapiMachine)
			})

			It("should find MAPI Machine .status.authoritativeAPI to equal ClusterAPI", func() {
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
			It("should verify MAPI Machine Paused condition is True", func() {
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that the non-authoritative MAPI Machine has an authoritative CAPI Machine mirror", func() {
				newCapiMachine = capiframework.GetMachine(cl, mapiMachineAuthCAPIName, capiframework.CAPINamespace)
			})
			It("should verify CAPI Machine Paused condition is False", func() {
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	var _ = Describe("Deleting CAPI Machines", Ordered, func() {
		var mapiMachineAuthCAPINameDeletion = "machine-authoritativeapi-capi-deletion"
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *mapiv1beta1.Machine

		Context("with spec.authoritativeAPI: ClusterAPI", func() {
			Context("when deleting the non-authoritative MAPI Machine", func() {
				BeforeAll(func() {
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPINameDeletion, mapiv1beta1.MachineAuthorityClusterAPI)
					newCapiMachine = capiframework.GetMachine(cl, newMapiMachine.Name, capiframework.CAPINamespace)
					verifyMachineRunning(cl, newCapiMachine)

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
						ObjectMeta: metav1.ObjectMeta{Name: mapiMachineAuthCAPINameDeletion, Namespace: capiframework.CAPINamespace},
					})
				})
			})
			Context("when deleting the authoritative CAPI Machine", func() {
				BeforeAll(func() {
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPINameDeletion, mapiv1beta1.MachineAuthorityClusterAPI)
					newCapiMachine = capiframework.GetMachine(cl, newMapiMachine.Name, capiframework.CAPINamespace)
					verifyMachineRunning(cl, newCapiMachine)

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
						ObjectMeta: metav1.ObjectMeta{Name: mapiMachineAuthCAPINameDeletion, Namespace: capiframework.CAPINamespace},
					})
				})
			})
		})
	})

	var _ = Describe("Machine Migration Round Trip Tests", Ordered, func() {
		var capiMapiCapiRoundTripName = "machine-capi-mapi-capi-roundtrip"
		var newMapiMachine *mapiv1beta1.Machine
		var newCapiMachine *clusterv1.Machine

		Context("CAPI (and no existing CAPI Machine with same name) -> MAPI -> CAPI round trip", func() {
			BeforeAll(func() {
				By("Creating a MAPI machine with spec.authoritativeAPI: ClusterAPI and no existing CAPI Machine with same name")
				newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, capiMapiCapiRoundTripName, mapiv1beta1.MachineAuthorityClusterAPI)
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
				newCapiMachine = capiframework.GetMachine(cl, capiMapiCapiRoundTripName, capiframework.CAPINamespace)
				verifyMachineRunning(cl, newCapiMachine)
			})

			It("should set the paused conditions and synchronised generation correctly", func() {
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSynchronizedGeneration(cl, newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should set the paused conditions and synchronised generation correctly after changing spec.authoritativeAPI: MachineAPI", func() {
				By("Updating spec.authoritativeAPI: MachineAPI")
				updateMachineAuthoritativeAPI(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineRunning(cl, newMapiMachine)
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSynchronizedGeneration(cl, newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should set the paused conditions and synchronised generation correctly after changing back spec.authoritativeAPI: ClusterAPI", func() {
				By("Updating spec.authoritativeAPI: ClusterAPI")
				updateMachineAuthoritativeAPI(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineRunning(cl, newCapiMachine)
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSynchronizedGeneration(cl, newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify mirror machines are deleted when deleting CAPI machine", func() {
				By("Deleting CAPI machine")
				capiframework.DeleteMachines(ctx, cl, capiframework.CAPINamespace, newCapiMachine)
				verifyResourceRemoved(newMapiMachine)
				verifyResourceRemoved(newCapiMachine)
				verifyResourceRemoved(&awsv1.AWSMachine{
					TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
					ObjectMeta: metav1.ObjectMeta{Name: capiMapiCapiRoundTripName, Namespace: capiframework.CAPINamespace},
				})
			})
		})

		//The bug https://issues.redhat.com/browse/OCPBUGS-63183 cause instance leak on AWS so I have to comment all the code out.
		/*
			Context("CAPI (and already existing CAPI Machine with same name) -> MAPI -> CAPI round trip", func() {
				BeforeAll(func() {
					capiMapiCapiRoundTripName = "machine-capi-mapi-capi-roundtrip2"
					By("Creating a MAPI machine with spec.authoritativeAPI: ClusterAPI and already existing CAPI Machine with same name")
					newCapiMachine = createCAPIMachine(ctx, cl, capiMapiCapiRoundTripName)
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, capiMapiCapiRoundTripName, mapiv1beta1.MachineAuthorityClusterAPI)
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

				It("should set the paused conditions and synchronised generation correctly", func() {
					verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMachineSynchronizedGeneration(cl, newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				})

				//MAPI machine phase is null https://issues.redhat.com/browse/OCPBUGS-63183
				PIt("should set the paused conditions and synchronised generation correctly after changing spec.authoritativeAPI: MachineAPI", func() {
					By("Updating spec.authoritativeAPI: MachineAPI")
					updateMachineAuthoritativeAPI(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
					verifyMachineRunning(cl, newMapiMachine)
					verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
					verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
					verifyMachineSynchronizedGeneration(cl, newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
					verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
					verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				})

				It("should set the paused conditions and synchronised generation correctly after changing back spec.authoritativeAPI: ClusterAPI", func() {
					By("Updating spec.authoritativeAPI: ClusterAPI")
					updateMachineAuthoritativeAPI(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMachineRunning(cl, newCapiMachine)
					verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMachineSynchronizedGeneration(cl, newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				})

				It("should verify mirror machines are deleted when deleting CAPI machine", func() {
					By("Deleting CAPI machine")
					capiframework.DeleteMachines(ctx, cl, capiframework.CAPINamespace, newCapiMachine)
					verifyResourceRemoved(newMapiMachine)
					verifyResourceRemoved(newCapiMachine)
					verifyResourceRemoved(&awsv1.AWSMachine{
						TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
						ObjectMeta: metav1.ObjectMeta{Name: capiMapiCapiRoundTripName, Namespace: capiframework.CAPINamespace},
					})
				})
			})*/
	})
})

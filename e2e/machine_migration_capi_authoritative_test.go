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
	var capiMachineAuthCAPISameName *clusterv1.Machine
	var mapiMachineAuthCAPISameName *mapiv1beta1.Machine

	var mapiMachineAuthCAPI *mapiv1beta1.Machine
	var capiMachineMirrorAuthCAPI *clusterv1.Machine

	var mapiMachineDeleteMAPI *mapiv1beta1.Machine
	var capiMachineMirrorDeleteMAPI *clusterv1.Machine

	var mapiMachineDeleteCAPI *mapiv1beta1.Machine
	var capiMachineMirrorDeleteCAPI *clusterv1.Machine

	var mapiMachineRoundTrip *mapiv1beta1.Machine
	var capiMachineMirrorRoundTrip *clusterv1.Machine

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}

		By("Creating CAPI Machine first for same-name scenario")
		capiMachineAuthCAPISameName = createCAPIMachine(ctx, cl, UniqueName("capi-auth-capi"))

		By("Creating MAPI Machine with AuthoritativeAPI: ClusterAPI and existing CAPI Machine with same name")
		mapiMachineAuthCAPISameName = createMAPIMachineWithAuthority(ctx, cl, capiMachineAuthCAPISameName.Name, mapiv1beta1.MachineAuthorityClusterAPI)

		By("Creating MAPI Machines with AuthoritativeAPI: ClusterAPI")
		mapiMachineAuthCAPI = createMAPIMachineWithAuthority(ctx, cl, UniqueName("capi-auth-capi-no-existing"), mapiv1beta1.MachineAuthorityClusterAPI)
		mapiMachineDeleteMAPI = createMAPIMachineWithAuthority(ctx, cl, UniqueName("capi-delete-mapi"), mapiv1beta1.MachineAuthorityClusterAPI)
		mapiMachineDeleteCAPI = createMAPIMachineWithAuthority(ctx, cl, UniqueName("capi-delete-capi"), mapiv1beta1.MachineAuthorityClusterAPI)
		mapiMachineRoundTrip = createMAPIMachineWithAuthority(ctx, cl, UniqueName("capi-roundtrip"), mapiv1beta1.MachineAuthorityClusterAPI)

		By("Getting all CAPI Machine mirrors")
		capiMachineMirrorAuthCAPI = capiframework.GetMachine(cl, mapiMachineAuthCAPI.Name, capiframework.CAPINamespace)
		capiMachineMirrorDeleteMAPI = capiframework.GetMachine(cl, mapiMachineDeleteMAPI.Name, capiframework.CAPINamespace)
		capiMachineMirrorDeleteCAPI = capiframework.GetMachine(cl, mapiMachineDeleteCAPI.Name, capiframework.CAPINamespace)
		capiMachineMirrorRoundTrip = capiframework.GetMachine(cl, mapiMachineRoundTrip.Name, capiframework.CAPINamespace)

		By("Waiting for CAPI Machines to reach Running state")
		verifyMachineRunning(cl, capiMachineAuthCAPISameName)
		verifyMachineRunning(cl, capiMachineMirrorAuthCAPI)
		verifyMachineRunning(cl, capiMachineMirrorDeleteMAPI)
		verifyMachineRunning(cl, capiMachineMirrorDeleteCAPI)
		verifyMachineRunning(cl, capiMachineMirrorRoundTrip)
	})

	var _ = Describe("Create MAPI Machine", Ordered, func() {
		AfterAll(func() {
			By("Cleaning up Create test resources")
			cleanupMachineResources(
				ctx,
				cl,
				[]*clusterv1.Machine{capiMachineAuthCAPISameName, capiMachineMirrorAuthCAPI},
				[]*mapiv1beta1.Machine{mapiMachineAuthCAPISameName, mapiMachineAuthCAPI},
			)
		})

		Context("with spec.authoritativeAPI: ClusterAPI and already existing CAPI Machine with same name", func() {
			It("should find MAPI Machine .status.authoritativeAPI to equal ClusterAPI", func() {
				verifyMachineAuthoritative(mapiMachineAuthCAPISameName, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMAPIMachineSynchronizedCondition(mapiMachineAuthCAPISameName, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify MAPI Machine Paused condition is True", func() {
				verifyMachinePausedCondition(mapiMachineAuthCAPISameName, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify CAPI Machine Paused condition is False", func() {
				verifyMachinePausedCondition(capiMachineAuthCAPISameName, mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})

		Context("with spec.authoritativeAPI: ClusterAPI and no existing CAPI Machine with same name", func() {
			It("should verify CAPI Machine gets created and becomes Running", func() {
				capiMachineMirrorAuthCAPI = capiframework.GetMachine(cl, mapiMachineAuthCAPI.Name, capiframework.CAPINamespace)
				verifyMachineRunning(cl, capiMachineMirrorAuthCAPI)
			})

			It("should find MAPI Machine .status.authoritativeAPI to equal ClusterAPI", func() {
				verifyMachineAuthoritative(mapiMachineAuthCAPI, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMAPIMachineSynchronizedCondition(mapiMachineAuthCAPI, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify MAPI Machine Paused condition is True", func() {
				verifyMachinePausedCondition(mapiMachineAuthCAPI, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that the non-authoritative MAPI Machine has an authoritative CAPI Machine mirror", func() {
				capiMachineMirrorAuthCAPI = capiframework.GetMachine(cl, mapiMachineAuthCAPI.Name, capiframework.CAPINamespace)
			})

			It("should verify CAPI Machine Paused condition is False", func() {
				verifyMachinePausedCondition(capiMachineMirrorAuthCAPI, mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	var _ = Describe("Deleting CAPI Machines", Ordered, func() {
		AfterAll(func() {
			By("Cleaning up Delete test resources")
			cleanupMachineResources(
				ctx,
				cl,
				[]*clusterv1.Machine{capiMachineMirrorDeleteMAPI, capiMachineMirrorDeleteCAPI},
				[]*mapiv1beta1.Machine{mapiMachineDeleteMAPI, mapiMachineDeleteCAPI},
			)
		})

		Context("with spec.authoritativeAPI: ClusterAPI", func() {
			Context("when deleting the non-authoritative MAPI Machine", func() {
				It("should delete MAPI Machine", func() {
					mapiframework.DeleteMachines(ctx, cl, mapiMachineDeleteMAPI)
					mapiframework.WaitForMachinesDeleted(cl, mapiMachineDeleteMAPI)
				})

				It("should verify the CAPI machine is deleted", func() {
					verifyResourceRemoved(capiMachineMirrorDeleteMAPI)
				})

				It("should verify the AWS machine is deleted", func() {
					verifyResourceRemoved(&awsv1.AWSMachine{
						TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
						ObjectMeta: metav1.ObjectMeta{Name: mapiMachineDeleteMAPI.Name, Namespace: capiframework.CAPINamespace},
					})
				})
			})

			Context("when deleting the authoritative CAPI Machine", func() {
				It("should delete CAPI Machine", func() {
					capiframework.DeleteMachines(ctx, cl, capiframework.CAPINamespace, capiMachineMirrorDeleteCAPI)
				})

				It("should verify the MAPI machine is deleted", func() {
					verifyResourceRemoved(mapiMachineDeleteCAPI)
				})

				It("should verify the AWS machine is deleted", func() {
					verifyResourceRemoved(&awsv1.AWSMachine{
						TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
						ObjectMeta: metav1.ObjectMeta{Name: mapiMachineDeleteCAPI.Name, Namespace: capiframework.CAPINamespace},
					})
				})
			})
		})
	})

	var _ = Describe("Machine Migration Round Trip Tests", Ordered, func() {
		AfterAll(func() {
			By("Cleaning up Round Trip test resources")
			cleanupMachineResources(
				ctx,
				cl,
				[]*clusterv1.Machine{capiMachineMirrorRoundTrip},
				[]*mapiv1beta1.Machine{mapiMachineRoundTrip},
			)
		})

		Context("CAPI (and no existing CAPI Machine with same name) -> MAPI -> CAPI round trip", func() {
			It("should create a CAPI mirror machine", func() {
				capiMachineMirrorRoundTrip = capiframework.GetMachine(cl, mapiMachineRoundTrip.Name, capiframework.CAPINamespace)
				verifyMachineRunning(cl, capiMachineMirrorRoundTrip)
			})

			It("should set the paused conditions and synchronised generation correctly", func() {
				verifyMachineAuthoritative(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSynchronizedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSynchronizedGeneration(cl, mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(capiMachineMirrorRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should set the paused conditions and synchronised generation correctly after changing spec.authoritativeAPI: MachineAPI", func() {
				By("Updating spec.authoritativeAPI: MachineAPI")
				updateMachineAuthoritativeAPI(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineRunning(cl, mapiMachineRoundTrip)
				verifyMachineAuthoritative(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSynchronizedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSynchronizedGeneration(cl, mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(capiMachineMirrorRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should set the paused conditions and synchronised generation correctly after changing back spec.authoritativeAPI: ClusterAPI", func() {
				By("Updating spec.authoritativeAPI: ClusterAPI")
				updateMachineAuthoritativeAPI(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineRunning(cl, capiMachineMirrorRoundTrip)
				verifyMachineAuthoritative(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSynchronizedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSynchronizedGeneration(cl, mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(capiMachineMirrorRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify mirror machines are deleted when deleting CAPI machine", func() {
				By("Deleting CAPI machine")
				capiframework.DeleteMachines(ctx, cl, capiframework.CAPINamespace, capiMachineMirrorRoundTrip)
				verifyResourceRemoved(mapiMachineRoundTrip)
				verifyResourceRemoved(capiMachineMirrorRoundTrip)
				verifyResourceRemoved(&awsv1.AWSMachine{
					TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
					ObjectMeta: metav1.ObjectMeta{Name: mapiMachineRoundTrip.Name, Namespace: capiframework.CAPINamespace},
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

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
	var mapiMachineAuthMAPI *mapiv1beta1.Machine
	var capiMachineMirrorAuthMAPI *clusterv1.Machine

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

		By("Creating all MAPI Machines with AuthoritativeAPI: MachineAPI")
		mapiMachineAuthMAPI = createMAPIMachineWithAuthority(ctx, cl, UniqueName("mapi-auth-mapi"), mapiv1beta1.MachineAuthorityMachineAPI)
		mapiMachineDeleteMAPI = createMAPIMachineWithAuthority(ctx, cl, UniqueName("mapi-delete-mapi"), mapiv1beta1.MachineAuthorityMachineAPI)
		mapiMachineDeleteCAPI = createMAPIMachineWithAuthority(ctx, cl, UniqueName("mapi-delete-capi"), mapiv1beta1.MachineAuthorityMachineAPI)
		mapiMachineRoundTrip = createMAPIMachineWithAuthority(ctx, cl, UniqueName("mapi-roundtrip"), mapiv1beta1.MachineAuthorityMachineAPI)

		By("Waiting for all MAPI Machines to reach Running state")
		verifyMachineRunning(cl, mapiMachineAuthMAPI)
		verifyMachineRunning(cl, mapiMachineDeleteMAPI)
		verifyMachineRunning(cl, mapiMachineDeleteCAPI)
		verifyMachineRunning(cl, mapiMachineRoundTrip)

		By("Getting all CAPI Machine mirrors")
		capiMachineMirrorAuthMAPI = capiframework.GetMachine(cl, mapiMachineAuthMAPI.Name, capiframework.CAPINamespace)
		capiMachineMirrorDeleteMAPI = capiframework.GetMachine(cl, mapiMachineDeleteMAPI.Name, capiframework.CAPINamespace)
		capiMachineMirrorDeleteCAPI = capiframework.GetMachine(cl, mapiMachineDeleteCAPI.Name, capiframework.CAPINamespace)
		capiMachineMirrorRoundTrip = capiframework.GetMachine(cl, mapiMachineRoundTrip.Name, capiframework.CAPINamespace)
	})

	var _ = Describe("Create standalone MAPI Machine", Ordered, func() {
		AfterAll(func() {
			By("Cleaning up Create test resources")
			cleanupMachineResources(
				ctx,
				cl,
				[]*clusterv1.Machine{capiMachineMirrorAuthMAPI},
				[]*mapiv1beta1.Machine{mapiMachineAuthMAPI},
			)
		})

		Context("With spec.authoritativeAPI: MachineAPI and no existing CAPI Machine with that name", func() {
			It("should find MAPI Machine .status.authoritativeAPI to equal MachineAPI", func() {
				verifyMachineAuthoritative(mapiMachineAuthMAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMAPIMachineSynchronizedCondition(mapiMachineAuthMAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify MAPI Machine Paused condition is False", func() {
				verifyMachinePausedCondition(mapiMachineAuthMAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify that the MAPI Machine has a CAPI Machine", func() {
				capiMachineMirrorAuthMAPI = capiframework.GetMachine(cl, mapiMachineAuthMAPI.Name, capiframework.CAPINamespace)
			})

			It("should verify CAPI Machine Paused condition is True", func() {
				verifyMachinePausedCondition(capiMachineMirrorAuthMAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})
	})

	var _ = Describe("Deleting MAPI Machines", Ordered, func() {
		AfterAll(func() {
			By("Cleaning up Delete test resources")
			cleanupMachineResources(
				ctx,
				cl,
				[]*clusterv1.Machine{capiMachineMirrorDeleteMAPI, capiMachineMirrorDeleteCAPI},
				[]*mapiv1beta1.Machine{mapiMachineDeleteMAPI, mapiMachineDeleteCAPI},
			)
		})

		Context("with spec.authoritativeAPI: MachineAPI", func() {
			Context("when deleting the authoritative MAPI Machine", func() {
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

			Context("when deleting the non-authoritative CAPI Machine", func() {
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

		Context("MAPI -> CAPI -> MAPI round trip", func() {
			It("should create a CAPI mirror machine", func() {
				capiMachineMirrorRoundTrip = capiframework.GetMachine(cl, mapiMachineRoundTrip.Name, capiframework.CAPINamespace)
			})

			It("should set the paused conditions and synchronised generation correctly", func() {
				verifyMachineAuthoritative(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSynchronizedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSynchronizedGeneration(cl, mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(capiMachineMirrorRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should set the paused conditions and synchronised generation correctly after changing spec.authoritativeAPI: ClusterAPI", func() {
				By("Updating spec.authoritativeAPI: ClusterAPI")
				updateMachineAuthoritativeAPI(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineRunning(cl, capiMachineMirrorRoundTrip)
				verifyMachineAuthoritative(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSynchronizedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSynchronizedGeneration(cl, mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(capiMachineMirrorRoundTrip, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should set the paused conditions and synchronised generation correctly after changing back spec.authoritativeAPI: MachineAPI", func() {
				By("Updating spec.authoritativeAPI: MachineAPI")
				updateMachineAuthoritativeAPI(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineRunning(cl, mapiMachineRoundTrip)
				verifyMachineAuthoritative(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSynchronizedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSynchronizedGeneration(cl, mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(mapiMachineRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(capiMachineMirrorRoundTrip, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify mirror machines are deleted when deleting MAPI machine", func() {
				By("Deleting MAPI machine")
				mapiframework.DeleteMachines(ctx, cl, mapiMachineRoundTrip)
				mapiframework.WaitForMachinesDeleted(cl, mapiMachineRoundTrip)
				verifyResourceRemoved(mapiMachineRoundTrip)
				verifyResourceRemoved(capiMachineMirrorRoundTrip)
				verifyResourceRemoved(&awsv1.AWSMachine{
					TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
					ObjectMeta: metav1.ObjectMeta{Name: mapiMachineRoundTrip.Name, Namespace: capiframework.CAPINamespace},
				})
			})
		})
	})
})

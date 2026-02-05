package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] MachineSet Migration CAPI Authoritative Tests", Ordered, func() {
	var capiMSAuthCAPISameName *clusterv1.MachineSet
	var mapiMSAuthCAPISameName *mapiv1beta1.MachineSet
	var awsMachineTemplateAuthCAPISameName *awsv1.AWSMachineTemplate

	var mapiMSAuthCAPI *mapiv1beta1.MachineSet
	var capiMSMirrorAuthCAPI *clusterv1.MachineSet
	var awsMachineTemplateAuthCAPI *awsv1.AWSMachineTemplate

	var mapiMSScale *mapiv1beta1.MachineSet
	var capiMSMirrorScale *clusterv1.MachineSet
	var awsMachineTemplateScale *awsv1.AWSMachineTemplate
	var firstMAPIMachine *mapiv1beta1.Machine
	var secondMAPIMachine *mapiv1beta1.Machine

	var mapiMSDelete *mapiv1beta1.MachineSet
	var capiMSMirrorDelete *clusterv1.MachineSet
	var awsMachineTemplateDelete *awsv1.AWSMachineTemplate

	var instanceType = "m5.large"

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}

		// Creating all MachineSets without waiting (parallel creation)"
		By("Creating CAPI MachineSet first for existing CAPI MachineSet scenario")
		capiMSAuthCAPISameName = createCAPIMachineSetSkipWait(ctx, cl, 0, UniqueName("capi-machineset-authoritativeapi-capi"), instanceType, true)

		By("Creating MAPI MachineSet with same name as existing CAPI MachineSet")
		mapiMSAuthCAPISameName = createMAPIMachineSetWithAuthoritativeAPISkipWait(
			ctx, cl, 0,
			capiMSAuthCAPISameName.Name,
			mapiv1beta1.MachineAuthorityClusterAPI,
			mapiv1beta1.MachineAuthorityClusterAPI,
			true, // skipWait=true
		)

		By("Creating MAPI MachineSet with AuthoritativeAPI: ClusterAPI and no existing CAPI MachineSet with same name")
		mapiMSAuthCAPI = createMAPIMachineSetWithAuthoritativeAPISkipWait(
			ctx, cl, 0,
			UniqueName("ms-authoritativeapi-capi"),
			mapiv1beta1.MachineAuthorityClusterAPI,
			mapiv1beta1.MachineAuthorityClusterAPI,
			true, // skipWait=true
		)

		By("Creating MachineSet for Scale tests with spec.authoritativeAPI: ClusterAPI")
		mapiMSScale = createMAPIMachineSetWithAuthoritativeAPISkipWait(
			ctx, cl, 1,
			UniqueName("ms-authoritativeapi-capi-scale"),
			mapiv1beta1.MachineAuthorityClusterAPI,
			mapiv1beta1.MachineAuthorityClusterAPI,
			true, // skipWait=true
		)
		By("Creating MachineSet for Delete tests with spec.authoritativeAPI: MachineAPI")
		mapiMSDelete = createMAPIMachineSetWithAuthoritativeAPISkipWait(
			ctx, cl, 1,
			UniqueName("ms-authoritativeapi-mapi-delete"),
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAuthorityMachineAPI,
			true, // skipWait=true
		)

		By("Waiting for all MachineSets to become ready (parallel waiting)")
		capiframework.WaitForMachineSet(cl, capiMSAuthCAPISameName.Name, capiframework.CAPINamespace)
		waitForMAPIMachineSetReady(ctx, cl, mapiMSAuthCAPISameName.Name, mapiv1beta1.MachineAuthorityClusterAPI)
		waitForMAPIMachineSetReady(ctx, cl, mapiMSAuthCAPI.Name, mapiv1beta1.MachineAuthorityClusterAPI)
		waitForMAPIMachineSetReady(ctx, cl, mapiMSScale.Name, mapiv1beta1.MachineAuthorityClusterAPI)
		waitForMAPIMachineSetReady(ctx, cl, mapiMSDelete.Name, mapiv1beta1.MachineAuthorityMachineAPI)

		By("Getting all MachineSet mirrors and templates")
		awsMachineTemplateAuthCAPISameName = waitForAWSMachineTemplate(cl, capiMSAuthCAPISameName.Name)
		capiMSMirrorAuthCAPI = waitForCAPIMachineSetMirror(cl, mapiMSAuthCAPI.Name)
		awsMachineTemplateAuthCAPI = waitForAWSMachineTemplate(cl, mapiMSAuthCAPI.Name)
		capiMSMirrorScale, awsMachineTemplateScale = waitForMAPIMachineSetMirrors(cl, mapiMSScale.Name)
		capiMSMirrorDelete, awsMachineTemplateDelete = waitForMAPIMachineSetMirrors(cl, mapiMSDelete.Name)

		By("Getting Machines from MachineSets")
		mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMSScale)
		Expect(err).ToNot(HaveOccurred(), "Should have successfully got MAPI Machines from MachineSet")
		Expect(mapiMachines).ToNot(BeEmpty(), "Should have found MAPI Machines")

		capiMachines := capiframework.GetMachinesFromMachineSet(cl, capiMSMirrorScale)
		Expect(capiMachines).ToNot(BeEmpty(), "Should have found CAPI Machines")
		Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name), "Should have CAPI Machine name match MAPI Machine name")
		firstMAPIMachine = mapiMachines[0]

		mapiMachinesDelete, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMSDelete)
		Expect(mapiMachinesDelete).ToNot(BeEmpty(), "Should have found MAPI Machines")
		Expect(err).ToNot(HaveOccurred(), "Should have successfully got MAPI Machines from MachineSet")

		capiMachinesDelete := capiframework.GetMachinesFromMachineSet(cl, capiMSMirrorDelete)
		Expect(capiMachinesDelete).ToNot(BeEmpty(), "Should have found CAPI Machines")
		Expect(capiMachinesDelete[0].Name).To(Equal(mapiMachinesDelete[0].Name), "Should have CAPI Machine name match MAPI Machine name")

	})

	var _ = Describe("Create MAPI MachineSets", Ordered, func() {
		AfterAll(func() {
			By("Cleaning up Create test resources")
			cleanupMachineSetTestResources(
				ctx,
				cl,
				[]*clusterv1.MachineSet{capiMSAuthCAPISameName, capiMSMirrorAuthCAPI},
				[]*awsv1.AWSMachineTemplate{awsMachineTemplateAuthCAPISameName, awsMachineTemplateAuthCAPI},
				[]*mapiv1beta1.MachineSet{mapiMSAuthCAPISameName, mapiMSAuthCAPI},
			)
		})

		Context("with spec.authoritativeAPI: ClusterAPI and existing CAPI MachineSet with same name", func() {
			It("should verify that MAPI MachineSet has Paused condition True", func() {
				verifyMachineSetPausedCondition(mapiMSAuthCAPISameName, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-55337
			PIt("should verify that the non-authoritative MAPI MachineSet providerSpec has been updated to reflect the authoritative CAPI MachineSet mirror values", func() {
				verifyMAPIMachineSetProviderSpec(mapiMSAuthCAPISameName, HaveField("InstanceType", Equal(instanceType)))
			})
		})

		Context("with spec.authoritativeAPI: ClusterAPI and no existing CAPI MachineSet with same name", func() {
			It("should find MAPI MachineSet .status.authoritativeAPI to equal CAPI", func() {
				verifyMachineSetAuthoritative(mapiMSAuthCAPI, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that MAPI MachineSet Paused condition is True", func() {
				verifyMachineSetPausedCondition(mapiMSAuthCAPI, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that MAPI MachineSet Synchronized condition is True", func() {
				verifyMAPIMachineSetSynchronizedCondition(mapiMSAuthCAPI, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should verify that the non-authoritative MAPI MachineSet has an authoritative CAPI MachineSet mirror", func() {
				waitForCAPIMachineSetMirror(cl, mapiMSAuthCAPI.Name)
			})

			It("should verify that CAPI MachineSet has Paused condition False", func() {
				verifyMachineSetPausedCondition(capiMSMirrorAuthCAPI, mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	var _ = Describe("Scale MAPI MachineSets", Ordered, func() {
		AfterAll(func() {
			By("Cleaning up Scale test resources")
			cleanupMachineSetTestResources(
				ctx,
				cl,
				[]*clusterv1.MachineSet{capiMSMirrorScale},
				[]*awsv1.AWSMachineTemplate{awsMachineTemplateScale},
				[]*mapiv1beta1.MachineSet{mapiMSScale},
			)
		})

		Context("with spec.authoritativeAPI: ClusterAPI", Ordered, func() {
			It("should succeed scaling CAPI MachineSet to 2 replicas", func() {
				By("Scaling up CAPI MachineSet to 2 replicas")
				capiframework.ScaleCAPIMachineSet(mapiMSScale.Name, 2, capiframework.CAPINamespace)

				By("Verifying MachineSet status.replicas is set to 2")
				verifyMachinesetReplicas(capiMSMirrorScale, 2)
				verifyMachinesetReplicas(mapiMSScale, 2)

				By("Verifying a new CAPI Machine is created and Paused condition is False")
				Eventually(func(g Gomega) {
					capiMSMirrorScale = capiframework.GetMachineSet(cl, mapiMSScale.Name, capiframework.CAPINamespace)
					g.Expect(capiMSMirrorScale).NotTo(BeNil(), "Should have found CAPI MachineSet")
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should have refreshed CAPI MachineSet")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMSMirrorScale)
				verifyMachineRunning(cl, capiMachine)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				var err error
				Eventually(func(g Gomega) {
					secondMAPIMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMSScale)
					g.Expect(err).ToNot(HaveOccurred(), "Should have successfully got MAPI Machines from MachineSet")
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have found latest MAPI Machine from MachineSet")
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(secondMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should succeed switching MachineSet's AuthoritativeAPI to MachineAPI", func() {
				switchMachineSetAuthoritativeAPI(mapiMSScale, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetPausedCondition(mapiMSScale, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetPausedCondition(capiMSMirrorScale, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSetSynchronizedCondition(mapiMSScale, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should succeed scaling up MAPI MachineSet to 3, after switching AuthoritativeAPI to MachineAPI", func() {
				By("Scaling up MAPI MachineSet to 3 replicas")
				Eventually(func() error {
					return mapiframework.ScaleMachineSet(mapiMSScale.Name, 3)
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should be able to scale up MAPI MachineSet")

				By("Verifying MachineSet status.replicas is set to 3")
				verifyMachinesetReplicas(mapiMSScale, 3)
				verifyMachinesetReplicas(capiMSMirrorScale, 3)

				By("Verifying the newly requested MAPI Machine has been created and its status.authoritativeAPI is MachineAPI and its Paused condition is False")
				var mapiMachine *mapiv1beta1.Machine
				var err error
				Eventually(func(g Gomega) {
					mapiMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMSScale)
					g.Expect(err).ToNot(HaveOccurred(), "Should have successfully got MAPI Machines from MachineSet")
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have found latest MAPI Machine from MachineSet")
				verifyMachineRunning(cl, mapiMachine)
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying there is a non-authoritative, paused CAPI Machine mirror for the new MAPI Machine")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMSMirrorScale)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying old Machines still exist and authority on them is still ClusterAPI")
				verifyMachineAuthoritative(firstMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should succeed scaling down MAPI MachineSet to 1, after the switch of AuthoritativeAPI to MachineAPI", func() {
				By("Scaling down MAPI MachineSet to 1 replicas")
				Eventually(func() error {
					return mapiframework.ScaleMachineSet(mapiMSScale.Name, 1)
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should be able to scale down MAPI MachineSet")
				verifyMachinesetReplicas(mapiMSScale, 1)
				verifyMachinesetReplicas(capiMSMirrorScale, 1)
			})

			It("should succeed switching back MachineSet's AuthoritativeAPI to ClusterAPI, after the initial switch to AuthoritativeAPI: MachineAPI", func() {
				switchMachineSetAuthoritativeAPI(mapiMSScale, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetPausedCondition(mapiMSScale, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetPausedCondition(capiMSMirrorScale, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSetSynchronizedCondition(mapiMSScale, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting CAPI MachineSet", func() {
				capiframework.DeleteMachineSets(ctx, cl, capiMSMirrorScale)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMSScale)
				capiframework.WaitForMachineSetsDeleted(cl, capiMSMirrorScale)
				verifyResourceRemoved(awsMachineTemplateScale)
			})
		})
	})

	var _ = Describe("Delete MachineSets", Ordered, func() {
		AfterAll(func() {
			By("Cleaning up Delete test resources")
			cleanupMachineSetTestResources(
				ctx,
				cl,
				[]*clusterv1.MachineSet{capiMSMirrorDelete},
				[]*awsv1.AWSMachineTemplate{awsMachineTemplateDelete},
				[]*mapiv1beta1.MachineSet{mapiMSDelete},
			)
		})

		Context("when removing non-authoritative MAPI MachineSet", Ordered, func() {
			It("shouldn't delete its authoritative CAPI MachineSet", func() {
				By("Switching AuthoritativeAPI to ClusterAPI")
				switchMachineSetAuthoritativeAPI(mapiMSDelete, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)

				// TODO(OCPBUGS-74571): this extra verification step is a workaround as a stop-gap until
				// remove this once https://issues.redhat.com/browse/OCPBUGS-74571 is fixed.
				By("Verifying MAPI MachineSet is paused and CAPI MachineSet is unpaused after switch")
				verifyMachineSetPausedCondition(mapiMSDelete, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetPausedCondition(capiMSMirrorDelete, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Scaling up CAPI MachineSet to 2 replicas")
				capiframework.ScaleCAPIMachineSet(mapiMSDelete.GetName(), 2, capiframework.CAPINamespace)

				By("Verifying MachineSet status.replicas is set to 2")
				verifyMachinesetReplicas(capiMSMirrorDelete, 2)
				verifyMachinesetReplicas(mapiMSDelete, 2)

				By("Verifying new CAPI Machine is running")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMSMirrorDelete)
				verifyMachineRunning(cl, capiMachine)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				var mapiMachine *mapiv1beta1.Machine
				var err error
				Eventually(func(g Gomega) {
					mapiMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMSDelete)
					g.Expect(err).ToNot(HaveOccurred(), "Should have successfully got MAPI Machines from MachineSet")
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have found latest MAPI Machine from MachineSet")
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Deleting non-authoritative MAPI MachineSet")
				Eventually(func(g Gomega) {
					mapiMSDelete, err = mapiframework.GetMachineSet(ctx, cl, mapiMSDelete.Name)
					g.Expect(err).ToNot(HaveOccurred(), "Should have successfully got mapiMachineSet")
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should have refreshed mapiMachineSet")
				mapiframework.DeleteMachineSets(cl, mapiMSDelete)

				By("Verifying CAPI MachineSet not removed, both MAPI Machines and Mirrors remain")
				// TODO: Add full verification once OCPBUGS-56897 is fixed
				Eventually(func(g Gomega) {
					capiMSMirrorDelete = capiframework.GetMachineSet(cl, mapiMSDelete.Name, capiframework.CAPINamespace)
					g.Expect(capiMSMirrorDelete).ToNot(BeNil(), "Should have found CAPI MachineSet after deleting non-authoritative MAPI MachineSet")
					g.Expect(capiMSMirrorDelete.DeletionTimestamp.IsZero()).To(BeTrue(), "Should have CAPI MachineSet not marked for deletion")
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should have verified CAPI MachineSet still exists")
			})
		})
	})
})

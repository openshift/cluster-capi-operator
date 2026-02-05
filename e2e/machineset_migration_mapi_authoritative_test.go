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
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] MachineSet Migration MAPI Authoritative Tests", Ordered, func() {
	var k komega.Komega

	var mapiMSAuthMAPI *mapiv1beta1.MachineSet
	var capiMSMirrorAuthMAPI *clusterv1.MachineSet
	var awsMachineTemplateAuthMAPI *awsv1.AWSMachineTemplate

	var capiMSAuthMAPISameName *clusterv1.MachineSet
	var awsMachineTemplateAuthMAPISameName *awsv1.AWSMachineTemplate

	var mapiMSScale *mapiv1beta1.MachineSet
	var capiMSMirrorScale *clusterv1.MachineSet
	var awsMachineTemplateScale *awsv1.AWSMachineTemplate
	var firstMAPIMachine *mapiv1beta1.Machine
	var secondMAPIMachine *mapiv1beta1.Machine

	var mapiMSScaleCAPI *mapiv1beta1.MachineSet
	var capiMSMirrorScaleCAPI *clusterv1.MachineSet
	var awsMachineTemplateScaleCAPI *awsv1.AWSMachineTemplate

	var mapiMSUpdate *mapiv1beta1.MachineSet
	var capiMSMirrorUpdate *clusterv1.MachineSet
	var awsMachineTemplateUpdate *awsv1.AWSMachineTemplate
	var newAWSMachineTemplateUpdate *awsv1.AWSMachineTemplate

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}

		k = komega.New(cl)

		// Creating all MachineSets without waiting (parallel creation)"
		By("Creating MachineSets for Create tests")
		mapiMSAuthMAPI = createMAPIMachineSetWithAuthoritativeAPISkipWait(
			ctx, cl, 0,
			UniqueName("ms-authoritativeapi-mapi"),
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAuthorityMachineAPI,
			true, // skipWait=true
		)

		By("Creating CAPI MachineSet first for existing CAPI MachineSet scenario")
		capiMSAuthMAPISameName = createCAPIMachineSetSkipWait(ctx, cl, 0, UniqueName("capi-machineset-authoritativeapi-mapi"), "", true)

		By("Creating MachineSet for Scale tests with spec.authoritativeAPI: MachineAPI")
		mapiMSScale = createMAPIMachineSetWithAuthoritativeAPISkipWait(
			ctx, cl, 1,
			UniqueName("ms-authoritativeapi-mapi-scale"),
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAuthorityMachineAPI,
			true, // skipWait=true
		)
		mapiMSScaleCAPI = createMAPIMachineSetWithAuthoritativeAPISkipWait(
			ctx, cl, 0,
			UniqueName("ms-mapi-machine-capi-scale"),
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAuthorityClusterAPI,
			true, // skipWait=true
		)

		By("Creating MachineSet for Update tests with spec.authoritativeAPI: MachineAPI")
		mapiMSUpdate = createMAPIMachineSetWithAuthoritativeAPISkipWait(
			ctx, cl, 0,
			UniqueName("ms-authoritativeapi-mapi-update"),
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAuthorityMachineAPI,
			true, // skipWait=true
		)

		By("Waiting for all MachineSets to become ready (parallel waiting)")
		waitForMAPIMachineSetReady(ctx, cl, mapiMSAuthMAPI.Name, mapiv1beta1.MachineAuthorityMachineAPI)
		capiframework.WaitForMachineSet(cl, capiMSAuthMAPISameName.Name, capiframework.CAPINamespace)
		waitForMAPIMachineSetReady(ctx, cl, mapiMSScale.Name, mapiv1beta1.MachineAuthorityMachineAPI)
		waitForMAPIMachineSetReady(ctx, cl, mapiMSScaleCAPI.Name, mapiv1beta1.MachineAuthorityMachineAPI)
		waitForMAPIMachineSetReady(ctx, cl, mapiMSUpdate.Name, mapiv1beta1.MachineAuthorityMachineAPI)

		By("Getting all MachineSet mirrors and templates")
		capiMSMirrorAuthMAPI, awsMachineTemplateAuthMAPI = waitForMAPIMachineSetMirrors(cl, mapiMSAuthMAPI.Name)
		awsMachineTemplateAuthMAPISameName = waitForAWSMachineTemplate(cl, capiMSAuthMAPISameName.Name)
		capiMSMirrorScale, awsMachineTemplateScale = waitForMAPIMachineSetMirrors(cl, mapiMSScale.Name)
		capiMSMirrorScaleCAPI, awsMachineTemplateScaleCAPI = waitForMAPIMachineSetMirrors(cl, mapiMSScaleCAPI.Name)
		capiMSMirrorUpdate, awsMachineTemplateUpdate = waitForMAPIMachineSetMirrors(cl, mapiMSUpdate.Name)

		By("Getting Machines from Scale MachineSet")
		mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMSScale)
		Expect(err).ToNot(HaveOccurred(), "Should have successfully got MAPI Machines from MachineSet")
		Expect(mapiMachines).ToNot(BeEmpty(), "Should have found MAPI Machines")

		capiMachines := capiframework.GetMachinesFromMachineSet(cl, capiMSMirrorScale)
		Expect(capiMachines).ToNot(BeEmpty(), "Should have found CAPI Machines")
		Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name), "Should have CAPI Machine name match MAPI Machine name")
		firstMAPIMachine = mapiMachines[0]
	})

	var _ = Describe("Create MAPI MachineSets", Ordered, func() {
		AfterAll(func() {
			By("Cleaning up Create test resources")
			cleanupMachineSetTestResources(
				ctx,
				cl,
				[]*clusterv1.MachineSet{capiMSMirrorAuthMAPI, capiMSAuthMAPISameName},
				[]*awsv1.AWSMachineTemplate{awsMachineTemplateAuthMAPI, awsMachineTemplateAuthMAPISameName},
				[]*mapiv1beta1.MachineSet{mapiMSAuthMAPI},
			)
		})

		Context("with spec.authoritativeAPI: MachineAPI and existing CAPI MachineSet with same name", func() {
			// https://issues.redhat.com/browse/OCPCLOUD-3188
			PIt("should reject creation of MAPI MachineSet with same name as existing CAPI MachineSet", func() {
				By("Creating a same name MAPI MachineSet")
				createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, capiMSAuthMAPISameName.Name, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})

		Context("with spec.authoritativeAPI: MachineAPI and when no existing CAPI MachineSet with same name", func() {
			It("should find MAPI MachineSet .status.authoritativeAPI to equal MAPI", func() {
				verifyMachineSetAuthoritative(mapiMSAuthMAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify that MAPI MachineSet Paused condition is False", func() {
				verifyMachineSetPausedCondition(mapiMSAuthMAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify that MAPI MachineSet Synchronized condition is True", func() {
				verifyMAPIMachineSetSynchronizedCondition(mapiMSAuthMAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should find that MAPI MachineSet has a CAPI MachineSet mirror", func() {
				waitForCAPIMachineSetMirror(cl, mapiMSAuthMAPI.Name)
			})

			It("should verify that the mirror CAPI MachineSet has Paused condition True", func() {
				verifyMachineSetPausedCondition(capiMSMirrorAuthMAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})
	})

	var _ = Describe("Scale MAPI MachineSets", Ordered, func() {
		AfterAll(func() {
			By("Cleaning up Scale test resources")
			cleanupMachineSetTestResources(
				ctx,
				cl,
				[]*clusterv1.MachineSet{capiMSMirrorScale, capiMSMirrorScaleCAPI},
				[]*awsv1.AWSMachineTemplate{awsMachineTemplateScale, awsMachineTemplateScaleCAPI},
				[]*mapiv1beta1.MachineSet{mapiMSScale, mapiMSScaleCAPI},
			)
		})

		Context("with spec.authoritativeAPI: MachineAPI", Ordered, func() {
			It("should be able scale MAPI MachineSet to 2 replicas successfully", func() {
				By("Scaling up MAPI MachineSet to 2 replicas")
				Eventually(func() error {
					return mapiframework.ScaleMachineSet(mapiMSScale.GetName(), 2)
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should be able to scale up MAPI MachineSet")
				mapiframework.WaitForMachineSet(ctx, cl, mapiMSScale.Name)
				verifyMachinesetReplicas(mapiMSScale, 2)
				verifyMachinesetReplicas(capiMSMirrorScale, 2)

				By("Verifying a new MAPI Machine is created and Paused condition is False")
				var err error
				Eventually(func(g Gomega) {
					secondMAPIMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMSScale)
					g.Expect(err).ToNot(HaveOccurred(), "Should have successfully got MAPI Machines from MachineSet")
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have found latest MAPI Machine from MachineSet")
				verifyMachineRunning(cl, secondMAPIMachine)
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(secondMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying there is a non-authoritative CAPI Machine mirror for the MAPI Machine and its Paused condition is True")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMSMirrorScale)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying CAPI MachineSet status.replicas is set to 2")
				verifyMachinesetReplicas(capiMSMirrorScale, 2)
			})

			It("should succeed switching MAPI MachineSet AuthoritativeAPI to ClusterAPI", func() {
				switchMachineSetAuthoritativeAPI(mapiMSScale, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetPausedCondition(mapiMSScale, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetPausedCondition(capiMSMirrorScale, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSetSynchronizedCondition(mapiMSScale, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should succeed scaling up CAPI MachineSet to 3, after the switch of AuthoritativeAPI to ClusterAPI", func() {
				By("Scaling up CAPI MachineSet to 3")
				capiframework.ScaleCAPIMachineSet(mapiMSScale.Name, 3, capiframework.CAPINamespace)

				By("Verifying MachineSet status.replicas is set to 3")
				verifyMachinesetReplicas(capiMSMirrorScale, 3)
				verifyMachinesetReplicas(mapiMSScale, 3)

				By("Verifying a new CAPI Machine is running and Paused condition is False")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMSMirrorScale)
				verifyMachineRunning(cl, capiMachine)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				var err error
				var mapiMachine *mapiv1beta1.Machine
				Eventually(func(g Gomega) {
					mapiMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMSScale)
					g.Expect(err).ToNot(HaveOccurred(), "Should have successfully got MAPI Machines from MachineSet")
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have found latest MAPI Machine from MachineSet")
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying old Machines still exist and authority on them is still MachineAPI")
				verifyMachineAuthoritative(firstMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should succeed scaling down CAPI MachineSet to 1, after the switch of AuthoritativeAPI to ClusterAPI", func() {
				By("Scaling down CAPI MachineSet to 1")
				capiframework.ScaleCAPIMachineSet(mapiMSScale.Name, 1, capiframework.CAPINamespace)

				By("Verifying both CAPI MachineSet and its MAPI MachineSet mirror are scaled down to 1")
				verifyMachinesetReplicas(capiMSMirrorScale, 1)
				verifyMachinesetReplicas(mapiMSScale, 1)
			})

			It("should succeed in switching back the AuthoritativeAPI to MachineAPI after the initial switch to ClusterAPI", func() {
				switchMachineSetAuthoritativeAPI(mapiMSScale, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetPausedCondition(mapiMSScale, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetPausedCondition(capiMSMirrorScale, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSetSynchronizedCondition(mapiMSScale, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting MAPI MachineSet", func() {
				Eventually(func() error {
					return mapiframework.DeleteMachineSets(cl, mapiMSScale)
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should be able to delete test MachineSet")
				capiframework.WaitForMachineSetsDeleted(cl, capiMSMirrorScale)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMSScale)
				verifyResourceRemoved(awsMachineTemplateScale)
			})
		})

		Context("with spec.authoritativeAPI: MachineAPI, spec.template.spec.authoritativeAPI: ClusterAPI", Ordered, func() {
			It("should create an authoritative CAPI Machine when scaling MAPI MachineSet to 1 replicas", func() {
				By("Scaling up MAPI MachineSet to 1 replicas")
				Eventually(func() error {
					return mapiframework.ScaleMachineSet(mapiMSScaleCAPI.GetName(), 1)
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should be able to scale up MAPI MachineSet")
				capiframework.WaitForMachineSet(cl, mapiMSScaleCAPI.Name, capiframework.CAPINamespace)
				verifyMachinesetReplicas(mapiMSScaleCAPI, 1)
				verifyMachinesetReplicas(capiMSMirrorScaleCAPI, 1)

				By("Verifying MAPI Machine is created and .status.authoritativeAPI to equal CAPI")
				var mapiMachine *mapiv1beta1.Machine
				var err error
				Eventually(func(g Gomega) {
					mapiMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMSScaleCAPI)
					g.Expect(err).ToNot(HaveOccurred(), "Should have successfully got MAPI Machines from MachineSet")
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have found latest MAPI Machine from MachineSet")
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying CAPI Machine is created and Paused condition is False and provisions a running Machine")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMSMirrorScaleCAPI)
				verifyMachineRunning(cl, capiMachine)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting MAPI MachineSet", func() {
				Eventually(func() error {
					return mapiframework.DeleteMachineSets(cl, mapiMSScaleCAPI)
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should be able to delete test MachineSet")
				capiframework.WaitForMachineSetsDeleted(cl, capiMSMirrorScaleCAPI)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMSScaleCAPI)
				verifyResourceRemoved(awsMachineTemplateScaleCAPI)
			})
		})
	})

	var _ = Describe("Update MachineSets", Ordered, func() {
		AfterAll(func() {
			By("Cleaning up Update test resources")
			cleanupMachineSetTestResources(
				ctx,
				cl,
				[]*clusterv1.MachineSet{capiMSMirrorUpdate},
				[]*awsv1.AWSMachineTemplate{awsMachineTemplateUpdate, newAWSMachineTemplateUpdate},
				[]*mapiv1beta1.MachineSet{mapiMSUpdate},
			)
		})

		Context("when MAPI MachineSet with spec.authoritativeAPI: MachineAPI and replicas 0", Ordered, func() {
			It("should reject update when attempting scaling of the CAPI MachineSet mirror", func() {
				By("Scaling up CAPI MachineSet to 1 should be rejected")
				capiframework.ScaleCAPIMachineSet(mapiMSUpdate.Name, 1, capiframework.CAPINamespace)
				Eventually(func(g Gomega) {
					capiMSMirrorUpdate = capiframework.GetMachineSet(cl, mapiMSUpdate.Name, capiframework.CAPINamespace)
					g.Expect(capiMSMirrorUpdate).NotTo(BeNil(), "Should have found CAPI MachineSet")
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should have found CAPI MachineSet")
				verifyMachinesetReplicas(capiMSMirrorUpdate, 0)
			})

			It("should reject update when attempting to change the spec of the CAPI MachineSet mirror", func() {
				By("Updating CAPI mirror spec (such as Deletion.Order)")
				Eventually(k.Update(capiMSMirrorUpdate, func() {
					capiMSMirrorUpdate.Spec.Deletion = clusterv1.MachineSetDeletionSpec{
						Order: clusterv1.OldestMachineSetDeletionOrder,
					}
				}), capiframework.WaitMedium, capiframework.RetryShort).Should(Succeed(), "Should have successfully updated CAPI MachineSet Deletion.Order")

				By("Verifying both MAPI and CAPI MachineSet spec value are restored to original value")
				Eventually(k.Object(mapiMSUpdate), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Spec.DeletePolicy", SatisfyAny(BeEmpty(), Equal("Random"))),
					"Should have DeletePolicy be either empty or 'Random'",
				)
				Eventually(k.Object(capiMSMirrorUpdate), capiframework.WaitShort, capiframework.RetryShort).Should(
					HaveField("Spec.Deletion.Order", Equal(clusterv1.RandomMachineSetDeletionOrder)),
					"Should have Deletion.Order be 'Random'",
				)
			})

			It("should create a new InfraTemplate when update MAPI MachineSet providerSpec", func() {
				By("Updating MAPI MachineSet providerSpec InstanceType to m5.large")
				newInstanceType := "m5.large"
				updateAWSMachineSetProviderSpec(ctx, cl, mapiMSUpdate, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					providerSpec.InstanceType = newInstanceType
				})

				By("Waiting for new InfraTemplate to be created")
				originalAWSMachineTemplateName := capiMSMirrorUpdate.Spec.Template.Spec.InfrastructureRef.Name
				Eventually(func(g Gomega) {
					capiMSMirrorUpdate = capiframework.GetMachineSet(cl, mapiMSUpdate.Name, capiframework.CAPINamespace)
					g.Expect(capiMSMirrorUpdate).NotTo(BeNil(), "Should have found CAPI MachineSet")
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should have refreshed CAPI MachineSet")
				Eventually(k.Object(capiMSMirrorUpdate), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Spec.Template.Spec.InfrastructureRef.Name", Not(Equal(originalAWSMachineTemplateName))),
					"Should have InfraTemplate name changed",
				)

				By("Verifying new InfraTemplate has the updated InstanceType")
				var err error
				Eventually(func(g Gomega) {
					newAWSMachineTemplateUpdate, err = capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSUpdate.Name, capiframework.CAPINamespace)
					g.Expect(err).ToNot(HaveOccurred(), "Should have successfully got new awsMachineTemplate")
					g.Expect(newAWSMachineTemplateUpdate.Spec.Template.Spec.InstanceType).To(Equal(newInstanceType), "Should have new awsMachineTemplate with updated InstanceType")
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "Should have found new awsMachineTemplate %s", newAWSMachineTemplateUpdate)

				By("Verifying the old InfraTemplate is deleted")
				verifyResourceRemoved(awsMachineTemplateUpdate)
			})
		})

		Context("when switching MAPI MachineSet spec.authoritativeAPI to ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				switchMachineSetAuthoritativeAPI(mapiMSUpdate, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSetSynchronizedCondition(mapiMSUpdate, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should be rejected when scaling MAPI MachineSet", func() {
				By("Scaling up MAPI MachineSet to 1")
				Eventually(func() error {
					return mapiframework.ScaleMachineSet(mapiMSUpdate.Name, 1)
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should allow scaling MAPI MachineSet")

				By("Verifying MAPI MachineSet replicas is restored to original value 0")
				verifyMachinesetReplicas(mapiMSUpdate, 0)
				verifyMachinesetReplicas(capiMSMirrorUpdate, 0)
			})

			It("should be rejected when when updating providerSpec of MAPI MachineSet", func() {
				By("Getting the current MAPI MachineSet providerSpec InstanceType")
				originalSpec := getAWSProviderSpecFromMachineSet(mapiMSUpdate)

				By("Updating the MAPI MachineSet providerSpec InstanceType")
				updateAWSMachineSetProviderSpec(ctx, cl, mapiMSUpdate, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					providerSpec.InstanceType = "m5.xlarge"
				})

				By("Verifying MAPI MachineSet instanceType is restored to original value")
				verifyMAPIMachineSetProviderSpec(mapiMSUpdate, HaveField("InstanceType", Equal(originalSpec.InstanceType)))
			})

			It("should update MAPI MachineSet and remove old InfraTemplate when CAPI MachineSet points to new InfraTemplate", func() {
				By("Creating a new awsMachineTemplate with different spec")
				newInstanceType := "m6.xlarge"
				originalAWSMachineTemplateName := capiMSMirrorUpdate.Spec.Template.Spec.InfrastructureRef.Name
				newAWSMachineTemplateUpdate = createAWSMachineTemplate(ctx, cl, originalAWSMachineTemplateName, func(spec *awsv1.AWSMachineSpec) {
					spec.InstanceType = newInstanceType
				})

				By("Updating CAPI MachineSet to point to the new InfraTemplate")
				updateCAPIMachineSetInfraTemplate(capiMSMirrorUpdate, newAWSMachineTemplateUpdate.Name)

				By("Verifying the MAPI MachineSet is updated to reflect the new template")
				var err error
				Eventually(func(g Gomega) {
					mapiMSUpdate, err = mapiframework.GetMachineSet(ctx, cl, mapiMSUpdate.Name)
					g.Expect(err).ToNot(HaveOccurred(), "Should have successfully refreshed MAPI MachineSet")
				}, capiframework.WaitShort, capiframework.RetryShort).Should(Succeed(), "Should have refreshed MAPI MachineSet")
				Eventually(k.Object(mapiMSUpdate), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Spec.Template.Spec.ProviderSpec.Value.Raw", ContainSubstring(newInstanceType)),
					"Should have MAPI MachineSet providerSpec updated to reflect the new InfraTemplate with InstanceType %s", newInstanceType,
				)
			})
		})
	})
})

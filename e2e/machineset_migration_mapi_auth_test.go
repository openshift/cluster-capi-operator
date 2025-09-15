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
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] MachineSet Migration MAPI Authoritative Tests", Ordered, func() {
	var k komega.Komega

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this only support on aws", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}

		k = komega.New(cl)
	})

	var _ = Describe("Create MAPI MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var existingCAPIMSAuthorityMAPIName = "capi-machineset-authoritativeapi-mapi"

		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet

		Context("with spec.authoritativeAPI: MachineAPI and existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				capiMachineSet = createCAPIMachineSet(ctx, cl, 0, existingCAPIMSAuthorityMAPIName, "")
				awsMachineTemplate = waitForAWSMachineTemplate(cl, existingCAPIMSAuthorityMAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI and existing CAPI MachineSet with same name' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{},
					)
				})
			})

			// https://issues.redhat.com/browse/OCPCLOUD-2641
			PIt("should reject creation of MAPI MachineSet with same name as existing CAPI MachineSet", func() {
				By("Creating a same name MAPI MachineSet")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, existingCAPIMSAuthorityMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})

		Context("with spec.authoritativeAPI: MachineAPI and when no existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				capiMachineSet = waitForCAPIMachineSetMirror(cl, mapiMSAuthMAPIName)
				awsMachineTemplate = waitForAWSMachineTemplate(cl, mapiMSAuthMAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI and when no existing CAPI MachineSet with same name' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should find MAPI MachineSet .status.authoritativeAPI to equal MAPI", func() {
				verifyMachineSetAuthoritative(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify that MAPI MachineSet Paused condition is False", func() {
				verifyMAPIMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should verify that MAPI MachineSet Synchronized condition is True", func() {
				verifyMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should find that MAPI MachineSet has a CAPI MachineSet mirror", func() {
				waitForCAPIMachineSetMirror(cl, mapiMSAuthMAPIName)
			})

			It("should verify that the mirror CAPI MachineSet has Paused condition True", func() {
				verifyCAPIMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})
	})

	var _ = Describe("Scale MAPI MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMSAuthMAPICAPI = "ms-mapi-machine-capi"

		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet
		var firstMAPIMachine *mapiv1beta1.Machine
		var secondMAPIMachine *mapiv1beta1.Machine

		Context("with spec.authoritativeAPI: MachineAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPIName)

				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				capiMachines := capiframework.GetMachinesFromMachineSet(cl, capiMachineSet)
				Expect(mapiMachines).ToNot(BeEmpty(), "no MAPI Machines found")
				Expect(capiMachines).ToNot(BeEmpty(), "no CAPI Machines found")
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))
				firstMAPIMachine = mapiMachines[0]

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should be able scale MAPI MachineSet to 2 replicas successfully", func() {
				By("Scaling up MAPI MachineSet to 2 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 2)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				mapiframework.WaitForMachineSet(ctx, cl, mapiMSAuthMAPIName)
				verifyMAPIMachinesetReplicas(mapiMachineSet, 2)

				By("Verifying a new MAPI Machine is created and Paused condition is False")
				var err error
				secondMAPIMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineRunning(cl, secondMAPIMachine.Name, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachinePausedCondition(secondMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying there is a non-authoritative CAPI Machine mirror for the MAPI Machine and its Paused condition is True")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyCAPIMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should succeed switching MAPI MachineSet AuthoritativeAPI to ClusterAPI", func() {
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyCAPIMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should succeed scaling up CAPI MachineSet to 3, after the switch of AuthoritativeAPI to ClusterAPI", func() {
				By("Scaling up CAPI MachineSet to 3")
				capiframework.ScaleMachineSet(mapiMSAuthMAPIName, 3, capiframework.CAPINamespace)

				By("Verifying a new CAPI Machine is running and Paused condition is False")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyMachineRunning(cl, capiMachine.Name, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyCAPIMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying old Machines still exist and authority on them is still MachineAPI")
				verifyMachineAuthoritative(firstMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should succeed scaling down CAPI MachineSet to 1, after the switch of AuthoritativeAPI to ClusterAPI", func() {
				By("Scaling up CAPI MachineSet to 1")
				capiframework.ScaleMachineSet(mapiMSAuthMAPIName, 1, capiframework.CAPINamespace)

				By("Verifying both CAPI MachineSet and its MAPI MachineSet mirror are scaled down to 1")
				// TODO: Uncomment once https://github.com/openshift/cluster-capi-operator/pull/329 is merged
				// verifyCAPIMachinesetReplicas(capiMachineSet, 1)
				// verifyMAPIMachinesetReplicas(mapiMachineSet, 1)
			})

			It("should succeed in switching back the AuthoritativeAPI to MachineAPI after the initial switch to ClusterAPI", func() {
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyCAPIMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting MAPI MachineSet", func() {
				Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed(), "Should be able to delete test MachineSet")
				capiframework.WaitForMachineSetsDeleted(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				verifyAWSMachineTemplateDeleted(awsMachineTemplate.Name)
			})
		})

		Context("with spec.authoritativeAPI: MachineAPI, spec.template.spec.authoritativeAPI: ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPICAPI, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPICAPI)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI, spec.template.spec.authoritativeAPI: ClusterAPI' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should create an authoritative CAPI Machine when scaling MAPI MachineSet to 1 replicas", func() {
				By("Scaling up MAPI MachineSet to 1 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 1)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				capiframework.WaitForMachineSet(cl, mapiMSAuthMAPICAPI, capiframework.CAPINamespace)
				// TODO: Uncomment once https://github.com/openshift/cluster-capi-operator/pull/329 is merged
				// verifyMAPIMachinesetReplicas(mapiMachineSet, 1)

				By("Verifying MAPI Machine is created and .status.authoritativeAPI to equal CAPI")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying CAPI Machine is created and Paused condition is False and provisions a running Machine")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(cl, capiMachineSet)
				verifyMachineRunning(cl, capiMachine.Name, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyCAPIMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should delete both MAPI and CAPI MachineSets/Machines and InfraMachineTemplate when deleting MAPI MachineSet", func() {
				Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed(), "Should be able to delete test MachineSet")
				capiframework.WaitForMachineSetsDeleted(cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				verifyAWSMachineTemplateDeleted(awsMachineTemplate.Name)
			})
		})
	})

	var _ = Describe("Update MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName = "ms-authoritativeapi-mapi"
		var mapiMachineSet *mapiv1beta1.MachineSet
		var capiMachineSet *clusterv1.MachineSet
		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var newAWSMachineTemplate *awsv1.AWSMachineTemplate

		BeforeAll(func() {
			mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			capiMachineSet, awsMachineTemplate = verifyMAPIMachineSetHasCAPIMirror(cl, mapiMSAuthMAPIName)

			DeferCleanup(func() {
				By("Cleaning up 'Update MachineSet' resources")
				cleanupMachineSetTestResources(
					ctx,
					cl,
					[]*clusterv1.MachineSet{capiMachineSet},
					[]*awsv1.AWSMachineTemplate{awsMachineTemplate, newAWSMachineTemplate},
					[]*mapiv1beta1.MachineSet{mapiMachineSet},
				)
			})
		})

		Context("when MAPI MachineSet with spec.authoritativeAPI: MachineAPI and replicas 0", Ordered, func() {
			It("should reject update when attempting scaling of the CAPI MachineSet mirror", func() {
				By("Scaling up CAPI MachineSet to 1 should be rejected")
				capiframework.ScaleMachineSet(mapiMSAuthMAPIName, 1, capiframework.CAPINamespace)
				capiMachineSet = capiframework.GetMachineSet(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
				// TODO: Uncomment once https://github.com/openshift/cluster-capi-operator/pull/329 is merged
				// verifyCAPIMachinesetReplicas(capiMachineSet, 0)
			})

			It("should reject update when attempting to change the spec of the CAPI MachineSet mirror", func() {
				By("Updating CAPI mirror spec (such as DeletePolicy)")
				Eventually(k.Update(capiMachineSet, func() {
					capiMachineSet.Spec.DeletePolicy = "Oldest"
				}), capiframework.WaitMedium, capiframework.RetryShort).Should(Succeed(), "Failed to update CAPI MachineSet DeletePolicy")

				By("Verifying both MAPI and CAPI MachineSet spec value are restored to original value")
				Eventually(k.Object(mapiMachineSet), capiframework.WaitShort, capiframework.RetryShort).Should(HaveField("Spec.DeletePolicy", SatisfyAny(BeEmpty(), Equal("Random"))), "DeletePolicy should be either empty or 'Random'")
				Eventually(k.Object(capiMachineSet), capiframework.WaitShort, capiframework.RetryShort).Should(HaveField("Spec.DeletePolicy", HaveValue(Equal("Random"))), "DeletePolicy should be 'Random'")
			})

			It("should create a new InfraTemplate when update MAPI MachineSet providerSpec", func() {
				By("Updating MAPI MachineSet providerSpec InstanceType to m5.large")
				newInstanceType := "m5.large"
				updateAWSMachineSetProviderSpec(ctx, cl, mapiMachineSet, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					providerSpec.InstanceType = newInstanceType
				})

				By("Waiting for new InfraTemplate to be created")
				originalAWSMachineTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name
				capiMachineSet = capiframework.GetMachineSet(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
				Eventually(k.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(HaveField("Spec.Template.Spec.InfrastructureRef.Name", Not(Equal(originalAWSMachineTemplateName))), "InfraTemplate name should be changed")

				By("Verifying new InfraTemplate has the updated InstanceType")
				newAWSMachineTemplate, err := capiframework.GetAWSMachineTemplateByPrefix(cl, mapiMSAuthMAPIName, capiframework.CAPINamespace)
				Expect(err).ToNot(HaveOccurred(), "Failed to get new awsMachineTemplate  %s", newAWSMachineTemplate)
				Expect(newAWSMachineTemplate.Spec.Template.Spec.InstanceType).To(Equal(newInstanceType))

				By("Verifying the old InfraTemplate is deleted")
				verifyAWSMachineTemplateDeleted(originalAWSMachineTemplateName)
			})
		})

		Context("when switching MAPI MachineSet spec.authoritativeAPI to ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should be rejected when scaling MAPI MachineSet", func() {
				By("Scaling up MAPI MachineSet to 1")
				mapiframework.ScaleMachineSet(mapiMSAuthMAPIName, 1)

				By("Verifying MAPI MachineSet replicas is restored to original value 0")
				// TODO: Uncomment once https://github.com/openshift/cluster-capi-operator/pull/329 is merged
				// verifyMAPIMachinesetReplicas(mapiMachineSet, 0)
			})

			It("should be rejected when when updating providerSpec of MAPI MachineSet", func() {
				By("Getting the current MAPI MachineSet providerSpec InstanceType")
				originalInstanceType := getMAPIMachineSetInstanceType(ctx, cl, mapiMachineSet)

				By("Updating the MAPI MachineSet providerSpec InstanceType")
				updateAWSMachineSetProviderSpec(ctx, cl, mapiMachineSet, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					providerSpec.InstanceType = "m5.xlarge"
				})

				By("Verifying MAPI MachineSet InstanceType is restored to original value")
				verifyMAPIMachineSetInstanceType(ctx, cl, mapiMachineSet, originalInstanceType)
			})

			It("should update MAPI MachineSet and remove old InfraTemplate when CAPI MachineSet points to new InfraTemplate", func() {
				By("Creating a new awsMachineTemplate with different spec")
				newInstanceType := "m6.xlarge"
				originalAWSMachineTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name
				newAWSMachineTemplate = createAWSMachineTemplateWithInstanceType(ctx, cl, originalAWSMachineTemplateName, newInstanceType)

				By("Updating CAPI MachineSet to point to the new InfraTemplate")
				updateCAPIMachineSetInfraTemplate(capiMachineSet, newAWSMachineTemplate.Name)

				By("Verifying the MAPI MachineSet is updated to reflect the new template")
				mapiMachineSet, _ = mapiframework.GetMachineSet(ctx, cl, mapiMSAuthMAPIName)
				Eventually(k.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Spec.Template.Spec.ProviderSpec.Value.Raw", ContainSubstring(newInstanceType)),
					"MAPI MachineSet providerSpec should be updated to reflect the new InfraTemplate with InstanceType %s", newInstanceType,
				)
			})
		})
	})
})

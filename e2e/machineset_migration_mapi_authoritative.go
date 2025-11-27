// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration][platform:aws][Disruptive] MachineSet Migration MAPI Authoritative Tests", Ordered, Label("Conformance"), Label("Serial"), func() {
	var k komega.Komega

	BeforeAll(func() {
		InitCommonVariables()
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}

		k = komega.New(cl)
	})

	Describe("Create MAPI MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName string
		var existingCAPIMSAuthorityMAPIName string

		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet

		Context("with spec.authoritativeAPI: MachineAPI and existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				existingCAPIMSAuthorityMAPIName = generateName("capi-ms-auth-mapi-")
				capiMachineSet = createCAPIMachineSet(ctx, cl, 0, existingCAPIMSAuthorityMAPIName, "")
				awsMachineTemplate = waitForAWSMachineTemplate(existingCAPIMSAuthorityMAPIName)

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

			// https://issues.redhat.com/browse/OCPCLOUD-3188
			PIt("should reject creation of MAPI MachineSet with same name as existing CAPI MachineSet", func() {
				By("Creating a same name MAPI MachineSet")
				createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, existingCAPIMSAuthorityMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})

		Context("with spec.authoritativeAPI: MachineAPI and when no existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				mapiMSAuthMAPIName = generateName("ms-auth-mapi-")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				capiMachineSet = waitForCAPIMachineSetMirror(mapiMSAuthMAPIName)
				awsMachineTemplate = waitForAWSMachineTemplate(mapiMSAuthMAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI and when no existing CAPI MachineSet with same name' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should create a CAPI mirror and set conditions correctly", func() {
				By("Verifying MAPI MachineSet .status.authoritativeAPI equals MachineAPI")
				verifyMachineSetAuthoritative(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying that MAPI MachineSet Paused condition is False")
				verifyMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying that MAPI MachineSet Synchronized condition is True")
				verifyMAPIMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying that MAPI MachineSet has a CAPI MachineSet mirror")
				waitForCAPIMachineSetMirror(mapiMSAuthMAPIName)

				By("Verifying that the mirror CAPI MachineSet has Paused condition True")
				verifyMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})
	})

	Describe("Scale MAPI MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName string
		var mapiMSAuthMAPICAPI string

		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet
		var firstMAPIMachine *mapiv1beta1.Machine
		var secondMAPIMachine *mapiv1beta1.Machine

		Context("with spec.authoritativeAPI: MachineAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMSAuthMAPIName = generateName("ms-auth-mapi-")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				capiMachineSet, awsMachineTemplate = waitForMAPIMachineSetMirrors(mapiMSAuthMAPIName)

				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				Expect(mapiMachines).ToNot(BeEmpty(), "no MAPI Machines found")

				capiMachines := capiframework.GetMachinesFromMachineSet(capiMachineSet)
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

			It("should scale, switch authority, and clean up successfully", func() {
				By("Scaling up MAPI MachineSet to 2 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 2)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				mapiframework.WaitForMachineSet(ctx, cl, mapiMSAuthMAPIName)
				verifyMachinesetReplicas(mapiMachineSet, 2)
				verifyMachinesetReplicas(capiMachineSet, 2)

				By("Verifying a new MAPI Machine is created and Paused condition is False")
				var err error
				secondMAPIMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineRunning(cl, secondMAPIMachine)
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(secondMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying there is a non-authoritative CAPI Machine mirror for the MAPI Machine and its Paused condition is True")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(capiMachineSet)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying CAPI MachineSet status.replicas is set to 2")
				verifyMachinesetReplicas(capiMachineSet, 2)

				By("Switching MAPI MachineSet AuthoritativeAPI to ClusterAPI")
				switchMachineSetTemplateAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Scaling up CAPI MachineSet to 3")
				capiframework.ScaleCAPIMachineSet(mapiMSAuthMAPIName, 3, capiframework.CAPINamespace)

				By("Verifying MachineSet status.replicas is set to 3")
				verifyMachinesetReplicas(capiMachineSet, 3)
				verifyMachinesetReplicas(mapiMachineSet, 3)

				By("Verifying a new CAPI Machine is running and Paused condition is False")
				capiMachine = capiframework.GetNewestMachineFromMachineSet(capiMachineSet)
				verifyMachineRunning(cl, capiMachine)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying old Machines still exist and authority on them is still MachineAPI")
				verifyMachineAuthoritative(firstMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Scaling down CAPI MachineSet to 1")
				capiframework.ScaleCAPIMachineSet(mapiMSAuthMAPIName, 1, capiframework.CAPINamespace)

				By("Verifying both CAPI MachineSet and its MAPI MachineSet mirror are scaled down to 1")
				verifyMachinesetReplicas(capiMachineSet, 1)
				verifyMachinesetReplicas(mapiMachineSet, 1)

				By("Switching back the AuthoritativeAPI to MachineAPI")
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetAuthoritative(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				switchMachineSetTemplateAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Deleting MAPI MachineSet and verifying mirrors are removed")
				Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed(), "Should be able to delete test MachineSet")
				capiframework.WaitForMachineSetsDeleted(capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				verifyResourceRemoved(awsMachineTemplate)
			})
		})

		Context("with spec.authoritativeAPI: MachineAPI, spec.template.spec.authoritativeAPI: ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMSAuthMAPICAPI = generateName("ms-mapi-machine-capi-")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPICAPI, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				capiMachineSet, awsMachineTemplate = waitForMAPIMachineSetMirrors(mapiMSAuthMAPICAPI)

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

			It("should create authoritative CAPI Machines and clean up successfully", func() {
				By("Scaling up MAPI MachineSet to 1 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMachineSet.GetName(), 1)).To(Succeed(), "should be able to scale up MAPI MachineSet")
				capiframework.WaitForMachineSet(ctx, cl, mapiMSAuthMAPICAPI, capiframework.CAPINamespace)
				verifyMachinesetReplicas(mapiMachineSet, 1)
				verifyMachinesetReplicas(capiMachineSet, 1)

				By("Verifying MAPI Machine is created and .status.authoritativeAPI to equal CAPI")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying CAPI Machine is created and Paused condition is False and provisions a running Machine")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(capiMachineSet)
				verifyMachineRunning(cl, capiMachine)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Deleting MAPI MachineSet and verifying mirrors are removed")
				Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed(), "Should be able to delete test MachineSet")
				capiframework.WaitForMachineSetsDeleted(capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				verifyResourceRemoved(awsMachineTemplate)
			})
		})
	})

	Describe("Update MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName string
		var mapiMachineSet *mapiv1beta1.MachineSet
		var capiMachineSet *clusterv1.MachineSet
		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var newAWSMachineTemplate *awsv1.AWSMachineTemplate

		BeforeAll(func() {
			mapiMSAuthMAPIName = generateName("ms-auth-mapi-")
			mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			capiMachineSet, awsMachineTemplate = waitForMAPIMachineSetMirrors(mapiMSAuthMAPIName)

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
			It("should reject CAPI mirror updates and allow MAPI providerSpec updates", func() {
				By("Scaling up CAPI MachineSet to 1 should be rejected by VAP")
				replicas := int32(1)
				Eventually(func() error {
					return k.Update(capiMachineSet, func() {
						capiMachineSet.Spec.Replicas = &replicas
					})()
				}, capiframework.WaitShort, capiframework.RetryShort).Should(MatchError(ContainSubstring("Changing .spec is not allowed")))

				capiMachineSet = capiframework.GetMachineSetWithRetry(mapiMSAuthMAPIName, capiframework.CAPINamespace)
				verifyMachinesetReplicas(capiMachineSet, 0)

				By("Updating CAPI mirror spec (such as Deletion.Order) should be rejected by VAP")
				Eventually(func() error {
					return k.Update(capiMachineSet, func() {
						capiMachineSet.Spec.Deletion = clusterv1.MachineSetDeletionSpec{
							Order: clusterv1.OldestMachineSetDeletionOrder,
						}
					})()
				}, capiframework.WaitShort, capiframework.RetryShort).Should(MatchError(ContainSubstring("Changing .spec is not allowed")))

				By("Updating MAPI MachineSet providerSpec InstanceType to m5.large")
				newInstanceType := "m5.large"
				Expect(updateAWSMachineSetProviderSpec(ctx, cl, mapiMachineSet, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
					providerSpec.InstanceType = newInstanceType
				})).To(Succeed(), "failed to patch MachineSet provider spec")

				By("Waiting for new InfraTemplate to be created")
				originalAWSMachineTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name
				capiMachineSet = capiframework.GetMachineSetWithRetry(mapiMSAuthMAPIName, capiframework.CAPINamespace)
				Eventually(k.Object(capiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(HaveField("Spec.Template.Spec.InfrastructureRef.Name", Not(Equal(originalAWSMachineTemplateName))), "Should have InfraTemplate name changed")

				By("Verifying new InfraTemplate has the updated InstanceType")
				var err error
				newAWSMachineTemplate, err = getAWSMachineTemplateByPrefix(mapiMSAuthMAPIName, capiframework.CAPINamespace)
				Expect(err).ToNot(HaveOccurred(), "Failed to get new awsMachineTemplate %s", newAWSMachineTemplate)
				Expect(newAWSMachineTemplate.Spec.Template.Spec.InstanceType).To(Equal(newInstanceType))

				By("Verifying the old InfraTemplate is deleted")
				verifyResourceRemoved(awsMachineTemplate)
			})
		})

		Context("when switching MAPI MachineSet spec.authoritativeAPI to ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				switchMachineSetTemplateAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should reject MAPI updates and allow CAPI InfraTemplate updates", func() {
				By("Scaling up MAPI MachineSet to 1 should be rejected by VAP")
				replicas := int32(1)
				Eventually(func() error {
					return k.Update(mapiMachineSet, func() {
						mapiMachineSet.Spec.Replicas = &replicas
					})()
				}, capiframework.WaitShort, capiframework.RetryShort).Should(MatchError(ContainSubstring("Any other change inside .spec is not allowed")))

				By("Updating the MAPI MachineSet providerSpec InstanceType should be rejected by VAP")
				Eventually(func() error {
					return updateAWSMachineSetProviderSpec(ctx, cl, mapiMachineSet, func(providerSpec *mapiv1beta1.AWSMachineProviderConfig) {
						providerSpec.InstanceType = "m5.xlarge"
					})
				}, capiframework.WaitShort, capiframework.RetryShort).Should(MatchError(ContainSubstring("Any other change inside .spec is not allowed")))

				By("Creating a new awsMachineTemplate with different spec")
				newInstanceType := "m6.xlarge"
				originalAWSMachineTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name
				newAWSMachineTemplate = createAWSMachineTemplate(ctx, cl, originalAWSMachineTemplateName, func(spec *awsv1.AWSMachineSpec) {
					spec.InstanceType = newInstanceType
				})

				By("Updating CAPI MachineSet to point to the new InfraTemplate")
				updateCAPIMachineSetInfraTemplate(capiMachineSet, newAWSMachineTemplate.Name)

				By("Verifying the MAPI MachineSet is updated to reflect the new template")
				var err error
				mapiMachineSet, err = mapiframework.GetMachineSet(ctx, cl, mapiMSAuthMAPIName)
				Expect(err).ToNot(HaveOccurred(), "failed to refresh MAPI MachineSet")
				Eventually(k.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
					HaveField("Spec.Template.Spec.ProviderSpec.Value.Raw", ContainSubstring(newInstanceType)),
					"Should have MAPI MachineSet providerSpec updated to reflect the new InfraTemplate with InstanceType %s", newInstanceType,
				)
			})
		})
	})
})

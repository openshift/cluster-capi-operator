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
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	mapiframework "github.com/openshift/cluster-api-actuator-pkg/pkg/framework"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] MachineSet Migration MAPI Authoritative Tests", Ordered, func() {
	var k komega.Komega

	BeforeAll(func() {
		skipUnsupportedPlatforms()

		if !capiframework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateMachineAPIMigration) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}

		k = komega.New(cl)
	})

	Describe("Create MAPI MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName string
		var existingCAPIMSAuthorityMAPIName string

		var infraMachineTemplate client.Object
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet

		Context("with spec.authoritativeAPI: MachineAPI and existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				existingCAPIMSAuthorityMAPIName = generateName("capi-ms-auth-mapi-")
				capiMachineSet = createCAPIMachineSet(ctx, cl, 0, existingCAPIMSAuthorityMAPIName, "")
				infraMachineTemplate = waitForInfraMachineTemplate(existingCAPIMSAuthorityMAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI and existing CAPI MachineSet with same name' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]client.Object{infraMachineTemplate},
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
				infraMachineTemplate = waitForInfraMachineTemplate(mapiMSAuthMAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI and when no existing CAPI MachineSet with same name' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{},
						[]client.Object{infraMachineTemplate},
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

		var infraMachineTemplate client.Object
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet
		var firstMAPIMachine *mapiv1beta1.Machine
		var secondMAPIMachine *mapiv1beta1.Machine

		Context("with spec.authoritativeAPI: MachineAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMSAuthMAPIName = generateName("ms-auth-mapi-")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				capiMachineSet, infraMachineTemplate = waitForMAPIMachineSetMirrors(mapiMSAuthMAPIName)

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
						[]client.Object{infraMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should scale, switch authority, and clean up successfully", func() {
				By("Scaling up MAPI MachineSet to 2 replicas")
				Expect(mapiframework.ScaleMachineSetWithContext(ctx, mapiMachineSet.GetName(), 2)).To(Succeed(), "should be able to scale up MAPI MachineSet")
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
				verifyResourceRemoved(infraMachineTemplate)
			})
		})

		Context("with spec.authoritativeAPI: MachineAPI, spec.template.spec.authoritativeAPI: ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMSAuthMAPICAPI = generateName("ms-mapi-machine-capi-")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPICAPI, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				capiMachineSet, infraMachineTemplate = waitForMAPIMachineSetMirrors(mapiMSAuthMAPICAPI)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: MachineAPI, spec.template.spec.authoritativeAPI: ClusterAPI' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]client.Object{infraMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should create authoritative CAPI Machines and clean up successfully", func() {
				By("Scaling up MAPI MachineSet to 1 replicas")
				Expect(mapiframework.ScaleMachineSetWithContext(ctx, mapiMachineSet.GetName(), 1)).To(Succeed(), "should be able to scale up MAPI MachineSet")
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
				verifyResourceRemoved(infraMachineTemplate)
			})
		})
	})

	Describe("Update MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName string
		var mapiMachineSet *mapiv1beta1.MachineSet
		var capiMachineSet *clusterv1.MachineSet
		var infraMachineTemplate client.Object
		var newInfraMachineTemplate client.Object

		newAWSInstanceType1 := "m5.2xlarge"
		newAWSInstanceType2 := "m5.4xlarge"
		newAWSInstanceType3 := "m5.8xlarge"
		newOpenStackFlavor1 := "m1.2xlarge"
		newOpenStackFlavor2 := "m1.4xlarge"
		newOpenStackFlavor3 := "m1.8xlarge"

		BeforeAll(func() {
			mapiMSAuthMAPIName = generateName("ms-auth-mapi-")
			mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
			capiMachineSet, infraMachineTemplate = waitForMAPIMachineSetMirrors(mapiMSAuthMAPIName)

			DeferCleanup(func() {
				By("Cleaning up 'Update MachineSet' resources")
				cleanupMachineSetTestResources(
					ctx,
					cl,
					[]*clusterv1.MachineSet{capiMachineSet},
					[]client.Object{infraMachineTemplate, newInfraMachineTemplate},
					[]*mapiv1beta1.MachineSet{mapiMachineSet},
				)
			})
		})

		Context("when MAPI MachineSet with spec.authoritativeAPI: MachineAPI and replicas 0", Ordered, func() {
			It("should reject CAPI mirror updates", func() {
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
			})

			DescribeTable(
				"Updating provider-specific spec in MAPI MachineSet",
				func(
					targetPlatform configv1.PlatformType,
					templateObj client.Object,
					updateProviderSpecFunc func(ms *mapiv1beta1.MachineSet) ([]byte, error),
					validateTemplateFunc func(g Gomega, obj client.Object),
				) {
					if platform != targetPlatform {
						Skip(fmt.Sprintf("Skipping %s-specific tests on non-%s platform.", targetPlatform, targetPlatform))
					}

					By("Updating MAPI MachineSet providerSpec")
					Expect(updateInfraMachineSetProviderSpec(ctx, cl, mapiMachineSet, func(ms *mapiv1beta1.MachineSet) ([]byte, error) {
						return updateProviderSpecFunc(ms)
					})).To(Succeed(), "failed to patch MachineSet provider spec")

					By("Waiting for new InfraTemplate with updated field to be created")
					Eventually(func(g Gomega) {
						originalInfraMachineTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name

						currentMachineSet := &clusterv1.MachineSet{}
						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(capiMachineSet), currentMachineSet)).To(Succeed(), "should get current CAPI machine set successfully")

						currentTemplateName := currentMachineSet.Spec.Template.Spec.InfrastructureRef.Name
						g.Expect(currentTemplateName).ToNot(Equal(originalInfraMachineTemplateName),
							"InfraTemplate name should have changed from %s", originalInfraMachineTemplateName)

						templateObj.SetName(currentTemplateName)
						templateObj.SetNamespace(capiMachineSet.Namespace)

						g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(templateObj), templateObj)).To(Succeed(), "should get current machine template successfully")

						validateTemplateFunc(g, templateObj)
						capiMachineSet = currentMachineSet
					}, capiframework.WaitLong, capiframework.RetryMedium).Should(Succeed(),
						"Should have a new InfraTemplate with updated field")

					By("Verifying the old InfraTemplate is deleted")
					verifyResourceRemoved(infraMachineTemplate)
				},
				Entry(
					"should allow MAPI AWS providerSpec updates",
					configv1.AWSPlatformType,
					&awsv1.AWSMachineTemplate{},
					func(ms *mapiv1beta1.MachineSet) ([]byte, error) {
						providerSpec := getAWSProviderSpecFromMachineSet(ms)
						Expect(providerSpec).ToNot(BeNil(), "failed to extract AWS ProviderSpec from MachineSet %s", ms.Name)
						providerSpec.InstanceType = newAWSInstanceType1
						return json.Marshal(providerSpec)
					},
					func(g Gomega, obj client.Object) {
						m, ok := obj.(*awsv1.AWSMachineTemplate)
						g.Expect(ok).To(BeTrue(), "should be an *awsv1.AWSMachineTemplate")

						g.Expect(m.Spec.Template.Spec.InstanceType).To(Equal(newAWSInstanceType1),
							"AWSMachineTemplate %s should have InstanceType %s", m.Name, newAWSInstanceType1)

						newInfraMachineTemplate = m
					},
				),
				Entry(
					"should allow MAPI OpenStack providerSpec updates",
					configv1.OpenStackPlatformType,
					&openstackv1.OpenStackMachineTemplate{},
					func(ms *mapiv1beta1.MachineSet) ([]byte, error) {
						if ms.Spec.Template.Spec.ProviderSpec.Value == nil {
							return nil, nil
						}

						providerSpec, err := mapi2capi.OpenStackProviderSpecFromRawExtension(ms.Spec.Template.Spec.ProviderSpec.Value)
						Expect(err).To(BeNil(), "failed to extract OpenStack ProviderSpec from MachineSet %s", ms.Name)
						providerSpec.Flavor = newOpenStackFlavor1

						return json.Marshal(providerSpec)
					},
					func(g Gomega, obj client.Object) {
						m, ok := obj.(*openstackv1.OpenStackMachineTemplate)
						g.Expect(ok).To(BeTrue(), "should be an *openstackv1.OpenStackMachineTemplate")

						g.Expect(*m.Spec.Template.Spec.Flavor).To(Equal(newOpenStackFlavor1),
							"OpenStackMachineTemplate %s should have InstanceType %s", m.Name, newOpenStackFlavor1)

						newInfraMachineTemplate = m
					},
				),
			)
		})

		Context("when switching MAPI MachineSet spec.authoritativeAPI to ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				switchMachineSetTemplateAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			It("should reject MAPI updates", func() {
				By("Scaling up MAPI MachineSet to 1 should be rejected by VAP")
				replicas := int32(1)
				Eventually(func() error {
					return k.Update(mapiMachineSet, func() {
						mapiMachineSet.Spec.Replicas = &replicas
					})()
				}, capiframework.WaitShort, capiframework.RetryShort).Should(MatchError(ContainSubstring("Any other change inside .spec is not allowed")))
			})

			DescribeTable(
				"Updating MAPI provider spec updates and CAPI Infra MachineTemplate",
				func(
					targetPlatform configv1.PlatformType,
					updateMAPIProviderSpecFunc func(ms *mapiv1beta1.MachineSet) ([]byte, error),
					createNewTemplateFunc func(originalInfraMachineTemplateName string) client.Object,
					validateMAPIMachineTemplateFunc func(mapiMachineSet *mapiv1beta1.MachineSet),
				) {
					if platform != targetPlatform {
						Skip(fmt.Sprintf("Skipping %s-specific tests on non-%s platform.", targetPlatform, targetPlatform))
					}

					By("Updating the field in the MAPI MachineSet providerSpec should be rejected by VAP")
					Eventually(func() error {
						return updateInfraMachineSetProviderSpec(ctx, cl, mapiMachineSet, func(ms *mapiv1beta1.MachineSet) ([]byte, error) {
							return updateMAPIProviderSpecFunc(ms)
						})
					}, capiframework.WaitShort, capiframework.RetryShort).Should(MatchError(ContainSubstring("Any other change inside .spec is not allowed")))

					By("Creating a new infra MachineTemplate with different spec")
					originalInfraMachineTemplateName := capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name
					newInfraMachineTemplate = createNewTemplateFunc(originalInfraMachineTemplateName)

					By("Updating CAPI MachineSet to point to the new InfraTemplate")
					updateCAPIMachineSetInfraTemplate(capiMachineSet, newInfraMachineTemplate.GetName())

					By("Verifying the MAPI MachineSet is updated to reflect the new template")
					var err error
					mapiMachineSet, err = mapiframework.GetMachineSet(ctx, cl, mapiMSAuthMAPIName)
					Expect(err).ToNot(HaveOccurred(), "failed to refresh MAPI MachineSet")
					validateMAPIMachineTemplateFunc(mapiMachineSet)
				},
				Entry("should reject updating AWS provider spec and allow updating CAPI AWS Machine template",
					configv1.AWSPlatformType,
					func(ms *mapiv1beta1.MachineSet) ([]byte, error) {
						providerSpec := getAWSProviderSpecFromMachineSet(ms)
						Expect(providerSpec).ToNot(BeNil(), "failed to extract AWS ProviderSpec from MachineSet %s", ms.Name)
						providerSpec.InstanceType = newAWSInstanceType2
						return json.Marshal(providerSpec)
					},
					func(originalAWSMachineTemplateName string) client.Object {
						newInfraMachineTemplate = createAWSMachineTemplate(ctx, cl, originalAWSMachineTemplateName, func(spec *awsv1.AWSMachineSpec) {
							spec.InstanceType = newAWSInstanceType3
						})

						return newInfraMachineTemplate
					},
					func(mapiMachineSet *mapiv1beta1.MachineSet) {
						Eventually(k.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
							HaveField("Spec.Template.Spec.ProviderSpec.Value.Raw", ContainSubstring(newAWSInstanceType3)),
							"Should have MAPI MachineSet providerSpec updated to reflect the new InfraTemplate with InstanceType %s", newAWSInstanceType3,
						)
					},
				),
				Entry("should reject updating OpenStack provider spec allow updating CAPI OpenStack Machine template",
					configv1.OpenStackPlatformType,
					func(ms *mapiv1beta1.MachineSet) ([]byte, error) {
						if ms.Spec.Template.Spec.ProviderSpec.Value == nil {
							return nil, nil
						}

						providerSpec, err := mapi2capi.OpenStackProviderSpecFromRawExtension(ms.Spec.Template.Spec.ProviderSpec.Value)
						Expect(err).To(BeNil(), "failed to extract OpenStack ProviderSpec from MachineSet %s", ms.Name)
						providerSpec.Flavor = newOpenStackFlavor2

						return json.Marshal(providerSpec)
					},
					func(originalOpenStackMachineTemplateName string) client.Object {
						newInfraMachineTemplate = createOpenStackMachineTemplate(ctx, cl, originalOpenStackMachineTemplateName, func(osms *openstackv1.OpenStackMachineSpec) {
							osms.Flavor = &newOpenStackFlavor3
						})

						return newInfraMachineTemplate
					},
					func(mapiMachineSet *mapiv1beta1.MachineSet) {
						Eventually(k.Object(mapiMachineSet), capiframework.WaitMedium, capiframework.RetryMedium).Should(
							HaveField("Spec.Template.Spec.ProviderSpec.Value.Raw", ContainSubstring(newOpenStackFlavor3)),
							"Should have MAPI MachineSet providerSpec updated to reflect the new InfraTemplate with Flavor %s", newOpenStackFlavor3,
						)
					},
				),
			)
		})
	})
})

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
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration][platform:aws][Disruptive] MachineSet Migration CAPI Authoritative Tests", Ordered, Label("Conformance"), Label("Serial"), func() {
	BeforeAll(func() {
		InitCommonVariables()
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	Describe("Create MAPI MachineSets", Ordered, func() {
		var mapiMSAuthCAPIName string
		var existingCAPIMSAuthorityCAPIName string

		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet
		var instanceType = "m5.large"

		Context("with spec.authoritativeAPI: ClusterAPI and existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				existingCAPIMSAuthorityCAPIName = generateName("capi-ms-auth-capi-")
				capiMachineSet = createCAPIMachineSet(ctx, cl, 0, existingCAPIMSAuthorityCAPIName, instanceType)

				By("Creating a same name MAPI MachineSet")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, existingCAPIMSAuthorityCAPIName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				awsMachineTemplate = waitForAWSMachineTemplate(existingCAPIMSAuthorityCAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: ClusterAPI and existing CAPI MachineSet with same name' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("should verify that MAPI MachineSet has Paused condition True", func() {
				verifyMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})

			// bug https://issues.redhat.com/browse/OCPBUGS-55337
			PIt("should verify that the non-authoritative MAPI MachineSet providerSpec has been updated to reflect the authoritative CAPI MachineSet mirror values", func() {
				verifyMAPIMachineSetProviderSpec(mapiMachineSet, HaveField("InstanceType", Equal(instanceType)))
			})
		})

		Context("with spec.authoritativeAPI: ClusterAPI and no existing CAPI MachineSet with same name", func() {
			BeforeAll(func() {
				mapiMSAuthCAPIName = generateName("ms-auth-capi-")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 0, mapiMSAuthCAPIName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				capiMachineSet = waitForCAPIMachineSetMirror(mapiMSAuthCAPIName)
				awsMachineTemplate = waitForAWSMachineTemplate(mapiMSAuthCAPIName)

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: ClusterAPI and no existing CAPI MachineSet with same name' resources")
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
				By("Verifying MAPI MachineSet .status.authoritativeAPI equals ClusterAPI")
				verifyMachineSetAuthoritative(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying that MAPI MachineSet Paused condition is True")
				verifyMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying that MAPI MachineSet Synchronized condition is True")
				verifyMAPIMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying that the non-authoritative MAPI MachineSet has an authoritative CAPI MachineSet mirror")
				waitForCAPIMachineSetMirror(mapiMSAuthCAPIName)

				By("Verifying that CAPI MachineSet has Paused condition False")
				verifyMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	Describe("Scale MAPI MachineSets", Ordered, func() {
		var mapiMSAuthCAPIName string

		var awsMachineTemplate *awsv1.AWSMachineTemplate
		var capiMachineSet *clusterv1.MachineSet
		var mapiMachineSet *mapiv1beta1.MachineSet
		var firstMAPIMachine *mapiv1beta1.Machine
		var secondMAPIMachine *mapiv1beta1.Machine

		Context("with spec.authoritativeAPI: ClusterAPI", Ordered, func() {
			BeforeAll(func() {
				mapiMSAuthCAPIName = generateName("ms-auth-capi-")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthCAPIName, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityClusterAPI)
				capiMachineSet, awsMachineTemplate = waitForMAPIMachineSetMirrors(mapiMSAuthCAPIName)

				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				Expect(mapiMachines).ToNot(BeEmpty(), "no MAPI Machines found")

				capiMachines := capiframework.GetMachinesFromMachineSet(capiMachineSet)
				Expect(capiMachines).ToNot(BeEmpty(), "no CAPI Machines found")
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))
				firstMAPIMachine = mapiMachines[0]

				DeferCleanup(func() {
					By("Cleaning up Context 'with spec.authoritativeAPI: ClusterAPI' resources")
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
				By("Scaling up CAPI MachineSet to 2 replicas")
				capiframework.ScaleCAPIMachineSet(mapiMSAuthCAPIName, 2, capiframework.CAPINamespace)

				By("Verifying MachineSet status.replicas is set to 2")
				verifyMachinesetReplicas(capiMachineSet, 2)
				verifyMachinesetReplicas(mapiMachineSet, 2)

				By("Verifying a new CAPI Machine is created and Paused condition is False")
				capiMachineSet = capiframework.GetMachineSetWithRetry(mapiMSAuthCAPIName, capiframework.CAPINamespace)
				capiMachine := capiframework.GetNewestMachineFromMachineSet(capiMachineSet)
				verifyMachineRunning(cl, capiMachine)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				var err error
				secondMAPIMachine, err = mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(secondMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Switching MachineSet's AuthoritativeAPI to MachineAPI")
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetAuthoritative(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				switchMachineSetTemplateAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Scaling up MAPI MachineSet to 3 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMSAuthCAPIName, 3)).To(Succeed(), "should be able to scale up MAPI MachineSet")

				By("Verifying MachineSet status.replicas is set to 3")
				verifyMachinesetReplicas(mapiMachineSet, 3)
				verifyMachinesetReplicas(capiMachineSet, 3)

				By("Verifying the newly requested MAPI Machine has been created and its status.authoritativeAPI is MachineAPI and its Paused condition is False")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineRunning(cl, mapiMachine)
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying there is a non-authoritative, paused CAPI Machine mirror for the new MAPI Machine")
				capiMachine = capiframework.GetNewestMachineFromMachineSet(capiMachineSet)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying old Machines still exist and authority on them is still ClusterAPI")
				verifyMachineAuthoritative(firstMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineAuthoritative(secondMAPIMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Scaling down MAPI MachineSet to 1 replicas")
				Expect(mapiframework.ScaleMachineSet(mapiMSAuthCAPIName, 1)).To(Succeed(), "should be able to scale down MAPI MachineSet")
				verifyMachinesetReplicas(mapiMachineSet, 1)
				verifyMachinesetReplicas(capiMachineSet, 1)

				By("Switching back MachineSet's AuthoritativeAPI to ClusterAPI")
				switchMachineSetTemplateAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSetSynchronizedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Deleting CAPI MachineSet and verifying mirrors are removed")
				capiframework.DeleteMachineSets(ctx, cl, capiMachineSet)
				mapiframework.WaitForMachineSetsDeleted(ctx, cl, mapiMachineSet)
				capiframework.WaitForMachineSetsDeleted(capiMachineSet)
				verifyResourceRemoved(awsMachineTemplate)
			})
		})
	})

	Describe("Delete MachineSets", Ordered, func() {
		var mapiMSAuthMAPIName string
		var mapiMachineSet *mapiv1beta1.MachineSet
		var capiMachineSet *clusterv1.MachineSet
		var awsMachineTemplate *awsv1.AWSMachineTemplate

		Context("when removing non-authoritative MAPI MachineSet", Ordered, func() {
			BeforeAll(func() {
				mapiMSAuthMAPIName = generateName("ms-auth-mapi-del-")
				mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(ctx, cl, 1, mapiMSAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI)
				capiMachineSet, awsMachineTemplate = waitForMAPIMachineSetMirrors(mapiMSAuthMAPIName)

				mapiMachines, err := mapiframework.GetMachinesFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(mapiMachines).ToNot(BeEmpty(), "no MAPI Machines found")
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")

				capiMachines := capiframework.GetMachinesFromMachineSet(capiMachineSet)
				Expect(capiMachines).ToNot(BeEmpty(), "no CAPI Machines found")
				Expect(capiMachines[0].Name).To(Equal(mapiMachines[0].Name))

				DeferCleanup(func() {
					By("Cleaning up Context 'when removing non-authoritative MAPI MachineSet' resources")
					cleanupMachineSetTestResources(
						ctx,
						cl,
						[]*clusterv1.MachineSet{capiMachineSet},
						[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
						[]*mapiv1beta1.MachineSet{mapiMachineSet},
					)
				})
			})

			It("shouldn't delete its authoritative CAPI MachineSet", func() {
				By("Switching AuthoritativeAPI to ClusterAPI")
				switchMachineSetTemplateAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)

				// TODO(OCPBUGS-74571): this extra verification step is a workaround as a stop-gap until
				// remove this once https://issues.redhat.com/browse/OCPBUGS-74571 is fixed.
				By("Verifying MAPI MachineSet is paused and CAPI MachineSet is unpaused after switch")
				verifyMachineSetPausedCondition(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSetPausedCondition(capiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Scaling up CAPI MachineSet to 2 replicas")
				capiframework.ScaleCAPIMachineSet(mapiMachineSet.GetName(), 2, capiframework.CAPINamespace)

				By("Verifying MachineSet status.replicas is set to 2")
				verifyMachinesetReplicas(capiMachineSet, 2)
				verifyMachinesetReplicas(mapiMachineSet, 2)

				By("Verifying new CAPI Machine is running")
				capiMachine := capiframework.GetNewestMachineFromMachineSet(capiMachineSet)
				verifyMachineRunning(cl, capiMachine)
				verifyMachinePausedCondition(capiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying there is a non-authoritative, paused MAPI Machine mirror for the new CAPI Machine")
				mapiMachine, err := mapiframework.GetLatestMachineFromMachineSet(ctx, cl, mapiMachineSet)
				Expect(err).ToNot(HaveOccurred(), "failed to get MAPI Machines from MachineSet")
				verifyMachineAuthoritative(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(mapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Deleting non-authoritative MAPI MachineSet")
				mapiMachineSet, err = mapiframework.GetMachineSet(ctx, cl, mapiMSAuthMAPIName)
				Expect(err).ToNot(HaveOccurred(), "failed to get mapiMachineSet")
				Expect(mapiframework.DeleteMachineSets(cl, mapiMachineSet)).To(Succeed())

				By("Verifying CAPI MachineSet not removed, both MAPI Machines and Mirrors remain")
				// TODO: Add full verification once OCPBUGS-56897 is fixed
				capiMachineSet = capiframework.GetMachineSetWithRetry(mapiMSAuthMAPIName, capiframework.CAPINamespace)
				Expect(capiMachineSet).ToNot(BeNil(), "CAPI MachineSet should still exist after deleting non-authoritative MAPI MachineSet")
				Expect(capiMachineSet.DeletionTimestamp.IsZero()).To(BeTrue(), "CAPI MachineSet should not be marked for deletion")
			})
		})
	})
})

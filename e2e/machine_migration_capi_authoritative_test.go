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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Migration CAPI Authoritative Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	Describe("Create MAPI Machine", Ordered, func() {
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *mapiv1beta1.Machine

		Context("with spec.authoritativeAPI: ClusterAPI and already existing CAPI Machine with same name", func() {
			var mapiMachineAuthCAPIName string

			BeforeAll(func() {
				mapiMachineAuthCAPIName = generateName("machine-auth-capi-")
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

			It("should adopt existing CAPI Machine and set conditions correctly", func() {
				By("Verifying MAPI Machine .status.authoritativeAPI equals ClusterAPI")
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying MAPI Machine Synchronized condition is True")
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying MAPI Machine Paused condition is True")
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying CAPI Machine Paused condition is False")
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})

		Context("with spec.authoritativeAPI: ClusterAPI and no existing CAPI Machine with same name", func() {
			var mapiMachineAuthCAPIName string

			BeforeAll(func() {
				mapiMachineAuthCAPIName = generateName("machine-auth-capi-")
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

			It("should create a CAPI mirror and set conditions correctly", func() {
				By("Verifying CAPI Machine gets created and becomes Running")
				newCapiMachine = capiframework.GetMachine(newMapiMachine.Name, capiframework.CAPINamespace)
				verifyMachineRunning(cl, newCapiMachine)

				By("Verifying MAPI Machine .status.authoritativeAPI equals ClusterAPI")
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying MAPI Machine Synchronized condition is True")
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying MAPI Machine Paused condition is True")
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Verifying that the non-authoritative MAPI Machine has an authoritative CAPI Machine mirror")
				newCapiMachine = capiframework.GetMachine(mapiMachineAuthCAPIName, capiframework.CAPINamespace)

				By("Verifying CAPI Machine Paused condition is False")
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})
	})

	Describe("Deleting CAPI Machines", Ordered, func() {
		var mapiMachineAuthCAPINameDeletion string
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *mapiv1beta1.Machine

		Context("with spec.authoritativeAPI: ClusterAPI", func() {
			Context("when deleting the non-authoritative MAPI Machine", func() {
				BeforeAll(func() {
					mapiMachineAuthCAPINameDeletion = generateName("machine-auth-capi-del-")
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPINameDeletion, mapiv1beta1.MachineAuthorityClusterAPI)
					newCapiMachine = capiframework.GetMachine(newMapiMachine.Name, capiframework.CAPINamespace)
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

				It("should delete MAPI Machine and its mirrors", func() {
					By("Deleting MAPI Machine")
					Expect(mapiframework.DeleteMachines(ctx, cl, newMapiMachine)).To(Succeed())
					mapiframework.WaitForMachinesDeleted(cl, newMapiMachine)

					By("Verifying the CAPI machine is deleted")
					verifyResourceRemoved(newCapiMachine)

					By("Verifying the AWS machine is deleted")
					verifyResourceRemoved(&awsv1.AWSMachine{
						TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
						ObjectMeta: metav1.ObjectMeta{Name: mapiMachineAuthCAPINameDeletion, Namespace: capiframework.CAPINamespace},
					})
				})
			})
			Context("when deleting the authoritative CAPI Machine", func() {
				BeforeAll(func() {
					mapiMachineAuthCAPINameDeletion = generateName("machine-auth-capi-del-")
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthCAPINameDeletion, mapiv1beta1.MachineAuthorityClusterAPI)
					newCapiMachine = capiframework.GetMachine(newMapiMachine.Name, capiframework.CAPINamespace)
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

				It("should delete CAPI Machine and its mirrors", func() {
					By("Deleting CAPI Machine")
					capiframework.DeleteMachines(ctx, cl, capiframework.CAPINamespace, newCapiMachine)

					By("Verifying the MAPI machine is deleted")
					verifyResourceRemoved(newMapiMachine)

					By("Verifying the AWS machine is deleted")
					verifyResourceRemoved(&awsv1.AWSMachine{
						TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
						ObjectMeta: metav1.ObjectMeta{Name: mapiMachineAuthCAPINameDeletion, Namespace: capiframework.CAPINamespace},
					})
				})
			})
		})
	})

	Describe("Machine Migration Round Trip Tests", Ordered, func() {
		var capiMapiCapiRoundTripName string
		var newMapiMachine *mapiv1beta1.Machine
		var newCapiMachine *clusterv1.Machine

		Context("CAPI (and no existing CAPI Machine with same name) -> MAPI -> CAPI round trip", func() {
			BeforeAll(func() {
				capiMapiCapiRoundTripName = generateName("machine-capi-roundtrip-")
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

			It("should complete CAPI -> MAPI -> CAPI round trip successfully", func() {
				By("Verifying a CAPI mirror machine is created")
				newCapiMachine = capiframework.GetMachine(capiMapiCapiRoundTripName, capiframework.CAPINamespace)
				verifyMachineRunning(cl, newCapiMachine)

				By("Verifying paused conditions and synchronised generation are set correctly")
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSynchronizedGeneration(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Updating spec.authoritativeAPI to MachineAPI")
				updateMachineAuthoritativeAPI(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineRunning(cl, newMapiMachine)
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSynchronizedGeneration(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Updating spec.authoritativeAPI back to ClusterAPI")
				updateMachineAuthoritativeAPI(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineRunning(cl, newCapiMachine)
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSynchronizedGeneration(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Deleting CAPI machine and verifying mirrors are removed")
				capiframework.DeleteMachines(ctx, cl, capiframework.CAPINamespace, newCapiMachine)
				verifyResourceRemoved(newMapiMachine)
				verifyResourceRemoved(newCapiMachine)
				verifyResourceRemoved(&awsv1.AWSMachine{
					TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
					ObjectMeta: metav1.ObjectMeta{Name: capiMapiCapiRoundTripName, Namespace: capiframework.CAPINamespace},
				})
			})
		})

		// The bug https://issues.redhat.com/browse/OCPBUGS-63183 cause instance leak on AWS so I have to comment all the code out.
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
					verifyMachineSynchronizedGeneration(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
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
					verifyMachineSynchronizedGeneration(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
					verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
					verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				})

				It("should set the paused conditions and synchronised generation correctly after changing back spec.authoritativeAPI: ClusterAPI", func() {
					By("Updating spec.authoritativeAPI: ClusterAPI")
					updateMachineAuthoritativeAPI(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMachineRunning(cl, newCapiMachine)
					verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
					verifyMachineSynchronizedGeneration(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
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

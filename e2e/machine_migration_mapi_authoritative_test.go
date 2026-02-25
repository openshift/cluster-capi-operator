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

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Migration MAPI Authoritative Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	Describe("Create standalone MAPI Machine", Ordered, func() {
		var mapiMachineAuthMAPIName string
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *mapiv1beta1.Machine

		Context("With spec.authoritativeAPI: MachineAPI and no existing CAPI Machine with that name", func() {
			BeforeAll(func() {
				mapiMachineAuthMAPIName = generateName("machine-auth-mapi-")
				newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthMAPIName, mapiv1beta1.MachineAuthorityMachineAPI)

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
				By("Verifying MAPI Machine .status.authoritativeAPI equals MachineAPI")
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying MAPI Machine Synchronized condition is True")
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying MAPI Machine Paused condition is False")
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Verifying that the MAPI Machine has a CAPI Machine")
				newCapiMachine = capiframework.GetMachine(mapiMachineAuthMAPIName, capiframework.CAPINamespace)

				By("Verifying CAPI Machine Paused condition is True")
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})
	})

	Describe("Deleting MAPI Machines", Ordered, func() {
		var mapiMachineAuthMAPINameDelete string
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *mapiv1beta1.Machine

		Context("with spec.authoritativeAPI: MachineAPI", func() {
			Context("when deleting the authoritative MAPI Machine", func() {
				BeforeAll(func() {
					mapiMachineAuthMAPINameDelete = generateName("machine-auth-mapi-del-")
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthMAPINameDelete, mapiv1beta1.MachineAuthorityMachineAPI)
					newCapiMachine = capiframework.GetMachine(newMapiMachine.Name, capiframework.CAPINamespace)
					verifyMachineRunning(cl, newMapiMachine)

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
						ObjectMeta: metav1.ObjectMeta{Name: mapiMachineAuthMAPINameDelete, Namespace: capiframework.CAPINamespace},
					})
				})
			})
			Context("when deleting the non-authoritative CAPI Machine", func() {
				BeforeAll(func() {
					mapiMachineAuthMAPINameDelete = generateName("machine-auth-mapi-del-")
					newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiMachineAuthMAPINameDelete, mapiv1beta1.MachineAuthorityMachineAPI)
					verifyMachineRunning(cl, newMapiMachine)
					newCapiMachine = capiframework.GetMachine(newMapiMachine.Name, capiframework.CAPINamespace)

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
						ObjectMeta: metav1.ObjectMeta{Name: mapiMachineAuthMAPINameDelete, Namespace: capiframework.CAPINamespace},
					})
				})
			})
		})
	})

	Describe("Machine Migration Round Trip Tests", Ordered, func() {
		var mapiCapiMapiRoundTripName string
		var newMapiMachine *mapiv1beta1.Machine
		var newCapiMachine *clusterv1.Machine

		Context("MAPI -> CAPI -> MAPI round trip", func() {
			BeforeAll(func() {
				mapiCapiMapiRoundTripName = generateName("machine-mapi-roundtrip-")
				By("Creating a MAPI machine with spec.authoritativeAPI: MachineAPI")
				newMapiMachine = createMAPIMachineWithAuthority(ctx, cl, mapiCapiMapiRoundTripName, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineRunning(cl, newMapiMachine)

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

			It("should complete MAPI -> CAPI -> MAPI round trip successfully", func() {
				By("Verifying a CAPI mirror machine is created")
				newCapiMachine = capiframework.GetMachine(mapiCapiMapiRoundTripName, capiframework.CAPINamespace)

				By("Verifying paused conditions and synchronised generation are set correctly")
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSynchronizedGeneration(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Updating spec.authoritativeAPI to ClusterAPI")
				updateMachineAuthoritativeAPI(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineRunning(cl, newCapiMachine)
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachineSynchronizedGeneration(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityClusterAPI)

				By("Updating spec.authoritativeAPI back to MachineAPI")
				updateMachineAuthoritativeAPI(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineRunning(cl, newMapiMachine)
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMAPIMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachineSynchronizedGeneration(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
				verifyMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

				By("Deleting MAPI machine and verifying mirrors are removed")
				Expect(mapiframework.DeleteMachines(ctx, cl, newMapiMachine)).To(Succeed())
				mapiframework.WaitForMachinesDeleted(cl, newMapiMachine)
				verifyResourceRemoved(newMapiMachine)
				verifyResourceRemoved(newCapiMachine)
				verifyResourceRemoved(&awsv1.AWSMachine{
					TypeMeta:   metav1.TypeMeta{Kind: "AWSMachine", APIVersion: awsv1.GroupVersion.String()},
					ObjectMeta: metav1.ObjectMeta{Name: mapiCapiMapiRoundTripName, Namespace: capiframework.CAPINamespace},
				})
			})
		})
	})
})

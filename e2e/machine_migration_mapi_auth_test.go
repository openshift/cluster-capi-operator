/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Migration MAPI Authoritative Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS for now", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Create standalone MAPI Machine", Ordered, func() {
		var mapiMachineAuthMAPIName = "machine-authoritativeapi-mapi"
		var newCapiMachine *clusterv1.Machine
		var newMapiMachine *mapiv1beta1.Machine
		var err error

		Context("With spec.authoritativeAPI: MachineAPI and no existing CAPI Machine with that name", func() {
			BeforeAll(func() {
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

			It("should find MAPI Machine .status.authoritativeAPI to equal MachineAPI", func() {
				verifyMachineAuthoritative(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
			It("should verify MAPI Machine Synchronized condition is True", func() {
				verifyMachineSynchronizedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
			It("should verify MAPI Machine Paused condition is False", func() {
				verifyMAPIMachinePausedCondition(newMapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
			It("should verify that the MAPI Machine has a CAPI Machine", func() {
				Eventually(func() error {
					newCapiMachine, err = capiframework.GetMachine(cl, mapiMachineAuthMAPIName, capiframework.CAPINamespace)
					return err
				}, capiframework.WaitMedium, capiframework.RetryMedium).Should(Succeed(), "CAPI Machine should exist")
			})
			It("should verify CAPI Machine Paused condition is True", func() {
				verifyCAPIMachinePausedCondition(newCapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})
	})
})

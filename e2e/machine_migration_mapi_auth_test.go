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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Machine Migration Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS for now", platform))
		}

		if !framework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}
	})

	var _ = Describe("Create standalone MAPI Machine", Ordered, func() {
		Context("With spec.authoritativeAPI: MachineAPI and no existing CAPI Machine with that name", func() {
			var newMachine *machinev1.Machine
			// This cleanup runs after all test specs in this context.
			AfterAll(func() {
				By("Cleaning up created machine")
				// If a new machine was created in the test, delete it.
				if newMachine != nil {
					// Try to delete the machine, but don't fail if it's already gone
					err := cl.Delete(context.Background(), newMachine)
					if err != nil && !apierrors.IsNotFound(err) {
						Expect(err).NotTo(HaveOccurred(), "Failed to delete machine")
					}

					// Wait for the machine to be fully deleted (if it wasn't already)
					key := client.ObjectKey{Name: newMachine.Name, Namespace: newMachine.Namespace}
					Eventually(func() bool {
						err := cl.Get(context.Background(), key, &machinev1.Machine{})
						return apierrors.IsNotFound(err)
					}, DefaultTimeout, DefaultInterval).Should(BeTrue(), "Eventually the machine should not be found")
				}
			})
			It("should create MAPI Machine and find its status.authoritativeAPI: MachineAPI", func() {
				newMachine = verifyMapiAutoritative(cl)
			})
			It("should verify that MAPI Machine Synchronized condition is True, Paused condition is False and status.authoritativeAPI: MachineAPI", func() {
				verifyMapiMachineSynchronizedPaused(newMachine)
			})
			It("should verify that the MAPI Machine has a CAPI Machine and CAPI Infra Machine mirrors, both with Paused condition True", func() {
				verifyCapiMachinePaused(cl, newMachine.Name)
			})
		})
	})
})

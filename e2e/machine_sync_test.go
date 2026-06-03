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
	"github.com/openshift/api/features"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/controllers/external"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("Machine Sync", Ordered, func() {
	BeforeAll(func() {
		switch platform {
		case configv1.AWSPlatformType, configv1.OpenStackPlatformType:
			// supported
		default:
			Skip(fmt.Sprintf("Machine sync is not supported on %s", platform))
		}

		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateMachineAPIMigration) {
			Skip("MachineAPIMigration feature gate is not enabled")
		}
	})

	It("should synchronize a MAPI Machine to CAPI with a stable Synchronized condition", func() {
		machineName := generateName("machine-sync-")
		mapiMachine := createMAPIMachineWithAuthority(ctx, cl, machineName, mapiv1beta1.MachineAuthorityMachineAPI)

		DeferCleanup(func() {
			cleanupMachineResources(ctx, cl,
				[]*clusterv1.Machine{{ObjectMeta: metav1.ObjectMeta{Name: machineName, Namespace: framework.CAPINamespace}}},
				[]*mapiv1beta1.Machine{mapiMachine},
			)
		})

		By("Verifying MAPI Machine Synchronized condition is True")
		verifyMAPIMachineSynchronizedCondition(mapiMachine, mapiv1beta1.MachineAuthorityMachineAPI)

		By("Verifying the CAPI Machine was created")
		capiMachine := framework.GetMachineWithRetry(machineName, framework.CAPINamespace)
		Expect(capiMachine).NotTo(BeNil())

		By("Fetching the infrastructure machine")
		infraMachine, err := external.GetObjectFromContractVersionedRef(ctx, cl, capiMachine.Spec.InfrastructureRef, capiMachine.Namespace)
		Expect(err).NotTo(HaveOccurred())
		infraMachineUID := infraMachine.GetUID()

		By("Verifying the Synchronized condition and infrastructure machine remain stable")
		Consistently(func(g Gomega) {
			g.Expect(komega.Get(mapiMachine)()).To(Succeed())
			g.Expect(mapiMachine.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(SynchronizedCondition)),
				HaveField("Status", Equal(corev1.ConditionTrue)),
			)))

			freshInfraMachine, err := external.GetObjectFromContractVersionedRef(ctx, cl, capiMachine.Spec.InfrastructureRef, capiMachine.Namespace)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(freshInfraMachine.GetUID()).To(Equal(infraMachineUID),
				"infrastructure machine UID changed — was deleted and recreated")
		}, "30s", "5s").Should(Succeed())
	})
})

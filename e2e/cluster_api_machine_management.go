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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIMachineManagement] Cluster API Machine Management", func() {
	BeforeEach(func() {
		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagement) {
			Skip("Feature gate ClusterAPIMachineManagement is not enabled.")
		}
	})

	Context("Operator & controller deployments", func() {
		It("should have the capi-operator deployment available", func() {
			assertDeploymentAvailable("capi-operator", framework.CAPIOperatorNamespace)
		})

		It("should have the cluster-api ClusterOperator reporting healthy", func() {
			co := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: framework.CAPIClusterOperatorName}}
			Eventually(komega.Object(co)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(SatisfyAll(
				HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", Equal(configv1.OperatorAvailable)),
					HaveField("Status", Equal(configv1.ConditionTrue)),
				))),
				HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", Equal(configv1.OperatorDegraded)),
					HaveField("Status", Equal(configv1.ConditionFalse)),
				))),
				HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", Equal(configv1.OperatorProgressing)),
					HaveField("Status", Equal(configv1.ConditionFalse)),
				))),
			))
		})

		It("should have the capi-controllers deployment available", func() {
			assertDeploymentAvailable("capi-controllers", framework.CAPINamespace)
		})

		It("should have the capi-installer deployment available", func() {
			assertDeploymentAvailable("capi-installer", framework.CAPIOperatorNamespace)
		})
	})

	Context("CRD & API readiness", func() {
		It("should have core Cluster API CRDs installed and established", func() {
			for _, name := range []string{
				"clusters.cluster.x-k8s.io",
				"machines.cluster.x-k8s.io",
				"machinesets.cluster.x-k8s.io",
			} {
				crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: name}}
				Eventually(komega.Object(crd)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
					HaveField("Status.Conditions", ContainElement(SatisfyAll(
						HaveField("Type", Equal(apiextensionsv1.Established)),
						HaveField("Status", Equal(apiextensionsv1.ConditionTrue)),
					))),
					"CRD %s should be established", name,
				)
			}
		})
	})

	Context("Management cluster resources", func() {
		It("should have the management cluster kubeconfig Secret present", func() {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-kubeconfig", clusterName),
				Namespace: framework.CAPINamespace,
			}}
			Eventually(komega.Object(secret)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
				HaveField("Data", HaveKey("value")),
			)
		})
	})

})

func assertDeploymentAvailable(name, namespace string) {
	GinkgoHelper()
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	Eventually(komega.Object(deployment)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
		HaveField("Status.Conditions", ContainElement(SatisfyAll(
			HaveField("Type", Equal(appsv1.DeploymentAvailable)),
			HaveField("Status", Equal(corev1.ConditionTrue)),
		))),
	)
}

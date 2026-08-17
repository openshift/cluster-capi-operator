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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIMachineManagementAWS] Cluster API Machine Management AWS",
	Label("platform:aws"),
	Label("skip-topology:External"),
	func() {
		BeforeEach(func() {
			if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
				Skip("Cluster API Machine Management AWS tests are not supported on External topology clusters.")
			}
			if platform != configv1.AWSPlatformType {
				Skip("Skipping AWS-specific tests on non-AWS platform.")
			}
			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagementAWS) {
				Skip("Feature gate ClusterAPIMachineManagementAWS is not enabled.")
			}
		})

		Context("AWS provider deployment", func() {
			It("should have the capa-controller-manager deployment available", func() {
				framework.AssertDeploymentAvailable("capa-controller-manager", framework.CAPINamespace)
			})
		})

		Context("AWS infrastructure CRDs", func() {
			DescribeTable("should have core AWS Cluster API CRDs installed and established",
				func(name string) {
					crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: name}}
					Eventually(komega.Object(crd)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
						HaveField("Status.Conditions", test.HaveCondition(apiextensionsv1.Established).WithStatus(apiextensionsv1.ConditionTrue)),
					)
				},
				Entry("awsclusters CRD", "awsclusters.infrastructure.cluster.x-k8s.io"),
				Entry("awsmachines CRD", "awsmachines.infrastructure.cluster.x-k8s.io"),
				Entry("awsmachinetemplates CRD", "awsmachinetemplates.infrastructure.cluster.x-k8s.io"),
				Entry("awsclustercontrolleridentities CRD", "awsclustercontrolleridentities.infrastructure.cluster.x-k8s.io"),
			)

		})

		Context("Management cluster AWS resources", func() {
			It("should have the AWSCluster object ready", func() {
				awsCluster := &awsv1.AWSCluster{ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: framework.CAPINamespace,
				}}
				Eventually(komega.Object(awsCluster)).WithTimeout(framework.WaitLong).WithPolling(framework.RetryMedium).Should(
					HaveField("Status.Ready", BeTrue()),
				)
			})
		})
	})

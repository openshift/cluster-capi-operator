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
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIMachineManagementOpenStack] Cluster API Machine Management OpenStack",
	Label("platform:openstack"),
	Label("skip-topology:External"),
	func() {
		BeforeEach(func() {
			if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
				Skip("Cluster API Machine Management OpenStack tests are not supported on External topology clusters.")
			}
			if platform != configv1.OpenStackPlatformType {
				Skip("Skipping OpenStack-specific tests on non-OpenStack platform.")
			}
			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagementOpenStack) {
				Skip("Feature gate ClusterAPIMachineManagementOpenStack is not enabled.")
			}
		})

		Context("OpenStack provider deployment", func() {
			It("should have the capo-controller-manager deployment available", func() {
				framework.AssertDeploymentAvailable("capo-controller-manager", framework.CAPINamespace)
			})
		})

		Context("OpenStack infrastructure CRDs", func() {
			DescribeTable("should have core OpenStack Cluster API CRDs installed and established",
				func(name string) {
					crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: name}}
					Eventually(komega.Object(crd)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
						HaveField("Status.Conditions", test.HaveCondition(apiextensionsv1.Established).WithStatus(apiextensionsv1.ConditionTrue)),
					)
				},
				Entry("openstackclusters CRD", "openstackclusters.infrastructure.cluster.x-k8s.io"),
				Entry("openstackmachines CRD", "openstackmachines.infrastructure.cluster.x-k8s.io"),
				Entry("openstackmachinetemplates CRD", "openstackmachinetemplates.infrastructure.cluster.x-k8s.io"),
				Entry("openstackclustertemplates CRD", "openstackclustertemplates.infrastructure.cluster.x-k8s.io"),
				Entry("openstackservers CRD", "openstackservers.infrastructure.cluster.x-k8s.io"),
			)
		})

		Context("Management cluster OpenStack resources", func() {
			It("should have the OpenStackCluster object ready", func() {
				openStackCluster := &openstackv1.OpenStackCluster{ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: framework.CAPINamespace,
				}}
				Eventually(komega.Object(openStackCluster)).WithTimeout(framework.WaitLong).WithPolling(framework.RetryMedium).Should(
					HaveField("Status.Ready", BeTrue()), "should have ready in status: %w", openStackCluster.Status,
				)
			})
		})
	})

var _ = Describe("[OTP][Jira:OCPCLOUD][OCPFeatureGate:ClusterAPIMachineManagementOpenStack][sig-cluster-lifecycle] Cluster API Secret Sync",
	Label("platform:openstack"),
	func() {
		BeforeEach(func() {
			if platform != configv1.OpenStackPlatformType {
				Skip("Skipping OpenStack-specific tests on non-OpenStack platform.")
			}
			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagementOpenStack) {
				Skip("Feature gate ClusterAPIMachineManagementOpenStack is not enabled.")
			}
		})

		It("should re-sync worker-user-data secret after deletion", Label("Disruptive"), Label("Lifecycle:informing"), secretSyncTest)
	})

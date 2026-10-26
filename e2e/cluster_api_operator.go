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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIOperator] Cluster API Operator", Label("skip-topology:External"), func() {
	BeforeEach(func() {
		if IsMicroShift {
			Skip("Cluster API is not supported on MicroShift")
		}
		if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
			Skip("Cluster API Operator tests are not supported on External topology clusters.")
		}
		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIOperator) {
			Skip("Feature gate ClusterAPIOperator is not enabled.")
		}
	})

	// Operator health is required with or without machine management enabled.
	Context("Operator deployments and status", func() {
		It("should have the capi-operator deployment available", func() {
			framework.AssertDeploymentAvailable("capi-operator", framework.CAPIOperatorNamespace)
		})

		It("should have the cluster-api ClusterOperator reporting healthy", func() {
			co := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: framework.CAPIClusterOperatorName}}
			Eventually(komega.Object(co)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(SatisfyAll(
				HaveField("Status.Conditions", test.HaveCondition(configv1.OperatorAvailable).WithStatus(configv1.ConditionTrue)),
				HaveField("Status.Conditions", test.HaveCondition(configv1.OperatorDegraded).WithStatus(configv1.ConditionFalse)),
				HaveField("Status.Conditions", test.HaveCondition(configv1.OperatorProgressing).WithStatus(configv1.ConditionFalse)),
			))
		})

		It("should have the capi-installer deployment available", func() {
			framework.AssertDeploymentAvailable("capi-installer", framework.CAPIOperatorNamespace)
		})
	})
})

/*
Copyright 2024 Red Hat, Inc.

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

package unsupported

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

var _ = Describe("CAPI unsupported controller", func() {
	ctx := context.Background()
	desiredOperatorReleaseVersion := "this-is-the-desired-release-version"
	var r *UnsupportedController
	var capiClusterOperator *configv1.ClusterOperator
	var testNamespaceName string

	BeforeEach(func() {
		r = &UnsupportedController{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client:         cl,
				ReleaseVersion: desiredOperatorReleaseVersion,
			},
		}

		By("Creating the cluster-api ClusterOperator")
		capiClusterOperator = &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster-api",
			},
		}
		Expect(cl.Create(ctx, capiClusterOperator)).To(Succeed(), "should be able to create the 'cluster-api' ClusterOperator object")

		By("Creating the testing namespace")
		namespace := corev1resourcebuilder.Namespace().WithGenerateName("test-capi-corecluster-").Build()
		Expect(cl.Create(ctx, namespace)).To(Succeed())
		testNamespaceName = namespace.Name
	})

	AfterEach(func() {
		testutils.CleanupResources(Default, ctx, testEnv.Config, cl, testNamespaceName, &configv1.ClusterOperator{})
	})

	It("should update cluster-api ClusterOperator status with an 'unsupported' message", func() {
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: capiClusterOperator.Name,
			},
		})
		Expect(err).ToNot(HaveOccurred(), "should be able to reconcile the cluster-api ClusterOperator without erroring")

		co := komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())
		Eventually(co).Should(HaveField("Status.Conditions",
			SatisfyAll(
				ContainElement(And(HaveField("Type", Equal(configv1.OperatorAvailable)), HaveField("Status", Equal(configv1.ConditionTrue)), HaveField("Message", Equal(capiUnsupportedPlatformMsg)))),
				ContainElement(And(HaveField("Type", Equal(configv1.OperatorProgressing)), HaveField("Status", Equal(configv1.ConditionFalse)))),
				ContainElement(And(HaveField("Type", Equal(configv1.OperatorDegraded)), HaveField("Status", Equal(configv1.ConditionFalse)))),
				ContainElement(And(HaveField("Type", Equal(configv1.OperatorUpgradeable)), HaveField("Status", Equal(configv1.ConditionTrue)))),
			),
		), "should match the expected ClusterOperator status conditions")

	})

	It("should update the ClusterOperator status version to the desired one", func() {
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: capiClusterOperator.Name,
			},
		})
		Expect(err).ToNot(HaveOccurred(), "should be able to reconcile the cluster-api ClusterOperator without erroring")

		co := komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())
		Eventually(co).Should(
			HaveField("Status.Versions", ContainElement(SatisfyAll(
				HaveField("Name", Equal("operator")),
				HaveField("Version", Equal(desiredOperatorReleaseVersion)),
			))),
		)
	})

	It("should update the ClusterOperator status version to the desired one when an incorrect one is present", func() {
		By("setting the ClusterOperator status version to an incorrect one")
		patchBase := client.MergeFrom(capiClusterOperator.DeepCopy())
		capiClusterOperator.Status.Versions = []configv1.OperandVersion{{Name: "operator", Version: "incorrect"}}
		Expect(cl.Status().Patch(ctx, capiClusterOperator, patchBase)).To(Succeed())

		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: capiClusterOperator.Name,
			},
		})
		Expect(err).ToNot(HaveOccurred(), "should be able to reconcile the cluster-api ClusterOperator without erroring")

		co := komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())
		Eventually(co).Should(
			HaveField("Status.Versions", ContainElement(SatisfyAll(
				HaveField("Name", Equal("operator")),
				HaveField("Version", Equal(desiredOperatorReleaseVersion)),
			))),
		)
	})
})

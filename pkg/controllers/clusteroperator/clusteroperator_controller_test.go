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

package clusteroperator

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const desiredOperatorReleaseVersion = "this-is-the-desired-release-version"

var (
	mgrCancel context.CancelFunc
	mgrDone   chan struct{}
)

var _ = Describe("ClusterOperator controller", func() {
	ctx := context.Background()
	var capiClusterOperator *configv1.ClusterOperator
	var testNamespaceName string

	BeforeEach(func() {
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

	Context("With a supported platform", func() {
		JustBeforeEach(func() {
			mgrCancel, mgrDone = startManager(false)
		})

		JustAfterEach(func() {
			stopManager()
		})

		It("should update the ClusterOperator status with the running version", func() {
			co := komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())
			Eventually(co).Should(HaveField("Status.Conditions",
				SatisfyAll(
					ContainElement(And(HaveField("Type", Equal(configv1.OperatorAvailable)), HaveField("Status", Equal(configv1.ConditionTrue)),
						HaveField("Message", Equal(fmt.Sprintf("Cluster CAPI Operator is available at %s", desiredOperatorReleaseVersion))))),
					ContainElement(And(HaveField("Type", Equal(configv1.OperatorProgressing)), HaveField("Status", Equal(configv1.ConditionFalse)))),
					ContainElement(And(HaveField("Type", Equal(configv1.OperatorDegraded)), HaveField("Status", Equal(configv1.ConditionFalse)))),
					ContainElement(And(HaveField("Type", Equal(configv1.OperatorUpgradeable)), HaveField("Status", Equal(configv1.ConditionTrue)))),
				),
			), "should match the expected ClusterOperator status conditions")

		})

		It("should update the ClusterOperator status version to the desired one", func() {
			Eventually(komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build()), time.Second*10).Should(
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

			co := komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())
			Eventually(co).Should(
				HaveField("Status.Versions", ContainElement(SatisfyAll(
					HaveField("Name", Equal("operator")),
					HaveField("Version", Equal(desiredOperatorReleaseVersion)),
				))),
			)
		})
	})

	Context("With an unsupported platform", func() {
		JustBeforeEach(func() {
			mgrCancel, mgrDone = startManager(true)
		})

		JustAfterEach(func() {
			stopManager()
		})

		It("should update the ClusterOperator status with an 'unsupported' message", func() {
			Eventually(komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())).
				Should(HaveField("Status.Conditions", SatisfyAll(
					ContainElement(And(HaveField("Type", Equal(configv1.OperatorAvailable)), HaveField("Status", Equal(configv1.ConditionTrue)),
						HaveField("Message", Equal("Cluster API is not yet implemented on this platform")))),
					ContainElement(And(HaveField("Type", Equal(configv1.OperatorProgressing)), HaveField("Status", Equal(configv1.ConditionFalse)))),
					ContainElement(And(HaveField("Type", Equal(configv1.OperatorDegraded)), HaveField("Status", Equal(configv1.ConditionFalse)))),
					ContainElement(And(HaveField("Type", Equal(configv1.OperatorUpgradeable)), HaveField("Status", Equal(configv1.ConditionTrue)))),
				)), "should match the expected ClusterOperator status conditions")
		})

		It("should update the ClusterOperator status version to the desired one", func() {
			Eventually(komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())).
				Should(HaveField("Status.Versions", ContainElement(SatisfyAll(
					HaveField("Name", Equal("operator")),
					HaveField("Version", Equal(desiredOperatorReleaseVersion)),
				))), "should match the expected ClusterOperator status versions")
		})

		It("should update the ClusterOperator status version to the desired one when an incorrect one is present", func() {
			By("Setting the ClusterOperator status version to an incorrect one")
			patchBase := client.MergeFrom(capiClusterOperator.DeepCopy())
			capiClusterOperator.Status.Versions = []configv1.OperandVersion{{Name: "operator", Version: "incorrect"}}
			Expect(cl.Status().Patch(ctx, capiClusterOperator, patchBase)).To(Succeed())

			By("Checking the conditions are as expected")
			Eventually(komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())).
				Should(HaveField("Status.Versions", ContainElement(SatisfyAll(
					HaveField("Name", Equal("operator")),
					HaveField("Version", Equal(desiredOperatorReleaseVersion)),
				))))
		})
	})
})

func startManager(isUnsupportedPlatform bool) (context.CancelFunc, chan struct{}) {
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	mgrDone := make(chan struct{})

	By("Setting up a manager and controller")

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  testScheme,
		Metrics: server.Options{BindAddress: "0"},
		Controller: config.Controller{
			SkipNameValidation: ptr.To(true),
		},
	})
	Expect(err).ToNot(HaveOccurred(), "Manager should be able to be created")

	r := &ClusterOperatorController{
		ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{Client: cl, ReleaseVersion: desiredOperatorReleaseVersion},
		IsUnsupportedPlatform:       isUnsupportedPlatform,
	}
	Expect(r.SetupWithManager(mgr)).To(Succeed(), "Reconciler should be able to setup with manager")

	By("Starting the manager")

	go func() {
		defer GinkgoRecover()
		defer close(mgrDone)

		Expect((mgr).Start(mgrCtx)).To(Succeed())
	}()

	return mgrCancel, mgrDone
}

func stopManager() {
	By("Stopping the manager")
	mgrCancel()
	Eventually(mgrDone).Should(BeClosed())
}

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
	"slices"

	"github.com/go-logr/logr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
)

const (
	desiredOperatorReleaseVersion = "this-is-the-desired-release-version"
)

var (
	mgrCancel context.CancelFunc
	mgrDone   chan struct{}
)

var _ = Describe("Upgradeable condition ownership", func() {
	It("should relinquish the placeholder and preserve the assessment controller's verdict", func(ctx context.Context) {
		co := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: controllers.ClusterOperatorName}}
		Expect(cl.Create(ctx, co)).To(Succeed())
		DeferCleanup(func(ctx context.Context) { Expect(cl.Delete(ctx, co)).To(Succeed()) })

		r := &ClusterOperatorController{
			Client: cl, FeatureGates: newFeatureGate(features.FeatureGateCRDCompatibilityRequirementOperator),
			Infra: configv1resourcebuilder.Infrastructure().AsAWS("test", "eu-west-2").Build(),
		}

		By("simulating the old field owner's placeholder")

		conditions := r.aggregatedStatus(nil)
		conditions = append(conditions, condition(configv1.OperatorUpgradeable, configv1.ConditionTrue, operatorstatus.ReasonAsExpected, ""))
		operatorstatus.MergeConditions(conditions, nil)
		Expect(r.writeStatus(ctx, co, conditions)).To(Succeed())
		_, err := r.Reconcile(ctx, ctrl.Request{})
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
		Expect(co.Status.Conditions).NotTo(ContainElement(HaveField("Type", Equal(configv1.OperatorUpgradeable))))
		By("writing an assessment verdict and reconciling the health aggregator")

		result := upgradeable.UpgradeableResult{ReconcileResult: upgradeable.ResultGenerator.Success()}.
			WithUpgradeable(configv1.ConditionFalse, "NotCompatible", "Incompatible CRDs: widgets.example.com")
		Expect(result.WriteClusterOperatorStatus(ctx, logr.Discard(), cl)).To(Succeed())
		_, err = r.Reconcile(ctx, ctrl.Request{})
		Expect(err).NotTo(HaveOccurred())
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
		Expect(co.Status.Conditions).To(ContainElement(SatisfyAll(HaveField("Type", Equal(configv1.OperatorUpgradeable)), HaveField("Status", Equal(configv1.ConditionFalse)))))
	})
})

var _ = Describe("ClusterOperator controller", func() {
	Context("with a supported platform", func() {
		var capiClusterOperator *configv1.ClusterOperator

		infra := configv1resourcebuilder.Infrastructure().AsAWS("test", "eu-west-2").Build()

		BeforeEach(func(ctx context.Context) {
			By("Creating the cluster-api ClusterOperator with a previous release version", func() {
				capiClusterOperator = &configv1.ClusterOperator{
					ObjectMeta: metav1.ObjectMeta{
						Name: controllers.ClusterOperatorName,
					},
				}
				Expect(cl.Create(ctx, capiClusterOperator)).To(Succeed())
				DeferCleanup(func(ctx context.Context) {
					testutils.CleanupResources(Default, ctx, testEnv.Config, cl, "", &configv1.ClusterOperator{})
				})
			})
		}, defaultNodeTimeout)

		DescribeTable("aggregated status conditions",
			func(ctx context.Context, gates featuregates.FeatureGate, subConditions []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration,
				expectedAvailable, expectedProgressing *test.ConditionMatcher) {
				mgrCancel, mgrDone = startManager(gates, infra)

				DeferCleanup(stopManager)

				if len(subConditions) > 0 {
					patchSubConditions(ctx, capiClusterOperator, subConditions...)
				}

				co := kWithCtx(ctx).Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())

				Eventually(co).
					WithContext(ctx).
					WithTimeout(defaultEventuallyTimeout).
					Should(SatisfyAll(
						HaveField("Status.Conditions", SatisfyAll(
							expectedAvailable,
							expectedProgressing,
							test.HaveCondition(configv1.OperatorDegraded).WithStatus(configv1.ConditionFalse),
							Not(test.HaveCondition(configv1.OperatorUpgradeable)),
						)),
					))
			},
			Entry("when all sub-controllers report success",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				allSubControllersSuccessful(),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonAsExpected),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonAsExpected),
				defaultNodeTimeout),
			Entry("when installer controller is progressing but was previously available",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withOverrides(allSubControllersSuccessful(),
					subCondition("InstallerControllerProgressing", configv1.ConditionTrue, operatorstatus.ReasonProgressing, "Installing components"),
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonAsExpected),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonProgressing).
					WithMessage(ContainSubstring("InstallerController")),
				defaultNodeTimeout),
			Entry("when revision controller is progressing but was previously available",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withOverrides(allSubControllersSuccessful(),
					subCondition("RevisionControllerProgressing", configv1.ConditionTrue, operatorstatus.ReasonProgressing, "Updating revisions"),
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonAsExpected),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonProgressing).
					WithMessage(ContainSubstring("RevisionController")),
				defaultNodeTimeout),
			Entry("when both installer and revision controllers are progressing but were previously available",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withOverrides(allSubControllersSuccessful(),
					subCondition("InstallerControllerProgressing", configv1.ConditionTrue, operatorstatus.ReasonProgressing, "Installing components"),
					subCondition("RevisionControllerProgressing", configv1.ConditionTrue, operatorstatus.ReasonProgressing, "Updating revisions"),
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonAsExpected),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonProgressing).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
					)),
				defaultNodeTimeout),
			Entry("when installer controller has a non-retryable error",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withOverrides(allSubControllersSuccessful(),
					subCondition("InstallerControllerAvailable", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "install failed"),
					subCondition("InstallerControllerProgressing", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "install failed"),
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(ContainSubstring("InstallerController")),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(ContainSubstring("InstallerController")),
				defaultNodeTimeout),
			Entry("when revision controller has a non-retryable error",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withOverrides(allSubControllersSuccessful(),
					subCondition("RevisionControllerAvailable", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "revision failed"),
					subCondition("RevisionControllerProgressing", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "revision failed"),
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(ContainSubstring("RevisionController")),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(ContainSubstring("RevisionController")),
				defaultNodeTimeout),
			Entry("when installer controller has an ephemeral error but was previously available",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withOverrides(allSubControllersSuccessful(),
					subCondition("InstallerControllerProgressing", configv1.ConditionTrue, operatorstatus.ReasonEphemeralError, "transient failure"),
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonAsExpected),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonEphemeralError).
					WithMessage(ContainSubstring("InstallerController")),
				defaultNodeTimeout),
			Entry("when installer controller sub-conditions are missing",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withoutConditions(allSubControllersSuccessful(),
					"InstallerControllerAvailable",
					"InstallerControllerProgressing",
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(ContainSubstring("InstallerController")),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(ContainSubstring("InstallerController")),
				defaultNodeTimeout),
			Entry("when all sub-conditions are missing",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{},
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
						ContainSubstring("CoreClusterController"),
						ContainSubstring("InfraClusterController"),
						ContainSubstring("SecretSyncController"),
						ContainSubstring("initializing"),
					)),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
						ContainSubstring("CoreClusterController"),
						ContainSubstring("InfraClusterController"),
						ContainSubstring("SecretSyncController"),
						ContainSubstring("initializing"),
					)),
				defaultNodeTimeout),
			Entry("when installer has not yet reported available during initial install",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{
					subCondition("InstallerControllerProgressing", configv1.ConditionTrue, operatorstatus.ReasonProgressing, "Installing components"),
				},
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
					)),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonProgressing).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
					)),
				defaultNodeTimeout),
			Entry("when one controller has a non-retryable error and the other is progressing",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withOverrides(allSubControllersSuccessful(),
					subCondition("InstallerControllerAvailable", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "install failed"),
					subCondition("InstallerControllerProgressing", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "install failed"),
					subCondition("RevisionControllerProgressing", configv1.ConditionTrue, operatorstatus.ReasonProgressing, "Updating revisions"),
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(ContainSubstring("InstallerController")),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
					)),
				defaultNodeTimeout),
			Entry("when both installer and revision controllers have non-retryable errors",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withOverrides(allSubControllersSuccessful(),
					subCondition("InstallerControllerAvailable", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "install failed"),
					subCondition("InstallerControllerProgressing", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "install failed"),
					subCondition("RevisionControllerAvailable", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "revision failed"),
					subCondition("RevisionControllerProgressing", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "revision failed"),
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
					)),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
					)),
				defaultNodeTimeout),
			Entry("when installer is waiting on external",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{
					subCondition("InstallerControllerProgressing", configv1.ConditionTrue, operatorstatus.ReasonWaitingOnExternal, "Waiting on ClusterAPI"),
				},
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
					)),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonWaitingOnExternal).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
					)),
				defaultNodeTimeout),

			// Prioritisation tests: when sub-controllers report different reasons,
			// the aggregated condition should report the highest priority reason.
			Entry("should report the highest priority progressing reason",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withOverrides(allSubControllersSuccessful(),
					subCondition("InstallerControllerProgressing", configv1.ConditionTrue, operatorstatus.ReasonEphemeralError, "transient failure"),
					subCondition("RevisionControllerProgressing", configv1.ConditionTrue, operatorstatus.ReasonProgressing, "Updating revisions"),
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonAsExpected),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonEphemeralError).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
					)),
				defaultNodeTimeout),
			Entry("should report the highest priority available reason",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withOverrides(allSubControllersSuccessful(),
					subCondition("InstallerControllerAvailable", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "install failed"),
					subCondition("InstallerControllerProgressing", configv1.ConditionFalse, operatorstatus.ReasonNonRetryableError, "install failed"),
					subCondition("RevisionControllerAvailable", configv1.ConditionFalse, operatorstatus.ReasonUninitialized, ""),
					subCondition("RevisionControllerProgressing", configv1.ConditionTrue, operatorstatus.ReasonProgressing, "Updating revisions"),
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
					)),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonNonRetryableError).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
					)),
				defaultNodeTimeout),
			Entry("skips missing machine-management controller conditions when the ClusterAPIMachineManagement gate is disabled",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagementAWS),
				withoutConditions(allSubControllersSuccessful(),
					"CoreClusterControllerAvailable",
					"CoreClusterControllerProgressing",
					"InfraClusterControllerAvailable",
					"InfraClusterControllerProgressing",
					"SecretSyncControllerAvailable",
					"SecretSyncControllerProgressing",
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonAsExpected).
					WithMessage(Equal("Cluster API Operator is available")),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonAsExpected).
					WithMessage(BeEmpty()),
				defaultNodeTimeout),
			Entry("requires machine-management controller conditions when the ClusterAPIMachineManagement gate is enabled",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAWS),
				withoutConditions(allSubControllersSuccessful(),
					"CoreClusterControllerAvailable",
					"CoreClusterControllerProgressing",
					"InfraClusterControllerAvailable",
					"InfraClusterControllerProgressing",
					"SecretSyncControllerAvailable",
					"SecretSyncControllerProgressing",
				),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(ContainSubstring("CoreClusterController")),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(ContainSubstring("CoreClusterController")),
				defaultNodeTimeout),
			Entry("requires InstallerController conditions even when the ClusterAPIMachineManagement gate is disabled",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagementAWS),
				withoutConditions(allSubControllersSuccessful(), "InstallerControllerAvailable", "InstallerControllerProgressing"),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(ContainSubstring("InstallerController")),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(ContainSubstring("InstallerController")),
				defaultNodeTimeout),
			Entry("requires RevisionController conditions even when the ClusterAPIMachineManagement gate is disabled",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagementAWS),
				withoutConditions(allSubControllersSuccessful(), "RevisionControllerAvailable", "RevisionControllerProgressing"),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(ContainSubstring("RevisionController")),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(ContainSubstring("RevisionController")),
				defaultNodeTimeout),
		)
	})

	// Platforms without Cluster API machine management, for example IBM Cloud VPC,
	// do not start the machine management controllers, so their conditions never
	// appear. The aggregation must not wait for them. See OCPBUGS-135095.
	Context("with a platform that does not support Cluster API machine management", func() {
		var capiClusterOperator *configv1.ClusterOperator

		BeforeEach(func(ctx context.Context) {
			capiClusterOperator = &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{
					Name: controllers.ClusterOperatorName,
				},
			}
			Expect(cl.Create(ctx, capiClusterOperator)).To(Succeed())
			DeferCleanup(func(ctx context.Context) {
				testutils.CleanupResources(Default, ctx, testEnv.Config, cl, "", &configv1.ClusterOperator{})
			})
		}, defaultNodeTimeout)

		DescribeTable("should become available without the ancillary controller conditions",
			func(ctx context.Context, gates featuregates.FeatureGate, infra *configv1.Infrastructure) {
				mgrCancel, mgrDone = startManager(gates, infra)

				DeferCleanup(stopManager)

				patchSubConditions(ctx, capiClusterOperator, withoutConditions(allSubControllersSuccessful(),
					"CoreClusterControllerAvailable",
					"CoreClusterControllerProgressing",
					"InfraClusterControllerAvailable",
					"InfraClusterControllerProgressing",
					"SecretSyncControllerAvailable",
					"SecretSyncControllerProgressing",
				)...)

				co := kWithCtx(ctx).Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())

				Eventually(co).
					WithContext(ctx).
					WithTimeout(defaultEventuallyTimeout).
					Should(HaveField("Status.Conditions", SatisfyAll(
						test.HaveCondition(configv1.OperatorAvailable).
							WithStatus(configv1.ConditionTrue).
							WithReason(operatorstatus.ReasonAsExpected),
						test.HaveCondition(configv1.OperatorProgressing).
							WithStatus(configv1.ConditionFalse).
							WithReason(operatorstatus.ReasonAsExpected),
						test.HaveCondition(configv1.OperatorDegraded).WithStatus(configv1.ConditionFalse),
					)))
			},
			Entry("on IBM Cloud VPC",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement),
				configv1resourcebuilder.Infrastructure().
					WithPlatformStatus(configv1.PlatformStatus{Type: configv1.IBMCloudPlatformType}).
					Build(),
				defaultNodeTimeout),
			Entry("on Azure Stack Hub, where the Azure gate is enabled",
				newFeatureGate(features.FeatureGateClusterAPIMachineManagement, features.FeatureGateClusterAPIMachineManagementAzure),
				configv1resourcebuilder.Infrastructure().
					WithPlatformStatus(configv1.PlatformStatus{
						Type:  configv1.AzurePlatformType,
						Azure: &configv1.AzurePlatformStatus{CloudName: configv1.AzureStackCloud},
					}).
					Build(),
				defaultNodeTimeout),
		)
	})
})

// newFeatureGate constructs a FeatureGate with the Cluster API base and platform gates
// classified as enabled or disabled based on the provided list.
func newFeatureGate(enabled ...configv1.FeatureGateName) featuregates.FeatureGate {
	allGates := []configv1.FeatureGateName{
		features.FeatureGateCRDCompatibilityRequirementOperator,
		features.FeatureGateClusterAPIMachineManagement,
		features.FeatureGateClusterAPIMachineManagementAWS,
		features.FeatureGateClusterAPIMachineManagementAzure,
		features.FeatureGateClusterAPIMachineManagementBareMetal,
		features.FeatureGateClusterAPIMachineManagementGCP,
		features.FeatureGateClusterAPIMachineManagementPowerVS,
		features.FeatureGateClusterAPIMachineManagementOpenStack,
		features.FeatureGateClusterAPIMachineManagementVSphere,
	}

	var disabled []configv1.FeatureGateName

	for _, g := range allGates {
		if !slices.Contains(enabled, g) {
			disabled = append(disabled, g)
		}
	}

	return featuregates.NewFeatureGate(enabled, disabled)
}

func patchSubConditions(ctx context.Context, co *configv1.ClusterOperator, conditions ...*configv1apply.ClusterOperatorStatusConditionApplyConfiguration) {
	applyConfig := configv1apply.ClusterOperator(controllers.ClusterOperatorName).
		WithUID(co.UID).
		WithStatus(configv1apply.ClusterOperatorStatus().
			WithConditions(conditions...))

	Expect(cl.Status().Patch(ctx, co, util.ApplyConfigPatch(applyConfig),
		operatorstatus.CAPIFieldOwner("test-sub-conditions"), client.ForceOwnership)).To(Succeed())
}

func subCondition(condType string, status configv1.ConditionStatus, reason operatorstatus.Reason, message string) *configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	return configv1apply.ClusterOperatorStatusCondition().
		WithType(configv1.ClusterStatusConditionType(condType)).
		WithStatus(status).
		WithReason(reason.String()).
		WithMessage(message).
		WithLastTransitionTime(metav1.Now())
}

func startManager(gates featuregates.FeatureGate, infra *configv1.Infrastructure) (context.CancelFunc, chan struct{}) {
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
		Client:         cl,
		ReleaseVersion: desiredOperatorReleaseVersion,
		FeatureGates:   gates,
		Infra:          infra,
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

func stopManager(ctx context.Context) {
	By("Stopping the manager", func() {
		mgrCancel()
		Eventually(mgrDone).WithContext(ctx).WithTimeout(defaultEventuallyTimeout).Should(BeClosed())
	})
}

func allSubControllersSuccessful() []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	controllers := []string{
		"InstallerController",
		"RevisionController",
		"UpgradeableController",
		"CoreClusterController",
		"InfraClusterController",
		"SecretSyncController",
	}

	conds := make([]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration, 0, 2*len(controllers))
	for _, c := range controllers {
		conds = append(conds,
			subCondition(c+"Available", configv1.ConditionTrue, operatorstatus.ReasonAsExpected, "Success"),
			subCondition(c+"Progressing", configv1.ConditionFalse, operatorstatus.ReasonAsExpected, "Success"),
		)
	}

	return conds
}

func withoutConditions(base []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration, types ...string) []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	exclude := make(map[configv1.ClusterStatusConditionType]bool, len(types))
	for _, t := range types {
		exclude[configv1.ClusterStatusConditionType(t)] = true
	}

	result := make([]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration, 0, len(base))

	for _, c := range base {
		if !exclude[*c.Type] {
			result = append(result, c)
		}
	}

	return result
}

func withOverrides(base []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration, overrides ...*configv1apply.ClusterOperatorStatusConditionApplyConfiguration) []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	result := append([]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{}, base...)

	for _, override := range overrides {
		found := false

		for i, c := range result {
			if *c.Type == *override.Type {
				result[i] = override
				found = true

				break
			}
		}

		if !found {
			result = append(result, override)
		}
	}

	return result
}

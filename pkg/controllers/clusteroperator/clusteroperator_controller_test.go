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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	configv1 "github.com/openshift/api/config/v1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	desiredOperatorReleaseVersion = "this-is-the-desired-release-version"
)

var (
	mgrCancel context.CancelFunc
	mgrDone   chan struct{}
)

var _ = Describe("ClusterOperator controller", func() {
	Context("with a supported platform", func() {
		var capiClusterOperator *configv1.ClusterOperator

		BeforeEach(func(ctx context.Context) {
			mgrCancel, mgrDone = startManager(false)

			DeferCleanup(stopManager)

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
				Expect(cl.Status().Update(ctx, capiClusterOperator)).To(Succeed())
			})
		}, defaultNodeTimeout)

		DescribeTable("rollup aggregation",
			func(ctx context.Context, subConditions []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration,
				expectedAvailable, expectedProgressing *test.ConditionMatcher) {
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
							test.HaveCondition(configv1.OperatorUpgradeable).WithStatus(configv1.ConditionTrue),
						)),
					))
			},
			Entry("when all sub-controllers report success",
				allSubControllersSuccessful(),
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonAsExpected),
				test.HaveCondition(configv1.OperatorProgressing).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonAsExpected),
				defaultNodeTimeout),
			Entry("when installer controller is progressing but was previously available",
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
				[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{},
				test.HaveCondition(configv1.OperatorAvailable).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonUninitialized).
					WithMessage(SatisfyAll(
						ContainSubstring("InstallerController"),
						ContainSubstring("RevisionController"),
						ContainSubstring("CoreClusterController"),
						ContainSubstring("InfraClusterController"),
						ContainSubstring("KubeconfigController"),
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
						ContainSubstring("KubeconfigController"),
						ContainSubstring("SecretSyncController"),
						ContainSubstring("initializing"),
					)),
				defaultNodeTimeout),
			Entry("when installer has not yet reported available during initial install",
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
		)
	})

	Context("with an unsupported platform", Ordered, func() {
		var capiClusterOperator *configv1.ClusterOperator

		BeforeAll(func() {
			mgrCancel, mgrDone = startManager(true)

			DeferCleanup(stopManager)
		})

		BeforeEach(func(ctx context.Context) {
			By("Creating the cluster-api ClusterOperator", func() {
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

		It("should set Available=True with unsupported message and write versions without reading sub-conditions", func(ctx context.Context) {
			co := kWithCtx(ctx).Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())

			Eventually(co).
				WithContext(ctx).
				WithTimeout(defaultEventuallyTimeout).
				Should(SatisfyAll(
					HaveField("Status.Conditions", SatisfyAll(
						test.HaveCondition(configv1.OperatorAvailable).
							WithStatus(configv1.ConditionTrue).
							WithMessage(capiUnsupportedPlatformMsg),
						test.HaveCondition(configv1.OperatorProgressing).WithStatus(configv1.ConditionFalse),
						test.HaveCondition(configv1.OperatorDegraded).WithStatus(configv1.ConditionFalse),
						test.HaveCondition(configv1.OperatorUpgradeable).WithStatus(configv1.ConditionTrue),
					)),
					HaveField("Status.Versions", ContainElement(SatisfyAll(
						HaveField("Name", Equal(operatorstatus.OperatorVersionKey)),
						HaveField("Version", Equal(desiredOperatorReleaseVersion)),
					))),
				))
		}, defaultNodeTimeout)

		It("should update an incorrect version", func(ctx context.Context) {
			By("Setting the ClusterOperator status version to an incorrect one")

			patchBase := client.MergeFrom(capiClusterOperator.DeepCopy())
			capiClusterOperator.Status.Versions = []configv1.OperandVersion{{Name: operatorstatus.OperatorVersionKey, Version: "old"}}
			Expect(cl.Status().Patch(ctx, capiClusterOperator, patchBase)).To(Succeed())

			By("Checking the version is corrected")
			Eventually(kWithCtx(ctx).Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())).
				WithContext(ctx).
				WithTimeout(defaultEventuallyTimeout).
				Should(HaveField("Status.Versions", ContainElement(SatisfyAll(
					HaveField("Name", Equal(operatorstatus.OperatorVersionKey)),
					HaveField("Version", Equal(desiredOperatorReleaseVersion)),
				))))
		}, defaultNodeTimeout)
	})
})

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
		Client:                cl,
		ReleaseVersion:        desiredOperatorReleaseVersion,
		IsUnsupportedPlatform: isUnsupportedPlatform,
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
		"CoreClusterController",
		"InfraClusterController",
		"KubeconfigController",
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

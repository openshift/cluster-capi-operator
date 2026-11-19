/*
Copyright 2026 Red Hat, Inc.

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

package installer

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

var _ = Describe("InstallerController ClusterOperator progress reporting", func() {
	// A controller that publishes progress conditions can have a write skipped
	// against a stale informer cache, leaving a condition that no longer
	// reflects its state. Such a condition must be self-correcting: the
	// ClusterOperator update event has to requeue the controller.
	It("republishes progress conditions that a skipped status write left behind", func(ctx context.Context) {
		createFixtures(ctx)
		addRevisionAndWaitForSuccess(ctx, providerCore)

		progressingType := operatorstatus.ControllerResultGenerator(controllerName).
			SubConditionType(operatorstatus.ConditionProgressingSuffix)

		co := &configv1.ClusterOperator{}
		co.SetName(operatorstatus.ClusterOperatorName)

		By("checking controller has stopped writing status", func() {
			// We should have stopped writing immediately on revision success,
			// so this is just a short sanity check. A reconcile still in flight
			// would overwrite the simulated condition and mask the behaviour
			// under test.
			var quiescentRV string

			Eventually(ctx, func(g Gomega) {
				g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(co), co)).To(Succeed())
				g.Expect(co.Status.Conditions).To(
					test.HaveCondition(progressingType).WithStatus(configv1.ConditionFalse))
				quiescentRV = co.GetResourceVersion()
			}).WithContext(ctx).WithTimeout(defaultEventuallyTimeout).Should(Succeed())

			Consistently(ctx, func(g Gomega) {
				current := &configv1.ClusterOperator{}
				g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(co), current)).To(Succeed())
				g.Expect(current.GetResourceVersion()).To(Equal(quiescentRV))
			}).WithContext(ctx).WithTimeout(3 * time.Second).Should(Succeed())
		})

		By("simulating an EphemeralError condition left by a skipped status write", func() {
			// Apply the conditions with the controller's own SSA field owner, as the
			// controller would have after a reconciler error. The transition time is
			// deliberately old: republishing must record a fresh transition, not
			// preserve the stale one.
			staleErrorAt := metav1.NewTime(time.Now().Add(-time.Hour))
			controller := operatorstatus.ControllerResultGenerator(controllerName)

			applyConfig := configv1apply.ClusterOperator(operatorstatus.ClusterOperatorName).
				WithUID(co.GetUID()).
				WithStatus(configv1apply.ClusterOperatorStatus().
					WithConditions(
						configv1apply.ClusterOperatorStatusCondition().
							WithType(progressingType).
							WithStatus(configv1.ConditionTrue).
							WithReason(operatorstatus.ReasonEphemeralError.String()).
							WithMessage("writing current revision: updating ClusterAPI status: request did not complete within requested timeout").
							WithLastTransitionTime(staleErrorAt),
						configv1apply.ClusterOperatorStatusCondition().
							WithType(controller.SubConditionType(operatorstatus.ConditionAvailableSuffix)).
							WithStatus(configv1.ConditionTrue).
							WithReason(operatorstatus.ReasonAsExpected.String()).
							WithMessage("Success").
							WithLastTransitionTime(staleErrorAt),
					))

			Expect(cl.Status().Patch(ctx, co, util.ApplyConfigPatch(applyConfig),
				operatorstatus.CAPIFieldOwner(controllerName), client.ForceOwnership)).To(Succeed())
		})

		By("verifying the controller republishes its actual state without any other trigger", func() {
			Eventually(ctx, kWithCtx(ctx).Object(co)).
				WithTimeout(defaultEventuallyTimeout).
				Should(HaveField("Status.Conditions",
					test.HaveCondition(progressingType).
						WithStatus(configv1.ConditionFalse).
						WithReason(operatorstatus.ReasonAsExpected.String())))
		})

		By("verifying the republished condition records a new transition time", func() {
			Eventually(ctx, kWithCtx(ctx).Object(co)).
				WithTimeout(defaultEventuallyTimeout).
				Should(HaveField("Status.Conditions",
					test.HaveCondition(progressingType).WithLastTransitionTime(
						WithTransform(func(t metav1.Time) time.Time { return t.Time },
							BeTemporally("~", time.Now(), 10*time.Second)))))
		})
	}, defaultNodeTimeout)
})

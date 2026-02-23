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
package operatorstatus

import (
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

const testResultGenerator ControllerResultGenerator = "Test"

func TestSuccess(t *testing.T) {
	g := NewWithT(t)
	result := testResultGenerator.Success()

	g.Expect(result.progressing).To(test.BeCondition("TestProgressing").
		WithStatus(configv1.ConditionFalse).
		WithReason(ReasonAsExpected).
		WithMessage("Success"))

	g.Expect(result.degraded).To(test.BeCondition("TestDegraded").
		WithStatus(configv1.ConditionFalse).
		WithReason(ReasonAsExpected).
		WithMessage("Success"))

	g.Expect(result.err).To(BeNil())

	ctrlResult, err := result.Result()
	g.Expect(err).To(BeNil())
	g.Expect(ctrlResult).To(Equal(ctrl.Result{}))
}

func TestProgressing(t *testing.T) {
	g := NewWithT(t)
	result := testResultGenerator.Progressing("installing components")

	g.Expect(result.progressing).To(test.BeCondition("TestProgressing").
		WithStatus(configv1.ConditionTrue).
		WithReason(ReasonProgressing).
		WithMessage("installing components"))

	g.Expect(result.degraded).To(test.BeCondition("TestDegraded").
		WithStatus(configv1.ConditionFalse).
		WithReason(ReasonProgressing).
		WithMessage("installing components"))

	g.Expect(result.err).To(BeNil())
}

func TestWaitingOnExternal(t *testing.T) {
	g := NewWithT(t)
	result := testResultGenerator.WaitingOnExternal("infrastructure")

	g.Expect(result.progressing).To(test.BeCondition("TestProgressing").
		WithStatus(configv1.ConditionTrue).
		WithReason(ReasonWaitingOnExternal).
		WithMessage("Waiting on infrastructure"))

	g.Expect(result.degraded).To(test.BeCondition("TestDegraded").
		WithStatus(configv1.ConditionFalse).
		WithReason(ReasonWaitingOnExternal).
		WithMessage("Waiting on infrastructure"))

	g.Expect(result.err).To(BeNil())
}

func TestError(t *testing.T) {
	t.Run("non-terminal error", func(t *testing.T) {
		g := NewWithT(t)
		testErr := fmt.Errorf("connection refused")
		result := testResultGenerator.Error(testErr)

		g.Expect(result.progressing).To(test.BeCondition("TestProgressing").
			WithStatus(configv1.ConditionTrue).
			WithReason(ReasonEphemeralError).
			WithMessage("connection refused"))

		g.Expect(result.degraded).To(test.BeCondition("TestDegraded").
			WithStatus(configv1.ConditionFalse).
			WithReason(ReasonProgressing).
			WithMessage("Controller encountered a retryable error"))

		g.Expect(result.err).To(Equal(testErr))
		g.Expect(errors.Is(result.err, reconcile.TerminalError(nil))).To(BeFalse())
	})

	t.Run("terminal error", func(t *testing.T) {
		g := NewWithT(t)
		innerErr := fmt.Errorf("fatal")
		termErr := reconcile.TerminalError(innerErr)
		result := testResultGenerator.Error(termErr)

		g.Expect(result.progressing).To(test.BeCondition("TestProgressing").
			WithStatus(configv1.ConditionFalse).
			WithReason(ReasonNonRetryableError).
			WithMessage(termErr.Error()))

		g.Expect(result.degraded).To(test.BeCondition("TestDegraded").
			WithStatus(configv1.ConditionTrue).
			WithReason(ReasonNonRetryableError).
			WithMessage(termErr.Error()))

		g.Expect(errors.Is(result.err, reconcile.TerminalError(nil))).To(BeTrue())
	})
}

func TestNonRetryableError(t *testing.T) {
	t.Run("plain error", func(t *testing.T) {
		g := NewWithT(t)
		testErr := fmt.Errorf("bad config")
		result := testResultGenerator.NonRetryableError(testErr)

		g.Expect(result.progressing).To(test.BeCondition("TestProgressing").
			WithStatus(configv1.ConditionFalse).
			WithReason(ReasonNonRetryableError).
			WithMessage("terminal error: bad config"))

		g.Expect(result.degraded).To(test.BeCondition("TestDegraded").
			WithStatus(configv1.ConditionTrue).
			WithReason(ReasonNonRetryableError).
			WithMessage("terminal error: bad config"))

		g.Expect(errors.Is(result.err, reconcile.TerminalError(nil))).To(BeTrue())
	})

	t.Run("already-terminal error", func(t *testing.T) {
		g := NewWithT(t)
		innerErr := fmt.Errorf("already wrapped")
		termErr := reconcile.TerminalError(innerErr)
		result := testResultGenerator.NonRetryableError(termErr)

		g.Expect(result.progressing).To(test.BeCondition("TestProgressing").
			WithMessage("terminal error: already wrapped"))

		g.Expect(result.degraded).To(test.BeCondition("TestDegraded").
			WithMessage("terminal error: already wrapped"))

		g.Expect(errors.Is(result.err, reconcile.TerminalError(nil))).To(BeTrue())
		// The error should not be re-wrapped: result.err should be the same terminal error
		g.Expect(result.err).To(Equal(termErr))
	})
}

func TestWithRequeueAfter(t *testing.T) {
	t.Run("on Success", func(t *testing.T) {
		g := NewWithT(t)
		result := testResultGenerator.Success(WithRequeueAfter(5 * time.Second))

		g.Expect(result.requeueAfter).To(Equal(5 * time.Second))

		ctrlResult, err := result.Result()
		g.Expect(err).To(BeNil())
		g.Expect(ctrlResult.RequeueAfter).To(Equal(5 * time.Second))
	})

	t.Run("on Progressing", func(t *testing.T) {
		g := NewWithT(t)
		result := testResultGenerator.Progressing("working", WithRequeueAfter(10*time.Second))

		g.Expect(result.requeueAfter).To(Equal(10 * time.Second))
	})

	assertRequeueAfterWithError := func(g *WithT, result ReconcileResult, testErr error) {
		g.THelper()

		ctrlResult, err := result.Result()
		g.Expect(err).To(MatchError(testErr))

		// We should ignore requeueAfter when returning an error.
		g.Expect(ctrlResult.RequeueAfter).To(BeZero())
	}

	t.Run("on Error", func(t *testing.T) {
		g := NewWithT(t)
		testErr := fmt.Errorf("transient")
		result := testResultGenerator.Error(testErr, WithRequeueAfter(15*time.Second))

		assertRequeueAfterWithError(g, result, testErr)
	})

	t.Run("on NonRetryableError", func(t *testing.T) {
		g := NewWithT(t)
		testErr := fmt.Errorf("non-retryable")
		result := testResultGenerator.NonRetryableError(testErr, WithRequeueAfter(15*time.Second))

		assertRequeueAfterWithError(g, result, testErr)
	})
}

// applyCondition builds a ClusterOperatorStatusConditionApplyConfiguration for
// use in mergeConditions tests.
func applyCondition(condType configv1.ClusterStatusConditionType, status configv1.ConditionStatus, reason, message string) *configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	return configv1apply.ClusterOperatorStatusCondition().
		WithType(condType).
		WithStatus(status).
		WithReason(reason).
		WithMessage(message)
}

func TestMergeConditions(t *testing.T) {
	existingTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))

	type condFields struct {
		condType configv1.ClusterStatusConditionType
		status   configv1.ConditionStatus
		reason   string
		message  string
	}

	for _, tc := range []struct {
		name         string
		existing     condFields
		new          condFields
		wantUpdate   bool
		wantTimeSame bool
	}{
		{
			name:         "existing matches same Status Reason and Message",
			existing:     condFields{"Progressing", configv1.ConditionFalse, "AsExpected", "Success"},
			new:          condFields{"Progressing", configv1.ConditionFalse, "AsExpected", "Success"},
			wantUpdate:   false,
			wantTimeSame: true,
		},
		{
			name:         "different Message",
			existing:     condFields{"Progressing", configv1.ConditionTrue, "Working", "old message"},
			new:          condFields{"Progressing", configv1.ConditionTrue, "Working", "new message"},
			wantUpdate:   true,
			wantTimeSame: false,
		},
		{
			name:         "different Status",
			existing:     condFields{"Progressing", configv1.ConditionTrue, "Working", "busy"},
			new:          condFields{"Progressing", configv1.ConditionFalse, "Working", "done"},
			wantUpdate:   true,
			wantTimeSame: false,
		},
		{
			name:         "different Reason",
			existing:     condFields{"Degraded", configv1.ConditionTrue, "OldReason", "error"},
			new:          condFields{"Degraded", configv1.ConditionTrue, "NewReason", "error"},
			wantUpdate:   true,
			wantTimeSame: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			existing := []configv1.ClusterOperatorStatusCondition{
				{
					Type:               tc.existing.condType,
					Status:             tc.existing.status,
					Reason:             tc.existing.reason,
					Message:            tc.existing.message,
					LastTransitionTime: existingTime,
				},
			}

			cond := applyCondition(tc.new.condType, tc.new.status, tc.new.reason, tc.new.message)
			needsUpdate, _ := mergeConditions(
				[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{cond},
				existing,
			)

			g.Expect(needsUpdate).To(Equal(tc.wantUpdate))

			if tc.wantTimeSame {
				g.Expect(*cond.LastTransitionTime).To(Equal(existingTime))
			} else {
				g.Expect(*cond.LastTransitionTime).ToNot(Equal(existingTime))
			}
		})
	}

	t.Run("no existing conditions", func(t *testing.T) {
		g := NewWithT(t)

		cond := applyCondition("Progressing", configv1.ConditionFalse, "AsExpected", "Success")
		g.Expect(cond.LastTransitionTime).To(BeNil())

		needsUpdate, logConds := mergeConditions(
			[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{cond},
			nil,
		)

		g.Expect(needsUpdate).To(BeTrue())
		g.Expect(cond.LastTransitionTime).ToNot(BeNil())
		g.Expect(logConds).To(HaveLen(2))
		g.Expect(logConds[0]).To(Equal(configv1.ClusterStatusConditionType("Progressing")))
		g.Expect(logConds[1]).To(Equal(configv1.ConditionFalse))
	})

	t.Run("multiple conditions mixed", func(t *testing.T) {
		g := NewWithT(t)

		existing := []configv1.ClusterOperatorStatusCondition{
			{
				Type:               "Progressing",
				Status:             configv1.ConditionFalse,
				Reason:             "AsExpected",
				Message:            "Success",
				LastTransitionTime: existingTime,
			},
		}

		unchangedCond := applyCondition("Progressing", configv1.ConditionFalse, "AsExpected", "Success")
		newCond := applyCondition("Degraded", configv1.ConditionFalse, "AsExpected", "Success")

		needsUpdate, logConds := mergeConditions(
			[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{unchangedCond, newCond},
			existing,
		)

		g.Expect(needsUpdate).To(BeTrue())
		g.Expect(logConds).To(HaveLen(4))
		// Unchanged condition preserves its LastTransitionTime
		g.Expect(*unchangedCond.LastTransitionTime).To(Equal(existingTime))
		// New condition gets a LastTransitionTime set
		g.Expect(newCond.LastTransitionTime).ToNot(BeNil())
	})
}

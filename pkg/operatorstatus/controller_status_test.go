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

	testmatchers "github.com/openshift/cluster-capi-operator/pkg/test/matchers"
)

const (
	testPrefix     = "Test"
	testFieldOwner = "test-owner"
)

func TestNewControllerResultGenerator(t *testing.T) {
	gen := NewControllerResultGenerator("MyPrefix", "my-owner")

	if gen.conditionPrefix != "MyPrefix" {
		t.Errorf("conditionPrefix = %q, want %q", gen.conditionPrefix, "MyPrefix")
	}

	if gen.fieldOwner != "my-owner" {
		t.Errorf("fieldOwner = %q, want %q", gen.fieldOwner, "my-owner")
	}
}

func TestSuccess(t *testing.T) {
	g := NewWithT(t)
	gen := NewControllerResultGenerator(testPrefix, testFieldOwner)
	result := gen.Success()

	g.Expect(result.progressing).To(testmatchers.BeCondition("TestProgressing").
		WithStatus(configv1.ConditionFalse).
		WithReason(ReasonAsExpected).
		WithMessage("Success"))

	g.Expect(result.degraded).To(testmatchers.BeCondition("TestDegraded").
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
	gen := NewControllerResultGenerator(testPrefix, testFieldOwner)
	result := gen.Progressing("installing components")

	g.Expect(result.progressing).To(testmatchers.BeCondition("TestProgressing").
		WithStatus(configv1.ConditionTrue).
		WithReason(ReasonProgressing).
		WithMessage("installing components"))

	g.Expect(result.degraded).To(testmatchers.BeCondition("TestDegraded").
		WithStatus(configv1.ConditionFalse).
		WithReason(ReasonProgressing).
		WithMessage("installing components"))

	g.Expect(result.err).To(BeNil())
}

func TestWaitingOnExternal(t *testing.T) {
	g := NewWithT(t)
	gen := NewControllerResultGenerator(testPrefix, testFieldOwner)
	result := gen.WaitingOnExternal("infrastructure")

	g.Expect(result.progressing).To(testmatchers.BeCondition("TestProgressing").
		WithStatus(configv1.ConditionTrue).
		WithReason(ReasonWaitingOnExternal).
		WithMessage("Waiting on infrastructure"))

	g.Expect(result.degraded).To(testmatchers.BeCondition("TestDegraded").
		WithStatus(configv1.ConditionFalse).
		WithReason(ReasonWaitingOnExternal).
		WithMessage("Waiting on infrastructure"))

	g.Expect(result.err).To(BeNil())
}

func TestError(t *testing.T) {
	t.Run("non-terminal error", func(t *testing.T) {
		g := NewWithT(t)
		gen := NewControllerResultGenerator(testPrefix, testFieldOwner)
		testErr := fmt.Errorf("connection refused")
		result := gen.Error(testErr)

		g.Expect(result.progressing).To(testmatchers.BeCondition("TestProgressing").
			WithStatus(configv1.ConditionTrue).
			WithReason(ReasonEphemeralError).
			WithMessage("connection refused"))

		g.Expect(result.degraded).To(testmatchers.BeCondition("TestDegraded").
			WithStatus(configv1.ConditionFalse).
			WithReason(ReasonProgressing).
			WithMessage("Controller encountered a retryable error"))

		g.Expect(result.err).To(Equal(testErr))
		g.Expect(errors.Is(result.err, reconcile.TerminalError(nil))).To(BeFalse())
	})

	t.Run("terminal error", func(t *testing.T) {
		g := NewWithT(t)
		gen := NewControllerResultGenerator(testPrefix, testFieldOwner)
		innerErr := fmt.Errorf("fatal")
		termErr := reconcile.TerminalError(innerErr)
		result := gen.Error(termErr)

		g.Expect(result.progressing).To(testmatchers.BeCondition("TestProgressing").
			WithStatus(configv1.ConditionFalse).
			WithReason(ReasonNonRetryableError).
			WithMessage(termErr.Error()))

		g.Expect(result.degraded).To(testmatchers.BeCondition("TestDegraded").
			WithStatus(configv1.ConditionTrue).
			WithReason(ReasonNonRetryableError).
			WithMessage(termErr.Error()))

		g.Expect(errors.Is(result.err, reconcile.TerminalError(nil))).To(BeTrue())
	})
}

func TestNonRetryableError(t *testing.T) {
	t.Run("plain error", func(t *testing.T) {
		g := NewWithT(t)
		gen := NewControllerResultGenerator(testPrefix, testFieldOwner)
		testErr := fmt.Errorf("bad config")
		result := gen.NonRetryableError(testErr)

		g.Expect(result.progressing).To(testmatchers.BeCondition("TestProgressing").
			WithStatus(configv1.ConditionFalse).
			WithReason(ReasonNonRetryableError).
			WithMessage("terminal error: bad config"))

		g.Expect(result.degraded).To(testmatchers.BeCondition("TestDegraded").
			WithStatus(configv1.ConditionTrue).
			WithReason(ReasonNonRetryableError).
			WithMessage("terminal error: bad config"))

		g.Expect(errors.Is(result.err, reconcile.TerminalError(nil))).To(BeTrue())
	})

	t.Run("already-terminal error", func(t *testing.T) {
		g := NewWithT(t)
		gen := NewControllerResultGenerator(testPrefix, testFieldOwner)
		innerErr := fmt.Errorf("already wrapped")
		termErr := reconcile.TerminalError(innerErr)
		result := gen.NonRetryableError(termErr)

		g.Expect(result.progressing).To(testmatchers.BeCondition("TestProgressing").
			WithMessage("terminal error: already wrapped"))

		g.Expect(result.degraded).To(testmatchers.BeCondition("TestDegraded").
			WithMessage("terminal error: already wrapped"))

		g.Expect(errors.Is(result.err, reconcile.TerminalError(nil))).To(BeTrue())
		// The error should not be re-wrapped: result.err should be the same terminal error
		g.Expect(result.err).To(Equal(termErr))
	})
}

func TestWithRequeueAfter(t *testing.T) {
	t.Run("on Success", func(t *testing.T) {
		g := NewWithT(t)
		gen := NewControllerResultGenerator(testPrefix, testFieldOwner)
		result := gen.Success(WithRequeueAfter(5 * time.Second))

		g.Expect(result.requeueAfter).To(Equal(5 * time.Second))

		ctrlResult, err := result.Result()
		g.Expect(err).To(BeNil())
		g.Expect(ctrlResult.RequeueAfter).To(Equal(5 * time.Second))
	})

	t.Run("on Progressing", func(t *testing.T) {
		g := NewWithT(t)
		gen := NewControllerResultGenerator(testPrefix, testFieldOwner)
		result := gen.Progressing("working", WithRequeueAfter(10*time.Second))

		g.Expect(result.requeueAfter).To(Equal(10 * time.Second))
	})

	t.Run("on Error", func(t *testing.T) {
		g := NewWithT(t)
		gen := NewControllerResultGenerator(testPrefix, testFieldOwner)
		result := gen.Error(fmt.Errorf("transient"), WithRequeueAfter(15*time.Second))

		ctrlResult, _ := result.Result()
		g.Expect(ctrlResult.RequeueAfter).To(Equal(15 * time.Second))
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

	t.Run("existing matches same Status Reason and Message", func(t *testing.T) {
		g := NewWithT(t)

		existingTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
		existing := []configv1.ClusterOperatorStatusCondition{
			{
				Type:               "Progressing",
				Status:             configv1.ConditionFalse,
				Reason:             "AsExpected",
				Message:            "Success",
				LastTransitionTime: existingTime,
			},
		}

		cond := applyCondition("Progressing", configv1.ConditionFalse, "AsExpected", "Success")
		needsUpdate, _ := mergeConditions(
			[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{cond},
			existing,
		)

		g.Expect(needsUpdate).To(BeFalse())
		g.Expect(*cond.LastTransitionTime).To(Equal(existingTime))
	})

	t.Run("same Status and Reason but different Message", func(t *testing.T) {
		g := NewWithT(t)

		existingTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
		existing := []configv1.ClusterOperatorStatusCondition{
			{
				Type:               "Progressing",
				Status:             configv1.ConditionTrue,
				Reason:             "Working",
				Message:            "old message",
				LastTransitionTime: existingTime,
			},
		}

		cond := applyCondition("Progressing", configv1.ConditionTrue, "Working", "new message")
		needsUpdate, _ := mergeConditions(
			[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{cond},
			existing,
		)

		g.Expect(needsUpdate).To(BeTrue())
		// LastTransitionTime is preserved when Status and Reason are unchanged
		g.Expect(*cond.LastTransitionTime).To(Equal(existingTime))
	})

	t.Run("different Status", func(t *testing.T) {
		g := NewWithT(t)

		existingTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
		existing := []configv1.ClusterOperatorStatusCondition{
			{
				Type:               "Progressing",
				Status:             configv1.ConditionTrue,
				Reason:             "Working",
				Message:            "busy",
				LastTransitionTime: existingTime,
			},
		}

		cond := applyCondition("Progressing", configv1.ConditionFalse, "Working", "done")
		needsUpdate, _ := mergeConditions(
			[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{cond},
			existing,
		)

		g.Expect(needsUpdate).To(BeTrue())
		g.Expect(*cond.LastTransitionTime).ToNot(Equal(existingTime))
	})

	t.Run("different Reason", func(t *testing.T) {
		g := NewWithT(t)

		existingTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
		existing := []configv1.ClusterOperatorStatusCondition{
			{
				Type:               "Degraded",
				Status:             configv1.ConditionTrue,
				Reason:             "OldReason",
				Message:            "error",
				LastTransitionTime: existingTime,
			},
		}

		cond := applyCondition("Degraded", configv1.ConditionTrue, "NewReason", "error")
		needsUpdate, _ := mergeConditions(
			[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{cond},
			existing,
		)

		g.Expect(needsUpdate).To(BeTrue())
		g.Expect(*cond.LastTransitionTime).ToNot(Equal(existingTime))
	})

	t.Run("multiple conditions mixed", func(t *testing.T) {
		g := NewWithT(t)

		existingTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
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

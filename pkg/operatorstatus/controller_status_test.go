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
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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
		result := testResultGenerator.Success().WithRequeueAfter(5 * time.Second)

		g.Expect(result.requeueAfter).To(Equal(5 * time.Second))

		ctrlResult, err := result.Result()
		g.Expect(err).To(BeNil())
		g.Expect(ctrlResult.RequeueAfter).To(Equal(5 * time.Second))
	})

	t.Run("on Progressing", func(t *testing.T) {
		g := NewWithT(t)
		result := testResultGenerator.Progressing("working").WithRequeueAfter(10 * time.Second)

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
		result := testResultGenerator.Error(testErr).WithRequeueAfter(15 * time.Second)

		assertRequeueAfterWithError(g, result, testErr)
	})

	t.Run("on NonRetryableError", func(t *testing.T) {
		g := NewWithT(t)
		testErr := fmt.Errorf("non-retryable")
		result := testResultGenerator.NonRetryableError(testErr).WithRequeueAfter(15 * time.Second)

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
		wantTimeSame bool
		wantUpdated  bool
	}{
		{
			name:         "existing matches same Status Reason and Message",
			existing:     condFields{"Progressing", configv1.ConditionFalse, "AsExpected", "Success"},
			new:          condFields{"Progressing", configv1.ConditionFalse, "AsExpected", "Success"},
			wantTimeSame: true,
			wantUpdated:  false,
		},
		{
			name:         "different Message",
			existing:     condFields{"Progressing", configv1.ConditionTrue, "Working", "old message"},
			new:          condFields{"Progressing", configv1.ConditionTrue, "Working", "new message"},
			wantTimeSame: false,
			wantUpdated:  true,
		},
		{
			name:         "different Status",
			existing:     condFields{"Progressing", configv1.ConditionTrue, "Working", "busy"},
			new:          condFields{"Progressing", configv1.ConditionFalse, "Working", "done"},
			wantTimeSame: false,
			wantUpdated:  true,
		},
		{
			name:         "different Reason",
			existing:     condFields{"Degraded", configv1.ConditionTrue, "OldReason", "error"},
			new:          condFields{"Degraded", configv1.ConditionTrue, "NewReason", "error"},
			wantTimeSame: false,
			wantUpdated:  true,
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
			updated := mergeConditions(
				[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{cond},
				existing,
			)

			g.Expect(updated).To(Equal(tc.wantUpdated))

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

		updated := mergeConditions(
			[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{cond},
			nil,
		)

		g.Expect(updated).To(BeTrue())
		g.Expect(cond.LastTransitionTime).ToNot(BeNil())
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

		updated := mergeConditions(
			[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{unchangedCond, newCond},
			existing,
		)

		g.Expect(updated).To(BeTrue())
		// Unchanged condition preserves its LastTransitionTime
		g.Expect(*unchangedCond.LastTransitionTime).To(Equal(existingTime))
		// New condition gets a LastTransitionTime set
		g.Expect(newCond.LastTransitionTime).ToNot(BeNil())
	})

	t.Run("all conditions unchanged", func(t *testing.T) {
		g := NewWithT(t)

		existing := []configv1.ClusterOperatorStatusCondition{
			{
				Type:               "Progressing",
				Status:             configv1.ConditionFalse,
				Reason:             "AsExpected",
				Message:            "Success",
				LastTransitionTime: existingTime,
			},
			{
				Type:               "Degraded",
				Status:             configv1.ConditionFalse,
				Reason:             "AsExpected",
				Message:            "Success",
				LastTransitionTime: existingTime,
			},
		}

		cond1 := applyCondition("Progressing", configv1.ConditionFalse, "AsExpected", "Success")
		cond2 := applyCondition("Degraded", configv1.ConditionFalse, "AsExpected", "Success")

		updated := mergeConditions(
			[]*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{cond1, cond2},
			existing,
		)

		g.Expect(updated).To(BeFalse())
		g.Expect(*cond1.LastTransitionTime).To(Equal(existingTime))
		g.Expect(*cond2.LastTransitionTime).To(Equal(existingTime))
	})
}

func newFakeClient(objs ...client.Object) client.WithWatch {
	scheme := runtime.NewScheme()
	if err := configv1.AddToScheme(scheme); err != nil {
		panic(err)
	}

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&configv1.ClusterOperator{}).
		Build()
}

func TestWriteClusterOperatorStatus(t *testing.T) {
	log := logr.Discard()

	for _, tc := range []struct {
		name            string
		conditions      []configv1.ClusterOperatorStatusCondition
		wantPatchCalled bool
	}{
		{
			name: "skips patch when conditions unchanged",
			conditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:               "TestProgressing",
					Status:             configv1.ConditionFalse,
					Reason:             ReasonAsExpected,
					Message:            "Success",
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               "TestDegraded",
					Status:             configv1.ConditionFalse,
					Reason:             ReasonAsExpected,
					Message:            "Success",
					LastTransitionTime: metav1.Now(),
				},
			},
			wantPatchCalled: false,
		},
		{
			name: "patches when conditions changed",
			conditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:               "TestProgressing",
					Status:             configv1.ConditionTrue,
					Reason:             ReasonProgressing,
					Message:            "installing components",
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               "TestDegraded",
					Status:             configv1.ConditionFalse,
					Reason:             ReasonProgressing,
					Message:            "installing components",
					LastTransitionTime: metav1.Now(),
				},
			},
			wantPatchCalled: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			co := &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{
					Name: ClusterOperatorName,
					UID:  types.UID("test-uid"),
				},
				Status: configv1.ClusterOperatorStatus{
					Conditions: tc.conditions,
				},
			}

			patchCalled := false
			cl := interceptor.NewClient(newFakeClient(co), interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					patchCalled = true
					return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
				},
			})

			result := testResultGenerator.Success()
			err := result.WriteClusterOperatorStatus(t.Context(), log, cl)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(patchCalled).To(Equal(tc.wantPatchCalled))
		})
	}

	t.Run("returns error when ClusterOperator not found", func(t *testing.T) {
		g := NewWithT(t)

		// No ClusterOperator seeded
		cl := newFakeClient()

		result := testResultGenerator.Success()
		err := result.WriteClusterOperatorStatus(t.Context(), log, cl)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to get ClusterOperator"))
	})
}

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
	"os"
	"testing"
	"time"

	"github.com/go-logr/logr/testr"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/test"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

var (
	testEnv *envtest.Environment
	cl      client.WithWatch
)

const testResultGenerator ControllerResultGenerator = "Test"
const defaultReleaseVersion = "1.0.0"

func TestMain(m *testing.M) {
	code, err := runTests(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	os.Exit(code)
}

func runTests(m *testing.M) (_ int, err error) {
	testEnv = &envtest.Environment{}

	_, cl, err = test.StartEnvTest(testEnv)
	if err != nil {
		return 1, fmt.Errorf("failed to start envtest: %w", err)
	}

	defer func() { err = errors.Join(err, testEnv.Stop()) }()

	return m.Run(), nil
}

// createClusterOperator creates a ClusterOperator in the envtest API server
// and optionally sets initial status conditions via a status update. It
// registers a cleanup function to delete the object when the test completes.
func createClusterOperator(t *testing.T, conditions []configv1.ClusterOperatorStatusCondition) *configv1.ClusterOperator {
	t.Helper()
	g := NewWithT(t)

	co := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name: ClusterOperatorName,
		},
	}

	g.Expect(cl.Create(t.Context(), co)).To(Succeed())
	t.Cleanup(func() {
		g.Expect(client.IgnoreNotFound(cl.Delete(context.Background(), co))).To(Succeed())
	})

	if len(conditions) > 0 {
		co.Status.Conditions = conditions
		g.Expect(cl.Status().Update(t.Context(), co)).To(Succeed())
	}

	return co
}

// seedOperatorVersion performs an SSA status patch to set status.versions on
// the ClusterOperator under the given field owner. This establishes field
// ownership naturally through the API server's managed fields tracker.
func seedOperatorVersion(ctx context.Context, k8sClient client.Client, fieldOwner client.FieldOwner) error {
	co := &configv1.ClusterOperator{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: ClusterOperatorName}, co); err != nil {
		return err
	}

	applyConfig := configv1apply.ClusterOperator(ClusterOperatorName).
		WithUID(co.UID).
		WithStatus(configv1apply.ClusterOperatorStatus().
			WithVersions(
				configv1apply.OperandVersion().
					WithName(OperatorVersionKey).
					WithVersion(defaultReleaseVersion),
			),
		)

	patch := util.ApplyConfigPatch(applyConfig)

	return k8sClient.Status().Patch(ctx, co, patch, fieldOwner, client.ForceOwnership)
}

func TestSuccess(t *testing.T) {
	g := NewWithT(t)
	result := testResultGenerator.Success()

	g.Expect(result.progressing).To(Equal(partialCondition{
		status:  configv1.ConditionFalse,
		reason:  ReasonAsExpected,
		message: "Success",
	}))

	g.Expect(result.available).To(HaveValue(Equal(partialCondition{
		status:  configv1.ConditionTrue,
		reason:  ReasonAsExpected,
		message: "Success",
	})))

	g.Expect(result.err).To(BeNil())

	ctrlResult, err := result.Result()
	g.Expect(err).To(BeNil())
	g.Expect(ctrlResult).To(Equal(ctrl.Result{}))
}

func TestProgressing(t *testing.T) {
	g := NewWithT(t)
	result := testResultGenerator.Progressing("installing components")

	g.Expect(result.progressing).To(Equal(partialCondition{
		status:  configv1.ConditionTrue,
		reason:  ReasonProgressing,
		message: "installing components",
	}))

	g.Expect(result.available).To(BeNil())

	g.Expect(result.err).To(BeNil())
}

func TestWaitingOnExternal(t *testing.T) {
	g := NewWithT(t)
	result := testResultGenerator.WaitingOnExternal("infrastructure")

	g.Expect(result.progressing).To(Equal(partialCondition{
		status:  configv1.ConditionTrue,
		reason:  ReasonWaitingOnExternal,
		message: "Waiting on infrastructure",
	}))

	g.Expect(result.available).To(BeNil())

	g.Expect(result.err).To(BeNil())
}

func TestError(t *testing.T) {
	t.Run("non-terminal error", func(t *testing.T) {
		g := NewWithT(t)
		testErr := fmt.Errorf("connection refused")
		result := testResultGenerator.Error(testErr)

		g.Expect(result.progressing).To(Equal(partialCondition{
			status:  configv1.ConditionTrue,
			reason:  ReasonEphemeralError,
			message: "connection refused",
		}))

		g.Expect(result.available).To(BeNil())

		g.Expect(result.err).To(Equal(testErr))
		g.Expect(errors.Is(result.err, reconcile.TerminalError(nil))).To(BeFalse())
	})

	t.Run("terminal error", func(t *testing.T) {
		g := NewWithT(t)
		innerErr := fmt.Errorf("fatal")
		termErr := reconcile.TerminalError(innerErr)
		result := testResultGenerator.Error(termErr)

		g.Expect(result.progressing).To(Equal(partialCondition{
			status:  configv1.ConditionFalse,
			reason:  ReasonNonRetryableError,
			message: termErr.Error(),
		}))

		g.Expect(result.available).To(HaveValue(Equal(partialCondition{
			status:  configv1.ConditionFalse,
			reason:  ReasonNonRetryableError,
			message: termErr.Error(),
		})))

		g.Expect(errors.Is(result.err, reconcile.TerminalError(nil))).To(BeTrue())
	})
}

func TestNonRetryableError(t *testing.T) {
	t.Run("plain error", func(t *testing.T) {
		g := NewWithT(t)
		testErr := fmt.Errorf("bad config")
		result := testResultGenerator.NonRetryableError(testErr)

		g.Expect(result.progressing).To(Equal(partialCondition{
			status:  configv1.ConditionFalse,
			reason:  ReasonNonRetryableError,
			message: "terminal error: bad config",
		}))

		g.Expect(result.available).To(HaveValue(Equal(partialCondition{
			status:  configv1.ConditionFalse,
			reason:  ReasonNonRetryableError,
			message: "terminal error: bad config",
		})))

		g.Expect(errors.Is(result.err, reconcile.TerminalError(nil))).To(BeTrue())
	})

	t.Run("already-terminal error", func(t *testing.T) {
		g := NewWithT(t)
		innerErr := fmt.Errorf("already wrapped")
		termErr := reconcile.TerminalError(innerErr)
		result := testResultGenerator.NonRetryableError(termErr)

		g.Expect(result.progressing).To(Equal(partialCondition{
			status:  configv1.ConditionFalse,
			reason:  ReasonNonRetryableError,
			message: "terminal error: already wrapped",
		}))

		g.Expect(result.available).To(HaveValue(Equal(partialCondition{
			status:  configv1.ConditionFalse,
			reason:  ReasonNonRetryableError,
			message: "terminal error: already wrapped",
		})))

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

func TestWriteClusterOperatorStatus(t *testing.T) {
	log := testr.New(t)

	type expectedCondition struct {
		status  configv1.ConditionStatus
		reason  string
		message string
	}

	existingAvailable := []configv1.ClusterOperatorStatusCondition{
		{
			Type:               "TestAvailable",
			Status:             configv1.ConditionTrue,
			Reason:             ReasonAsExpected,
			Message:            "Success",
			LastTransitionTime: metav1.Now(),
		},
	}

	for _, tc := range []struct {
		name               string
		existingConditions []configv1.ClusterOperatorStatusCondition
		result             ReconcileResult
		wantProgressing    expectedCondition
		wantAvailable      *expectedCondition
	}{
		{
			name:            "Success writes Progressing and Available conditions",
			result:          testResultGenerator.Success(),
			wantProgressing: expectedCondition{configv1.ConditionFalse, ReasonAsExpected, "Success"},
			wantAvailable:   &expectedCondition{configv1.ConditionTrue, ReasonAsExpected, "Success"},
		},
		{
			name:            "Progressing without existing Available does not write Available",
			result:          testResultGenerator.Progressing("installing components"),
			wantProgressing: expectedCondition{configv1.ConditionTrue, ReasonProgressing, "installing components"},
			wantAvailable:   nil,
		},
		{
			name:               "Progressing with existing Available preserves Available",
			existingConditions: existingAvailable,
			result:             testResultGenerator.Progressing("installing components"),
			wantProgressing:    expectedCondition{configv1.ConditionTrue, ReasonProgressing, "installing components"},
			wantAvailable:      &expectedCondition{configv1.ConditionTrue, ReasonAsExpected, "Success"},
		},
		{
			name:               "Error with existing Available preserves Available",
			existingConditions: existingAvailable,
			result:             testResultGenerator.Error(fmt.Errorf("connection refused")),
			wantProgressing:    expectedCondition{configv1.ConditionTrue, ReasonEphemeralError, "connection refused"},
			wantAvailable:      &expectedCondition{configv1.ConditionTrue, ReasonAsExpected, "Success"},
		},
		{
			name:               "WaitingOnExternal with existing Available preserves Available",
			existingConditions: existingAvailable,
			result:             testResultGenerator.WaitingOnExternal("infrastructure"),
			wantProgressing:    expectedCondition{configv1.ConditionTrue, ReasonWaitingOnExternal, "Waiting on infrastructure"},
			wantAvailable:      &expectedCondition{configv1.ConditionTrue, ReasonAsExpected, "Success"},
		},
		{
			name:               "NonRetryableError explicitly sets Available=False",
			existingConditions: existingAvailable,
			result:             testResultGenerator.NonRetryableError(fmt.Errorf("bad config")),
			wantProgressing:    expectedCondition{configv1.ConditionFalse, ReasonNonRetryableError, "terminal error: bad config"},
			wantAvailable:      &expectedCondition{configv1.ConditionFalse, ReasonNonRetryableError, "terminal error: bad config"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			co := createClusterOperator(t, tc.existingConditions)

			result := tc.result
			err := result.WriteClusterOperatorStatus(t.Context(), log, cl)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(cl.Get(t.Context(), client.ObjectKeyFromObject(co), co)).To(Succeed())

			progressing := findClusterOperatorCondition("TestProgressing", co.Status.Conditions)
			g.Expect(progressing).ToNot(BeNil())
			g.Expect(progressing.Status).To(Equal(tc.wantProgressing.status))
			g.Expect(progressing.Reason).To(Equal(tc.wantProgressing.reason))
			g.Expect(progressing.Message).To(Equal(tc.wantProgressing.message))

			available := findClusterOperatorCondition("TestAvailable", co.Status.Conditions)
			if tc.wantAvailable == nil {
				g.Expect(available).To(BeNil())
			} else {
				g.Expect(available).ToNot(BeNil())
				g.Expect(available.Status).To(Equal(tc.wantAvailable.status))
				g.Expect(available.Reason).To(Equal(tc.wantAvailable.reason))
				g.Expect(available.Message).To(Equal(tc.wantAvailable.message))
			}
		})
	}

	t.Run("skips patch when conditions unchanged", func(t *testing.T) {
		g := NewWithT(t)

		createClusterOperator(t, []configv1.ClusterOperatorStatusCondition{
			{
				Type:               "TestProgressing",
				Status:             configv1.ConditionFalse,
				Reason:             ReasonAsExpected,
				Message:            "Success",
				LastTransitionTime: metav1.Now(),
			},
			{
				Type:               "TestAvailable",
				Status:             configv1.ConditionTrue,
				Reason:             ReasonAsExpected,
				Message:            "Success",
				LastTransitionTime: metav1.Now(),
			},
		})

		patchCalled := false
		interceptCl := interceptor.NewClient(cl, interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				patchCalled = true
				return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		})

		result := testResultGenerator.Success()
		err := result.WriteClusterOperatorStatus(t.Context(), log, interceptCl)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(patchCalled).To(BeFalse())
	})

	t.Run("patches when conditions changed", func(t *testing.T) {
		g := NewWithT(t)

		createClusterOperator(t, []configv1.ClusterOperatorStatusCondition{
			{
				Type:               "TestProgressing",
				Status:             configv1.ConditionTrue,
				Reason:             ReasonProgressing,
				Message:            "installing components",
				LastTransitionTime: metav1.Now(),
			},
			{
				Type:               "TestAvailable",
				Status:             configv1.ConditionTrue,
				Reason:             ReasonAsExpected,
				Message:            "Success",
				LastTransitionTime: metav1.Now(),
			},
		})

		patchCalled := false
		interceptCl := interceptor.NewClient(cl, interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				patchCalled = true
				return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		})

		result := testResultGenerator.Success()
		err := result.WriteClusterOperatorStatus(t.Context(), log, interceptCl)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(patchCalled).To(BeTrue())
	})

	t.Run("returns error when ClusterOperator not found", func(t *testing.T) {
		g := NewWithT(t)

		// No ClusterOperator created.
		result := testResultGenerator.Success()
		err := result.WriteClusterOperatorStatus(t.Context(), log, cl)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to get ClusterOperator"))
	})

	t.Run("does not update versions with a different field owner when WithUpdateOperatorVersion is not set", func(t *testing.T) {
		g := NewWithT(t)

		co := createClusterOperator(t, nil)

		// Seed a version under a different field owner.
		g.Expect(seedOperatorVersion(t.Context(), cl, client.FieldOwner("other-owner"))).To(Succeed())

		// Write status without WithUpdateOperatorVersion.
		result := testResultGenerator.Success()
		g.Expect(result.WriteClusterOperatorStatus(t.Context(), log, cl)).To(Succeed())

		g.Expect(cl.Get(t.Context(), client.ObjectKeyFromObject(co), co)).To(Succeed())
		g.Expect(co.Status.Versions).To(ContainElement(SatisfyAll(
			HaveField("Name", Equal(OperatorVersionKey)),
			HaveField("Version", Equal(defaultReleaseVersion)),
		)))
	})

	t.Run("does not update versions with same field owner when WithUpdateOperatorVersion is not set", func(t *testing.T) {
		g := NewWithT(t)

		co := createClusterOperator(t, nil)

		// Seed a version under the same field owner as the code under test.
		g.Expect(seedOperatorVersion(t.Context(), cl, CAPIFieldOwner(testResultGenerator))).To(Succeed())

		// Write status without WithUpdateOperatorVersion.
		result := testResultGenerator.Success()
		g.Expect(result.WriteClusterOperatorStatus(t.Context(), log, cl)).To(Succeed())

		g.Expect(cl.Get(t.Context(), client.ObjectKeyFromObject(co), co)).To(Succeed())
		g.Expect(co.Status.Versions).To(ContainElement(SatisfyAll(
			HaveField("Name", Equal(OperatorVersionKey)),
			HaveField("Version", Equal(defaultReleaseVersion)),
		)))
	})

	t.Run("overwrites versions with a different field owner when WithUpdateOperatorVersion is set", func(t *testing.T) {
		g := NewWithT(t)

		co := createClusterOperator(t, nil)

		// Seed a version under a different field owner.
		g.Expect(seedOperatorVersion(t.Context(), cl, client.FieldOwner("other-owner"))).To(Succeed())

		// Write status with WithUpdateOperatorVersion to a new version.
		result := testResultGenerator.Success().WithUpdateOperatorVersion("2.0.0")
		g.Expect(result.WriteClusterOperatorStatus(t.Context(), log, cl)).To(Succeed())

		g.Expect(cl.Get(t.Context(), client.ObjectKeyFromObject(co), co)).To(Succeed())
		g.Expect(co.Status.Versions).To(ContainElement(SatisfyAll(
			HaveField("Name", Equal(OperatorVersionKey)),
			HaveField("Version", Equal("2.0.0")),
		)))
	})

	t.Run("overwrites versions with same field owner when WithUpdateOperatorVersion is set", func(t *testing.T) {
		g := NewWithT(t)

		co := createClusterOperator(t, nil)

		// Seed a version under the same field owner as the code under test.
		g.Expect(seedOperatorVersion(t.Context(), cl, CAPIFieldOwner(testResultGenerator))).To(Succeed())

		// Write status with WithUpdateOperatorVersion to a new version.
		result := testResultGenerator.Success().WithUpdateOperatorVersion("2.0.0")
		g.Expect(result.WriteClusterOperatorStatus(t.Context(), log, cl)).To(Succeed())

		g.Expect(cl.Get(t.Context(), client.ObjectKeyFromObject(co), co)).To(Succeed())
		g.Expect(co.Status.Versions).To(ConsistOf(SatisfyAll(
			HaveField("Name", Equal(OperatorVersionKey)),
			HaveField("Version", Equal("2.0.0")),
		)))
	})
}

func TestFindClusterOperatorCondition(t *testing.T) {
	t.Run("returns matching condition", func(t *testing.T) {
		g := NewWithT(t)

		conditions := []configv1.ClusterOperatorStatusCondition{
			{Type: "TestProgressing", Status: configv1.ConditionFalse},
			{Type: "TestAvailable", Status: configv1.ConditionTrue},
		}

		result := findClusterOperatorCondition("TestAvailable", conditions)
		g.Expect(result).ToNot(BeNil())
		g.Expect(result.Type).To(Equal(configv1.ClusterStatusConditionType("TestAvailable")))
		g.Expect(result.Status).To(Equal(configv1.ConditionTrue))
	})

	t.Run("returns nil when not found", func(t *testing.T) {
		g := NewWithT(t)

		conditions := []configv1.ClusterOperatorStatusCondition{
			{Type: "TestProgressing", Status: configv1.ConditionFalse},
		}

		result := findClusterOperatorCondition("TestAvailable", conditions)
		g.Expect(result).To(BeNil())
	})

	t.Run("returns nil for empty slice", func(t *testing.T) {
		g := NewWithT(t)

		g.Expect(findClusterOperatorCondition("TestAvailable", nil)).To(BeNil())
		g.Expect(findClusterOperatorCondition("TestAvailable", []configv1.ClusterOperatorStatusCondition{})).To(BeNil())
	})
}

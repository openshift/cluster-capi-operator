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
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	clusterOperatorName = "cluster-api"
	ssaFieldOwnerDomain = "capi-operator.openshift.io"

	// ReasonAsExpected is the reason for the condition when the operator is in a normal state.
	ReasonAsExpected = "AsExpected"

	// ReasonProgressing indicates that the controller is progressing normally.
	// An observer should continue to wait.
	ReasonProgressing = "Progressing"

	// ReasonWaitingOnExternal indicates that the controller is waiting on an external event.
	// An observer should continue to wait.
	ReasonWaitingOnExternal = "WaitingOnExternal"

	// ReasonEphemeralError indicates that the controller encountered an ephemeral error.
	// An observer should continue to wait.
	// If this condition persists, the ClusterOperator will eventually enter a degraded state.
	ReasonEphemeralError = "EphemeralError"

	// ReasonNonRetryableError indicates that the controller encountered a non-retryable error.
	ReasonNonRetryableError = "NonRetryableError"
)

// CAPIFieldOwner returns a qualifiedclient.FieldOwner for the given qualifier.
// The qualifier should identify the writer in the context of the CAPI operator,
// for example a controller name.
func CAPIFieldOwner[S ~string](qualifier S) client.FieldOwner {
	return client.FieldOwner(ssaFieldOwnerDomain + "/" + qualifier)
}

// ReconcileResult represents the result of a controller's reconciliation.
// As well as returning a reconcile.Result to controller-runtime, it can also
// write the result as conditions on the ClusterOperator.
type ReconcileResult struct {
	ControllerResultGenerator

	progressing  *configv1apply.ClusterOperatorStatusConditionApplyConfiguration
	degraded     *configv1apply.ClusterOperatorStatusConditionApplyConfiguration
	err          error
	requeueAfter time.Duration
}

// Result returns a reconcile.Result for controller-runtime.
func (r *ReconcileResult) Result() (ctrl.Result, error) {
	// controller-runtime requires Result{} to be empty when returning an error.
	if r.err != nil {
		return ctrl.Result{}, r.err
	}

	return ctrl.Result{RequeueAfter: r.requeueAfter}, nil
}

// Error returns any error that occurred during reconciliation, if any.
func (r *ReconcileResult) Error() error {
	return r.err
}

// WithRequeueAfter sets requeueAfter on the returned reconcile.Result.
func (r ReconcileResult) WithRequeueAfter(requeueAfter time.Duration) ReconcileResult {
	r.requeueAfter = requeueAfter
	return r
}

// ControllerResultGenerator generates ReconcileResults for a specific controller.
type ControllerResultGenerator string

// Success returns a ReconcileResult indicating that the controller has succeeded.
// Returning this result will not requeue the controller.
func (c ControllerResultGenerator) Success() ReconcileResult {
	return ReconcileResult{
		ControllerResultGenerator: c,
		progressing:               c.progressingCondition(configv1.ConditionFalse, ReasonAsExpected, "Success"),
		degraded:                  c.degradedCondition(configv1.ConditionFalse, ReasonAsExpected, "Success"),
		err:                       nil,
	}
}

// SuccessP is a convenience wrapper around Success that returns a pointer to the ReconcileResult.
func (c ControllerResultGenerator) SuccessP() *ReconcileResult {
	return ptr.To(c.Success())
}

// Progressing returns a ReconcileResult indicating that the controller is
// progressing. This should only be used when expected to be reconciled again
// immediately, for example after writing status to a watched resource.
// Returning this result will not requeue the controller directly.
func (c ControllerResultGenerator) Progressing(message string) ReconcileResult {
	return ReconcileResult{
		ControllerResultGenerator: c,
		progressing:               c.progressingCondition(configv1.ConditionTrue, ReasonProgressing, message),
		degraded:                  c.degradedCondition(configv1.ConditionFalse, ReasonProgressing, message),
		err:                       nil,
	}
}

// ProgressingP is a convenience wrapper around Progressing that returns a pointer to the ReconcileResult.
func (c ControllerResultGenerator) ProgressingP(message string) *ReconcileResult {
	return ptr.To(c.Progressing(message))
}

// WaitingOnExternal returns a ReconcileResult indicating that the controller is
// waiting on an external event. The wait description will be included in the condition message.
// Returning this result will not requeue the controller directly, so it should
// only be used when expecting a watched event to occur.
func (c ControllerResultGenerator) WaitingOnExternal(waitDescription string) ReconcileResult {
	message := fmt.Sprintf("Waiting on %s", waitDescription)

	return ReconcileResult{
		ControllerResultGenerator: c,
		progressing:               c.progressingCondition(configv1.ConditionTrue, ReasonWaitingOnExternal, message),
		degraded:                  c.degradedCondition(configv1.ConditionFalse, ReasonWaitingOnExternal, message),
		err:                       nil,
	}
}

// WaitingOnExternalP is a convenience wrapper around WaitingOnExternal that returns a pointer to the ReconcileResult.
func (c ControllerResultGenerator) WaitingOnExternalP(waitDescription string) *ReconcileResult {
	return ptr.To(c.WaitingOnExternal(waitDescription))
}

// Error returns a ReconcileResult with an error. If the error is a controller-runtime terminal error, calling this method has the same effect as calling NonRetryableError.
// Otherwise, returning this result will requeue the controller.
func (c ControllerResultGenerator) Error(err error) ReconcileResult {
	// If the error is a controller-runtime terminal error, return a non-retryable error
	if errors.Is(err, reconcile.TerminalError(nil)) {
		return c.nonRetryableError(err)
	}

	return ReconcileResult{
		ControllerResultGenerator: c,
		progressing:               c.progressingCondition(configv1.ConditionTrue, ReasonEphemeralError, err.Error()),
		degraded:                  c.degradedCondition(configv1.ConditionFalse, ReasonProgressing, "Controller encountered a retryable error"),
		err:                       err,
	}
}

// ErrorP is a convenience wrapper around Error that returns a pointer to the ReconcileResult.
func (c ControllerResultGenerator) ErrorP(err error) *ReconcileResult {
	return ptr.To(c.Error(err))
}

// NonRetryableError returns a ReconcileResult with a non-retryable error. The
// error will be wrapped in a controller-runtime terminal error if it is not
// already a terminal error. Returning this result will not requeue the
// controller.
func (c ControllerResultGenerator) NonRetryableError(err error) ReconcileResult {
	if !errors.Is(err, reconcile.TerminalError(nil)) {
		err = reconcile.TerminalError(err)
	}

	return c.nonRetryableError(err)
}

// NonRetryableErrorP is a convenience wrapper around NonRetryableError that returns a pointer to the ReconcileResult.
func (c ControllerResultGenerator) NonRetryableErrorP(err error) *ReconcileResult {
	return ptr.To(c.NonRetryableError(err))
}

func (c ControllerResultGenerator) nonRetryableError(termErr error) ReconcileResult {
	return ReconcileResult{
		ControllerResultGenerator: c,
		progressing:               c.progressingCondition(configv1.ConditionFalse, ReasonNonRetryableError, termErr.Error()),
		degraded:                  c.degradedCondition(configv1.ConditionTrue, ReasonNonRetryableError, termErr.Error()),
		err:                       termErr,
	}
}

func (c ControllerResultGenerator) condition(condType configv1.ClusterStatusConditionType, status configv1.ConditionStatus, reason, message string) *configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	return configv1apply.ClusterOperatorStatusCondition().
		WithType(condType).
		WithStatus(status).
		WithReason(reason).
		WithMessage(message)
}

func (c ControllerResultGenerator) progressingCondition(status configv1.ConditionStatus, reason, message string) *configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	return c.condition(configv1.ClusterStatusConditionType(c+"Progressing"), status, reason, message)
}

func (c ControllerResultGenerator) degradedCondition(status configv1.ConditionStatus, reason, message string) *configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	return c.condition(configv1.ClusterStatusConditionType(c+"Degraded"), status, reason, message)
}

// WriteClusterOperatorStatus writes the reconcile result as conditions on the ClusterOperator.
func (r *ReconcileResult) WriteClusterOperatorStatus(ctx context.Context, log logr.Logger, k8sclient client.Client) error {
	// Get the ClusterOperator
	co := &configv1.ClusterOperator{}
	if err := k8sclient.Get(ctx, client.ObjectKey{Name: clusterOperatorName}, co); err != nil {
		return fmt.Errorf("failed to get ClusterOperator: %w", err)
	}

	// Build conditions based on reconcile result
	conditions := []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{
		r.progressing,
		r.degraded,
	}

	updated := mergeConditions(conditions, co.Status.Conditions)
	if !updated {
		return nil
	}

	clusterOperatorApplyConfig := configv1apply.ClusterOperator(clusterOperatorName).
		WithUID(co.UID).
		WithStatus(configv1apply.ClusterOperatorStatus().
			WithConditions(conditions...))

	patch := util.ApplyConfigPatch(clusterOperatorApplyConfig)
	if err := k8sclient.Status().Patch(ctx, co, patch, CAPIFieldOwner(r.ControllerResultGenerator), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to patch ClusterOperator status: %w", err)
	}

	return nil
}

// mergeConditions sets LastTransitionTime on each new condition based on the
// existing conditions. If a condition's Status/Reason/Message are unchanged,
// LastTransitionTime is preserved from the existing condition.
func mergeConditions(newConditions []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration, existingConditions []configv1.ClusterOperatorStatusCondition) bool {
	now := metav1.Now()

	findClusterOperatorCondition := func(condType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
		for i := range existingConditions {
			if existingConditions[i].Type == condType {
				return &existingConditions[i]
			}
		}

		return nil
	}

	updated := false

	for _, cond := range newConditions {
		if cond.Type == nil || cond.Status == nil || cond.Reason == nil || cond.Message == nil {
			// Programming error - should never happen
			panic(fmt.Sprintf("condition is missing required fields: %+v", cond))
		}

		existing := findClusterOperatorCondition(*cond.Type)

		switch {
		case existing == nil:
			cond.WithLastTransitionTime(now)

			updated = true

		// Don't update LastTransitionTime if Status/Reason/Message are the same
		case existing.Status == *cond.Status && existing.Reason == *cond.Reason && existing.Message == *cond.Message:
			cond.WithLastTransitionTime(existing.LastTransitionTime)

		default:
			cond.WithLastTransitionTime(now)

			updated = true
		}
	}

	return updated
}

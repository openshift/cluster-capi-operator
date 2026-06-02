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
	// ClusterOperatorName is the name of the ClusterOperator resource managed
	// by the CAPI operator.
	ClusterOperatorName = "cluster-api"
	// CAPIOperatorIdentifierDomain is the domain used as the field owner prefix for
	// server-side apply operations by the CAPI operator.
	CAPIOperatorIdentifierDomain = "capi-operator.openshift.io"

	// ConditionAvailableSuffix is the suffix added to a controller prefix to
	// form the controller's available condition type.
	ConditionAvailableSuffix = "Available"

	// ConditionProgressingSuffix is the suffix added to a controller prefix to
	// form the controller's progressing condition type.
	ConditionProgressingSuffix = "Progressing"

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

const (
	// OperatorVersionKey is the key used to store the operator version in the ClusterOperator status.
	OperatorVersionKey = "operator"
)

// CAPIFieldOwner returns a qualifiedclient.FieldOwner for the given qualifier.
// The qualifier should identify the writer in the context of the CAPI operator,
// for example a controller name.
func CAPIFieldOwner[S ~string](qualifier S) client.FieldOwner {
	return client.FieldOwner(CAPIOperatorIdentifierDomain + "/" + qualifier)
}

type partialCondition struct {
	status  configv1.ConditionStatus
	reason  string
	message string
}

// ReconcileResult represents the result of a controller's reconciliation.
// As well as returning a reconcile.Result to controller-runtime, it can also
// write the result as conditions on the ClusterOperator.
type ReconcileResult struct {
	ControllerResultGenerator

	// a ReconcileResult must always have an explicit progressing condition
	progressing partialCondition

	// the available condition is optional, and will be maintained from the
	// current state if not set explicitly
	available *partialCondition

	// if operatorVersion is set, we will update the ClusterOperator operator
	// version to this value when writing status
	operatorVersion string

	err          error
	requeueAfter time.Duration
}

// Internal constructors for ReconcileResult. Callers do not set these properties directly.

func newReconcileResult(controllerResultGenerator ControllerResultGenerator, progressingStatus configv1.ConditionStatus, reason, message string) ReconcileResult {
	return ReconcileResult{
		ControllerResultGenerator: controllerResultGenerator,
		progressing:               partialCondition{progressingStatus, reason, message},
	}
}

func (r ReconcileResult) withAvailable(status configv1.ConditionStatus, reason, message string) ReconcileResult {
	r.available = &partialCondition{status, reason, message}
	return r
}

func (r ReconcileResult) withError(err error) ReconcileResult {
	r.err = err
	return r
}

// WithUpdateOperatorVersion causes the reconcile result to also update the
// operator version when writing status to the ClusterOperator.
func (r ReconcileResult) WithUpdateOperatorVersion(operatorVersion string) ReconcileResult {
	r.operatorVersion = operatorVersion
	return r
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
	return newReconcileResult(c, configv1.ConditionFalse, ReasonAsExpected, "Success").
		withAvailable(configv1.ConditionTrue, ReasonAsExpected, "Success")
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
	return newReconcileResult(c, configv1.ConditionTrue, ReasonProgressing, message)
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

	return newReconcileResult(c, configv1.ConditionTrue, ReasonWaitingOnExternal, message)
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

	return newReconcileResult(c, configv1.ConditionTrue, ReasonEphemeralError, err.Error()).
		withError(err)
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

func (c ControllerResultGenerator) nonRetryableError(terminalErr error) ReconcileResult {
	return newReconcileResult(c, configv1.ConditionFalse, ReasonNonRetryableError, terminalErr.Error()).
		withAvailable(configv1.ConditionFalse, ReasonNonRetryableError, terminalErr.Error()).
		withError(terminalErr)
}

func (c ControllerResultGenerator) condition(condType string, status configv1.ConditionStatus, reason, message string) *configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	return configv1apply.ClusterOperatorStatusCondition().
		WithType(c.conditionType(condType)).
		WithStatus(status).
		WithReason(reason).
		WithMessage(message)
}

func (c ControllerResultGenerator) conditionType(condType string) configv1.ClusterStatusConditionType {
	return configv1.ClusterStatusConditionType(string(c) + condType)
}

// WriteClusterOperatorStatus writes the reconcile result as conditions on the ClusterOperator.
func (r *ReconcileResult) WriteClusterOperatorStatus(ctx context.Context, log logr.Logger, k8sclient client.Client) error {
	// Get the ClusterOperator
	co := &configv1.ClusterOperator{}
	if err := k8sclient.Get(ctx, client.ObjectKey{Name: ClusterOperatorName}, co); err != nil {
		return fmt.Errorf("failed to get ClusterOperator: %w", err)
	}

	// Extract currently managed fields. This ensures that we preserve operator version if we're not updating it.
	clusterOperatorApplyConfig, err := configv1apply.ExtractClusterOperatorStatus(co, string(CAPIFieldOwner(r.ControllerResultGenerator)))
	if err != nil {
		return fmt.Errorf("failed to extract ClusterOperator apply configuration: %w", err)
	}

	clusterOperatorApplyConfig = clusterOperatorApplyConfig.WithUID(co.UID)

	conditions := r.constructPartialConditions(co)
	conditionsUpdated := mergeConditions(conditions, co.Status.Conditions)

	releaseVersionNeedsUpdate := false
	if r.operatorVersion != "" {
		releaseVersionNeedsUpdate = func() bool {
			for _, version := range co.Status.Versions {
				if version.Name == OperatorVersionKey {
					return version.Version != r.operatorVersion
				}
			}

			return true
		}()
	}

	if !conditionsUpdated && !releaseVersionNeedsUpdate {
		return nil
	}

	status := clusterOperatorApplyConfig.Status
	if status == nil {
		status = configv1apply.ClusterOperatorStatus()
	}

	// Clear previously extracted conditions to avoid duplicates, as
	// WithConditions appends to the existing slice.
	status.Conditions = nil

	status = status.WithConditions(conditions...)
	if r.operatorVersion != "" {
		// Clear previously extracted versions to avoid duplicates, as
		// WithVersions appends to the existing slice.
		status.Versions = nil
		status = status.WithVersions(
			configv1apply.OperandVersion().
				WithName(OperatorVersionKey).
				WithVersion(r.operatorVersion))
	}

	patch := util.ApplyConfigPatch(clusterOperatorApplyConfig.WithStatus(status))
	if err := k8sclient.Status().Patch(ctx, co, patch, CAPIFieldOwner(r.ControllerResultGenerator), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to patch ClusterOperator status: %w", err)
	}

	return nil
}

// constructPartialConditions returns a set of condition apply configurations
// for the ReconcileResult. They do not yet have LastTransitionTime set.
func (r *ReconcileResult) constructPartialConditions(co *configv1.ClusterOperator) []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	// The following behaviours are intended to implement the semantics of
	// - configv1.ClusterOperatorStatusAvailable
	// - configv1.ClusterOperatorStatusProgressing
	// as described in the godoc of those constants.
	//
	// ReconcileResult always contains an explicit Progressing condition, so we
	// we use that directly.
	//
	// Where an explicit Available condition is set, we use that directly.
	// Otherwise, we calculate it based on existing state and the Progressing
	// condition.
	//
	// During installation, we don't want to declare Available=True until the
	// first time we have successfully reconciled. We achieve this by initially
	// not setting the Available condition at all. The condition aggregator will
	// interpret this correctly.
	//
	// After we have become Available for the first time, we don't want to
	// declare Available=False except for conditions which require an
	// administrator's intervention. Currently we set it for non-retryable
	// errors. Otherwise we copy the previous state of the Available condition
	// if it exists.
	conditions := []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{
		r.condition(ConditionProgressingSuffix, r.progressing.status, r.progressing.reason, r.progressing.message),
	}

	if r.available != nil {
		// Explicitly set Available condition
		conditions = append(conditions, r.condition(ConditionAvailableSuffix, r.available.status, r.available.reason, r.available.message))
	} else {
		// Infer Available condition from existing state, don't write if not
		// already present
		currentAvailable := findClusterOperatorCondition(r.conditionType(ConditionAvailableSuffix), co.Status.Conditions)
		if currentAvailable != nil {
			conditions = append(conditions, r.condition(ConditionAvailableSuffix, currentAvailable.Status, currentAvailable.Reason, currentAvailable.Message))
		}
	}

	return conditions
}

func findClusterOperatorCondition(condType configv1.ClusterStatusConditionType, conditions []configv1.ClusterOperatorStatusCondition) *configv1.ClusterOperatorStatusCondition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}

	return nil
}

// mergeConditions sets LastTransitionTime on each new condition based on the
// existing conditions. If a condition's Status/Reason/Message are unchanged,
// LastTransitionTime is preserved from the existing condition.
func mergeConditions(newConditions []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration, existingConditions []configv1.ClusterOperatorStatusCondition) bool {
	now := metav1.Now()

	updated := false

	for _, cond := range newConditions {
		if cond.Type == nil || cond.Status == nil || cond.Reason == nil || cond.Message == nil {
			// Programming error - should never happen
			panic(fmt.Sprintf("condition is missing required fields: %+v", cond))
		}

		existing := findClusterOperatorCondition(*cond.Type, existingConditions)

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

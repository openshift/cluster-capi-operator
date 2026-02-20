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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	clusterOperatorName = "cluster-api"

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

func NewControllerResultGenerator(conditionPrefix string, fieldOwner string) ControllerResultGenerator {
	return ControllerResultGenerator{
		conditionPrefix: conditionPrefix,
		fieldOwner:      fieldOwner,
	}
}

type ReconcileResult struct {
	*ControllerResultGenerator

	progressing  *configv1apply.ClusterOperatorStatusConditionApplyConfiguration
	degraded     *configv1apply.ClusterOperatorStatusConditionApplyConfiguration
	err          error
	requeueAfter time.Duration
}

func (r *ReconcileResult) Result() (ctrl.Result, error) {
	return ctrl.Result{RequeueAfter: r.requeueAfter}, r.err
}

func (r *ReconcileResult) Error() error {
	return r.err
}

type ReconcileResultOption func(*ReconcileResult)

func WithRequeueAfter(requeueAfter time.Duration) ReconcileResultOption {
	return func(r *ReconcileResult) {
		r.requeueAfter = requeueAfter
	}
}

type ControllerResultGenerator struct {
	conditionPrefix string
	fieldOwner      string
}

func resultWithOptions(result ReconcileResult, opts ...ReconcileResultOption) ReconcileResult {
	for _, opt := range opts {
		opt(&result)
	}

	return result
}

func (c *ControllerResultGenerator) Success(opts ...ReconcileResultOption) ReconcileResult {
	return resultWithOptions(ReconcileResult{
		ControllerResultGenerator: c,
		progressing:               c.progressingCondition(configv1.ConditionFalse, ReasonAsExpected, "Success"),
		degraded:                  c.degradedCondition(configv1.ConditionFalse, ReasonAsExpected, "Success"),
		err:                       nil,
	}, opts...)
}

func (c *ControllerResultGenerator) Progressing(message string, opts ...ReconcileResultOption) ReconcileResult {
	return resultWithOptions(ReconcileResult{
		ControllerResultGenerator: c,
		progressing:               c.progressingCondition(configv1.ConditionTrue, ReasonProgressing, message),
		degraded:                  c.degradedCondition(configv1.ConditionFalse, ReasonProgressing, message),
		err:                       nil,
	}, opts...)
}

func (c *ControllerResultGenerator) WaitingOnExternal(waitDescription string, opts ...ReconcileResultOption) ReconcileResult {
	message := fmt.Sprintf("Waiting on %s", waitDescription)

	return resultWithOptions(ReconcileResult{
		ControllerResultGenerator: c,
		progressing:               c.progressingCondition(configv1.ConditionTrue, ReasonWaitingOnExternal, message),
		degraded:                  c.degradedCondition(configv1.ConditionFalse, ReasonWaitingOnExternal, message),
		err:                       nil,
	}, opts...)
}

func (c *ControllerResultGenerator) Error(err error, opts ...ReconcileResultOption) ReconcileResult {
	// If the error is a controller-runtime terminal error, return a non-retryable error
	if errors.Is(err, reconcile.TerminalError(nil)) {
		return c.nonRetryableError(err, opts...)
	}

	return resultWithOptions(ReconcileResult{
		ControllerResultGenerator: c,
		progressing:               c.progressingCondition(configv1.ConditionTrue, ReasonEphemeralError, err.Error()),
		degraded:                  c.degradedCondition(configv1.ConditionFalse, ReasonProgressing, "Controller encountered a retryable error"),
		err:                       err,
	}, opts...)
}

func (c *ControllerResultGenerator) NonRetryableError(err error, opts ...ReconcileResultOption) ReconcileResult {
	// Wrap the error in a terminal error if it's not already a terminal error
	termErr := err
	if !errors.Is(termErr, reconcile.TerminalError(nil)) {
		termErr = reconcile.TerminalError(err)
	}

	return c.nonRetryableError(termErr, opts...)
}

func (c *ControllerResultGenerator) nonRetryableError(termErr error, opts ...ReconcileResultOption) ReconcileResult {
	return resultWithOptions(ReconcileResult{
		ControllerResultGenerator: c,
		progressing:               c.progressingCondition(configv1.ConditionFalse, ReasonNonRetryableError, termErr.Error()),
		degraded:                  c.degradedCondition(configv1.ConditionTrue, ReasonNonRetryableError, termErr.Error()),
		err:                       termErr,
	}, opts...)
}

func (c *ControllerResultGenerator) condition(condType configv1.ClusterStatusConditionType, status configv1.ConditionStatus, reason, message string) *configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	return configv1apply.ClusterOperatorStatusCondition().
		WithType(condType).
		WithStatus(status).
		WithReason(reason).
		WithMessage(message)
}

func (c *ControllerResultGenerator) progressingCondition(status configv1.ConditionStatus, reason, message string) *configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	return c.condition(configv1.ClusterStatusConditionType(c.conditionPrefix+"Progressing"), status, reason, message)
}

func (c *ControllerResultGenerator) degradedCondition(status configv1.ConditionStatus, reason, message string) *configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	return c.condition(configv1.ClusterStatusConditionType(c.conditionPrefix+"Degraded"), status, reason, message)
}

// updateClusterOperatorConditions updates the RevisionController conditions on the ClusterOperator.
func (result *ReconcileResult) WriteClusterOperatorConditions(ctx context.Context, log logr.Logger, k8sclient client.Client) error {
	// Get the ClusterOperator
	co := &configv1.ClusterOperator{}
	if err := k8sclient.Get(ctx, client.ObjectKey{Name: clusterOperatorName}, co); err != nil {
		return fmt.Errorf("failed to get ClusterOperator: %w", err)
	}

	// Build conditions based on reconcile result
	conditions := []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{
		result.progressing,
		result.degraded,
	}
	needsUpdate, logConditions := mergeConditions(conditions, co.Status.Conditions)

	if !needsUpdate {
		return nil
	}

	log.Info("Updating conditions", logConditions...)

	clusterOperatorApplyConfig := configv1apply.ClusterOperator(clusterOperatorName).
		WithStatus(configv1apply.ClusterOperatorStatus().
			WithConditions(conditions...),
		)

	patch := util.ApplyConfigPatch(clusterOperatorApplyConfig)
	if err := k8sclient.Status().Patch(ctx, co, patch, client.FieldOwner(result.fieldOwner), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to patch ClusterOperator status: %w", err)
	}

	return nil
}

func mergeConditions(newConditions []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration, existingConditions []configv1.ClusterOperatorStatusCondition) (bool, []any) {
	now := metav1.Now()

	// Check if any conditions changed
	needsUpdate := false
	logConditions := make([]any, 0, len(newConditions)*2)

	findClusterOperatorCondition := func(condType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
		for i := range existingConditions {
			if existingConditions[i].Type == condType {
				return &existingConditions[i]
			}
		}

		return nil
	}

	for _, cond := range newConditions {
		if cond.Type == nil || cond.Status == nil || cond.Reason == nil || cond.Message == nil {
			// Programming error - should never happen
			panic(fmt.Sprintf("condition is missing required fields: %+v", cond))
		}

		existing := findClusterOperatorCondition(*cond.Type)

		switch {
		case existing == nil:
			needsUpdate = true

			cond.WithLastTransitionTime(now)

		// Don't update LastTransitionTime if Status/Reason are the same
		case existing.Status == *cond.Status && existing.Reason == *cond.Reason:
			cond.WithLastTransitionTime(existing.LastTransitionTime)

			if existing.Message != *cond.Message {
				needsUpdate = true
			}

		default:
			needsUpdate = true

			cond.WithLastTransitionTime(now)
		}

		logConditions = append(logConditions, *cond.Type, *cond.Status)
	}

	return needsUpdate, logConditions
}

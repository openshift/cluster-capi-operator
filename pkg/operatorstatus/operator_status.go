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
package operatorstatus

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
)

const (
	// ReasonSyncFailed is the reason for the condition when the operator failed to sync resources.
	ReasonSyncFailed = "SyncingFailed"
)

// ClusterOperatorStatusClient is a client for managing the status of the ClusterOperator object.
type ClusterOperatorStatusClient struct {
	client.Client
	Recorder          record.EventRecorder
	ManagedNamespace  string
	OperatorNamespace string
	ReleaseVersion    string
	Platform          configv1.PlatformType
}

// SetStatusAvailable sets the Available condition to True, with the given reason
// and message, and sets both the Progressing and Degraded conditions to False.
func (r *ClusterOperatorStatusClient) SetStatusAvailable(ctx context.Context, availableConditionMsg string, opts ...SyncStatusOption) error {
	log := ctrl.LoggerFrom(ctx)

	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		log.Error(err, "unable to set cluster operator status available")
		return err
	}

	if availableConditionMsg == "" {
		availableConditionMsg = fmt.Sprintf("Cluster CAPI Operator is available at %s", r.ReleaseVersion)
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		NewClusterOperatorStatusCondition(configv1.OperatorAvailable, configv1.ConditionTrue, ReasonAsExpected, availableConditionMsg),
		NewClusterOperatorStatusCondition(configv1.OperatorProgressing, configv1.ConditionFalse, ReasonAsExpected, ""),
		NewClusterOperatorStatusCondition(configv1.OperatorDegraded, configv1.ConditionFalse, ReasonAsExpected, ""),
		NewClusterOperatorStatusCondition(configv1.OperatorUpgradeable, configv1.ConditionTrue, ReasonAsExpected, ""),
	}

	log.Info("syncing status: available")

	return r.SyncStatus(ctx, co, append(opts, WithConditions(conds))...)
}

// SetStatusDegraded sets the Degraded condition to True, with the given reason and
// message, and sets the upgradeable condition.  It does not modify any existing
// Available or Progressing conditions.
func (r *ClusterOperatorStatusClient) SetStatusDegraded(ctx context.Context, reconcileErr error, opts ...SyncStatusOption) error {
	log := ctrl.LoggerFrom(ctx)

	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		log.Error(err, "unable to set cluster operator status degraded")
		return err
	}

	message := fmt.Sprintf("Failed to resync because %v", reconcileErr)

	conds := []configv1.ClusterOperatorStatusCondition{
		NewClusterOperatorStatusCondition(configv1.OperatorDegraded, configv1.ConditionTrue,
			ReasonSyncFailed, message),
		NewClusterOperatorStatusCondition(configv1.OperatorUpgradeable, configv1.ConditionFalse, ReasonAsExpected, ""),
	}

	r.Recorder.Eventf(co, corev1.EventTypeWarning, "Status degraded", reconcileErr.Error())
	log.Info("syncing status: degraded", "message", message)

	return r.SyncStatus(ctx, co, append(opts, WithConditions(conds))...)
}

// GetOrCreateClusterOperator is responsible for fetching the cluster operator should it exist,
// or creating a new cluster operator if it does not already exist.
func (r *ClusterOperatorStatusClient) GetOrCreateClusterOperator(ctx context.Context) (*configv1.ClusterOperator, error) {
	log := ctrl.LoggerFrom(ctx)

	co := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name: controllers.ClusterOperatorName,
		},
	}

	err := r.Get(ctx, client.ObjectKey{Name: controllers.ClusterOperatorName}, co)
	if errors.IsNotFound(err) {
		log.Info("ClusterOperator does not exist, creating a new one.")

		err = r.Create(ctx, co)
		if err != nil {
			return nil, fmt.Errorf("failed to create cluster operator: %w", err)
		}

		return co, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get clusterOperator %q: %w", controllers.ClusterOperatorName, err)
	}

	return co, nil
}

type syncStatusOptions struct {
	conditions []configv1.ClusterOperatorStatusCondition
	versions   []configv1.OperandVersion
}

// SyncStatusOption sets an option for the SyncStatus operation.
type SyncStatusOption func(*syncStatusOptions)

// WithConditions sets conditions for a SyncStatus operation.
func WithConditions(conditions []configv1.ClusterOperatorStatusCondition) SyncStatusOption {
	return func(o *syncStatusOptions) {
		o.conditions = conditions
	}
}

// WithVersions sets versions for a SyncStatus operation.
func WithVersions(versions []configv1.OperandVersion) SyncStatusOption {
	return func(o *syncStatusOptions) {
		o.versions = versions
	}
}

// SyncStatus performs a full sync of the ClusterOperator object if any of the
// conditions, versions, or related objects have changed.
func (r *ClusterOperatorStatusClient) SyncStatus(ctx context.Context, co *configv1.ClusterOperator, opts ...SyncStatusOption) error {
	syncOptions := syncStatusOptions{}

	for _, opt := range opts {
		opt(&syncOptions)
	}

	log := ctrl.LoggerFrom(ctx)
	patchBase := client.MergeFrom(co.DeepCopy())

	shouldUpdate := false

	for _, cond := range syncOptions.conditions {
		if !isStatusConditionPresentAndEqual(co.Status.Conditions, cond) {
			v1helpers.SetStatusCondition(&co.Status.Conditions, cond, clock.RealClock{})

			shouldUpdate = true
		}
	}

	if syncOptions.versions != nil && !equality.Semantic.DeepEqual(co.Status.Versions, syncOptions.versions) {
		co.Status.Versions = syncOptions.versions
		shouldUpdate = true
	}

	if shouldUpdate {
		log.Info("syncing status", "message", v1helpers.GetStatusDiff(co.Status, co.Status))

		if err := r.Client.Status().Patch(ctx, co, patchBase); err != nil {
			return fmt.Errorf("failed to update cluster operator status: %w", err)
		}
	}

	return nil
}

// OperandVersions returns the operand versions for the ClusterOperator.
func (r *ClusterOperatorStatusClient) OperandVersions() []configv1.OperandVersion {
	return []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}
}

// NewClusterOperatorStatusCondition creates a new ClusterOperatorStatusCondition.
func NewClusterOperatorStatusCondition(conditionType configv1.ClusterStatusConditionType,
	conditionStatus configv1.ConditionStatus, reason string,
	message string) configv1.ClusterOperatorStatusCondition {
	return configv1.ClusterOperatorStatusCondition{
		Type:               conditionType,
		Status:             conditionStatus,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

// isStatusConditionPresentAndEqual returns true when cond is present and equal.
func isStatusConditionPresentAndEqual(conditions []configv1.ClusterOperatorStatusCondition, cond configv1.ClusterOperatorStatusCondition) bool {
	for _, condition := range conditions {
		if condition.Type == cond.Type {
			return condition.Status == cond.Status && condition.Reason == cond.Reason && condition.Message == cond.Message
		}
	}

	return false
}

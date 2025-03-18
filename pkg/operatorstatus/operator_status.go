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
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
)

const (
	// ReasonAsExpected is the reason for the condition when the operator is in a normal state.
	ReasonAsExpected = "AsExpected"

	// ReasonInitializing is the reason for the condition when the operator is initializing.
	ReasonInitializing = "Initializing"

	// ReasonSyncing is the reason for the condition when the operator is syncing resources.
	ReasonSyncing = "SyncingResources"

	// ReasonSyncFailed is the reason for the condition when the operator failed to sync resources.
	ReasonSyncFailed = "SyncingFailed"
)

// ClusterOperatorStatusClient is a client for managing the status of the ClusterOperator object.
type ClusterOperatorStatusClient struct {
	client.Client
	Recorder         record.EventRecorder
	ManagedNamespace string
	ReleaseVersion   string
}

// SetStatusAvailable sets the Available condition to True, with the given reason
// and message, and sets both the Progressing and Degraded conditions to False.
func (r *ClusterOperatorStatusClient) SetStatusAvailable(ctx context.Context, availableConditionMsg string) error {
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

	if co, shouldUpdate := clusterObjectNeedsUpdating(co, conds, r.operandVersions(), r.relatedObjects()); shouldUpdate {
		log.V(2).Info("syncing status: available")
		return r.SyncStatus(ctx, co, conds)
	}

	return nil
}

// SetStatusDegraded sets the Degraded condition to True, with the given reason and
// message, and sets the upgradeable condition.  It does not modify any existing
// Available or Progressing conditions.
func (r *ClusterOperatorStatusClient) SetStatusDegraded(ctx context.Context, reconcileErr error) error {
	log := ctrl.LoggerFrom(ctx)

	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		log.Error(err, "unable to set cluster operator status degraded")
		return err
	}

	desiredVersions := r.operandVersions()
	currentVersions := co.Status.Versions

	var message string
	if !reflect.DeepEqual(desiredVersions, currentVersions) {
		message = fmt.Sprintf("Failed when progressing towards %s because %e", printOperandVersions(desiredVersions), reconcileErr)
	} else {
		message = fmt.Sprintf("Failed to resync for %s because %e", printOperandVersions(desiredVersions), reconcileErr)
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		NewClusterOperatorStatusCondition(configv1.OperatorDegraded, configv1.ConditionTrue,
			ReasonSyncFailed, message),
		NewClusterOperatorStatusCondition(configv1.OperatorUpgradeable, configv1.ConditionFalse, ReasonAsExpected, ""),
	}

	// Update cluster conditions only if they have been changed
	for _, cond := range conds {
		if !v1helpers.IsStatusConditionPresentAndEqual(co.Status.Conditions, cond.Type, cond.Status) {
			r.Recorder.Eventf(co, corev1.EventTypeWarning, "Status degraded", reconcileErr.Error())
			log.V(2).Info("syncing status: degraded", "message", message)

			return r.SyncStatus(ctx, co, conds)
		}
	}

	return nil
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

	err := r.Client.Get(ctx, client.ObjectKey{Name: controllers.ClusterOperatorName}, co)
	if errors.IsNotFound(err) {
		log.Info("ClusterOperator does not exist, creating a new one.")

		err = r.Client.Create(ctx, co)
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

// SyncStatus applies the new condition to the ClusterOperator object.
func (r *ClusterOperatorStatusClient) SyncStatus(ctx context.Context, co *configv1.ClusterOperator, conds []configv1.ClusterOperatorStatusCondition) error {
	for _, c := range conds {
		v1helpers.SetStatusCondition(&co.Status.Conditions, c)
	}

	if err := r.Client.Status().Update(ctx, co); err != nil {
		return fmt.Errorf("failed to update cluster operator status: %w", err)
	}

	return nil
}

func (r *ClusterOperatorStatusClient) relatedObjects() []configv1.ObjectReference {
	// TBD: Add an actual set of object references from getResources method
	return []configv1.ObjectReference{
		{Resource: "namespaces", Name: controllers.DefaultManagedNamespace},
		{Group: configv1.GroupName, Resource: "clusteroperators", Name: controllers.ClusterOperatorName},
		{Resource: "namespaces", Name: r.ManagedNamespace},
		{Group: "", Resource: "serviceaccounts", Name: "cluster-capi-operator"},
		{Group: "", Resource: "configmaps", Name: "cluster-capi-operator-images"},
		{Group: "apps", Resource: "deployments", Name: "cluster-capi-operator"},
	}
}
func (r *ClusterOperatorStatusClient) operandVersions() []configv1.OperandVersion {
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

func printOperandVersions(versions []configv1.OperandVersion) string {
	versionsOutput := []string{}
	for _, operand := range versions {
		versionsOutput = append(versionsOutput, fmt.Sprintf("%s: %s", operand.Name, operand.Version))
	}

	return strings.Join(versionsOutput, ", ")
}

func clusterObjectNeedsUpdating(co *configv1.ClusterOperator, conds []configv1.ClusterOperatorStatusCondition, desiredVersions []configv1.OperandVersion, desiredRelatedObjects []configv1.ObjectReference) (*configv1.ClusterOperator, bool) {
	shouldUpdate := false

	for _, cond := range conds {
		if !v1helpers.IsStatusConditionPresentAndEqual(co.Status.Conditions, cond.Type, cond.Status) {
			shouldUpdate = true
		}
	}

	if !equality.Semantic.DeepEqual(co.Status.Versions, desiredVersions) {
		co.Status.Versions = desiredVersions
		shouldUpdate = true
	}

	if !equality.Semantic.DeepEqual(co.Status.RelatedObjects, desiredRelatedObjects) {
		co.Status.RelatedObjects = desiredRelatedObjects
		shouldUpdate = true
	}

	return co, shouldUpdate
}

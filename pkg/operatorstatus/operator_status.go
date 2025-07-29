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
	"sort"
	"strings"

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
	Platform         configv1.PlatformType
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

	log.Info("syncing status: available")

	return r.SyncStatus(ctx, co, conds, r.OperandVersions(), r.RelatedObjects())
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

	desiredVersions := r.OperandVersions()
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

	r.Recorder.Eventf(co, corev1.EventTypeWarning, "Status degraded", reconcileErr.Error())
	log.Info("syncing status: degraded", "message", message)

	// We pass in currentVersion and not desiredVersion because we are degraded
	// and as such we are still progressing towards the desired version.
	return r.SyncStatus(ctx, co, conds, currentVersions, r.RelatedObjects())
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

// SyncStatus performs a full sync of the ClusterOperator object if any of the
// conditions, versions, or related objects have changed.
func (r *ClusterOperatorStatusClient) SyncStatus(ctx context.Context, co *configv1.ClusterOperator, desiredConditions []configv1.ClusterOperatorStatusCondition, desiredVersions []configv1.OperandVersion, desiredRelatedObjects []configv1.ObjectReference) error {
	log := ctrl.LoggerFrom(ctx)
	patchBase := client.MergeFrom(co.DeepCopy())

	shouldUpdate := false

	for _, cond := range desiredConditions {
		if !isStatusConditionPresentAndEqual(co.Status.Conditions, cond) {
			v1helpers.SetStatusCondition(&co.Status.Conditions, cond, clock.RealClock{})

			shouldUpdate = true
		}
	}

	if !equality.Semantic.DeepEqual(co.Status.Versions, desiredVersions) {
		co.Status.Versions = desiredVersions
		shouldUpdate = true
	}

	if !isRelatedObjectsDeepEqual(co.Status.RelatedObjects, desiredRelatedObjects) {
		co.Status.RelatedObjects = desiredRelatedObjects
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

func platformToInfraPrefix(platform configv1.PlatformType) string {
	switch platform {
	case configv1.BareMetalPlatformType:
		return "Metal3"
	default:
		return string(platform)
	}
}

// RelatedObjects returns the related objects for the ClusterOperator.
func (r *ClusterOperatorStatusClient) RelatedObjects() []configv1.ObjectReference {
	references := []configv1.ObjectReference{
		{Group: "", Resource: "namespaces", Name: r.ManagedNamespace},
		{Group: "", Resource: "serviceaccounts", Name: "cluster-capi-operator", Namespace: controllers.DefaultManagedNamespace},
		{Group: "", Resource: "configmaps", Name: "cluster-capi-operator-images", Namespace: controllers.DefaultManagedNamespace},
		{Group: "apps", Resource: "deployments", Name: "cluster-capi-operator", Namespace: controllers.DefaultManagedNamespace},
		{Group: configv1.GroupName, Resource: "clusteroperators", Name: controllers.ClusterOperatorName},
		{Group: "cluster.x-k8s.io", Resource: "clusters", Namespace: r.ManagedNamespace},
		{Group: "cluster.x-k8s.io", Resource: "machines", Namespace: r.ManagedNamespace},
		{Group: "cluster.x-k8s.io", Resource: "machinesets", Namespace: r.ManagedNamespace},
	}

	platformPrefix := platformToInfraPrefix(r.Platform)

	for groupVersionKind, t := range r.Scheme().AllKnownTypes() {
		if strings.HasSuffix(groupVersionKind.Group, "cluster.x-k8s.io") {
			// Ignore lists
			if _, found := t.FieldByName("ObjectMeta"); !found {
				continue
			}

			if strings.HasPrefix(t.Name(), platformPrefix) {
				ref := configv1.ObjectReference{
					Group:     groupVersionKind.Group,
					Resource:  strings.ToLower(t.Name()),
					Namespace: r.ManagedNamespace,
				}

				references = append(references, ref)
			}
		}
	}

	return references
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

func printOperandVersions(versions []configv1.OperandVersion) string {
	versionsOutput := []string{}
	for _, operand := range versions {
		versionsOutput = append(versionsOutput, fmt.Sprintf("%s: %s", operand.Name, operand.Version))
	}

	return strings.Join(versionsOutput, ", ")
}

// isRelatedObjectsDeepEqual compares two slices of ObjectReference and returns true if they are equal.
// Slices that have the same elements but different ordering are still considered equal.
func isRelatedObjectsDeepEqual(currentRelatedObjects, desiredRelatedObjects []configv1.ObjectReference) bool {
	// Deep copy current related objects to avoid modifying the original slice.
	currentRelatedObjectsCopy := make([]configv1.ObjectReference, len(currentRelatedObjects))
	copy(currentRelatedObjectsCopy, currentRelatedObjects)

	// Deep copy desired related objects to avoid modifying the original slice.
	desiredRelatedObjectsCopy := make([]configv1.ObjectReference, len(desiredRelatedObjects))
	copy(desiredRelatedObjectsCopy, desiredRelatedObjects)

	// Sort current and desired related objects to make a consistent comparison.
	// This is necessary because the related objects are not sorted by default.
	sortRelatedObjects(currentRelatedObjectsCopy)
	sortRelatedObjects(desiredRelatedObjectsCopy)

	return equality.Semantic.DeepEqual(currentRelatedObjectsCopy, desiredRelatedObjectsCopy)
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

// sortRelatedObjects sorts the related objects by group and then by resource.
func sortRelatedObjects(relatedObjects []configv1.ObjectReference) {
	sort.Slice(relatedObjects, func(i, j int) bool {
		a, b := relatedObjects[i], relatedObjects[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if a.Resource != b.Resource { //nolint:wsl
			return a.Resource < b.Resource
		}
		if a.Namespace != b.Namespace { //nolint:wsl
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name //nolint:wsl,nlreturn
	})
}

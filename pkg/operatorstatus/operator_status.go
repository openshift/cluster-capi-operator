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
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	featuregates "github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
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

	// ReasonControllersNotAvailable is the reason for the condition when one or more of the operator's controllers are not available.
	ReasonControllersNotAvailable = "ControllersNotAvailable"

	// ReasonControllersDegraded is the reason for the condition when one or more of the operator's controllers are degraded.
	ReasonControllersDegraded = "ControllersDegraded"

	// CapiInstallerControllerAvailableCondition is the condition type that indicates the CapiInstaller controller is available.
	CapiInstallerControllerAvailableCondition = "CapiInstallerControllerAvailable"

	// CapiInstallerControllerDegradedCondition is the condition type that indicates the CapiInstaller controller is degraded.
	CapiInstallerControllerDegradedCondition = "CapiInstallerControllerDegraded"

	// CoreClusterControllerAvailableCondition is the condition type that indicates the CoreCluster controller is available.
	CoreClusterControllerAvailableCondition = "CoreClusterControllerAvailable"

	// CoreClusterControllerDegradedCondition is the condition type that indicates the CoreCluster controller is degraded.
	CoreClusterControllerDegradedCondition = "CoreClusterControllerDegraded"

	// InfraClusterControllerAvailableCondition is the condition type that indicates the InfraCluster controller is available.
	InfraClusterControllerAvailableCondition = "InfraClusterControllerAvailable"

	// InfraClusterControllerDegradedCondition is the condition type that indicates the InfraCluster controller is degraded.
	InfraClusterControllerDegradedCondition = "InfraClusterControllerDegraded"

	// KubeconfigControllerAvailableCondition is the condition type that indicates the Kubeconfig controller is available.
	KubeconfigControllerAvailableCondition = "KubeconfigControllerAvailable"

	// KubeconfigControllerDegradedCondition is the condition type that indicates the Kubeconfig controller is degraded.
	KubeconfigControllerDegradedCondition = "KubeconfigControllerDegraded"

	// MachineSetSyncControllerAvailableCondition is the condition type that indicates the MachineSetSync controller is available.
	MachineSetSyncControllerAvailableCondition = "MachineSetSyncControllerAvailable"

	// MachineSetSyncControllerDegradedCondition is the condition type that indicates the MachineSetSync controller is degraded.
	MachineSetSyncControllerDegradedCondition = "MachineSetSyncControllerDegraded"

	// MachineSyncControllerAvailableCondition is the condition type that indicates the MachineSync controller is available.
	MachineSyncControllerAvailableCondition = "MachineSyncControllerAvailable"

	// MachineSyncControllerDegradedCondition is the condition type that indicates the MachineSync controller is degraded.
	MachineSyncControllerDegradedCondition = "MachineSyncControllerDegraded"

	// SecretSyncControllerAvailableCondition is the condition type that indicates the SecretSync controller is available.
	SecretSyncControllerAvailableCondition = "SecretSyncControllerAvailable"

	// SecretSyncControllerDegradedCondition is the condition type that indicates the SecretSync controller is degraded.
	SecretSyncControllerDegradedCondition = "SecretSyncControllerDegraded"
)

// ClusterOperatorStatusClient is a client for managing the status of the ClusterOperator object.
type ClusterOperatorStatusClient struct {
	client.Client
	Recorder         record.EventRecorder
	ManagedNamespace string
	ReleaseVersion   string
	FeatureGates     featuregates.FeatureGate
}

// SetStatus sets the status for the ClusterOperator.
func (r *ClusterOperatorStatusClient) SetStatus(ctx context.Context, isUnsupportedPlatform bool) error {
	log := ctrl.LoggerFrom(ctx)

	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		log.Error(err, "unable to get or create cluster operator")
		return err
	}

	var conds []configv1.ClusterOperatorStatusCondition

	if isUnsupportedPlatform {
		conds = unsupportedConditions()
	} else {
		conds = aggregatedStatusConditions(co, r.ReleaseVersion, r.FeatureGates)
	}

	if co, shouldUpdate := clusterObjectNeedsUpdating(co, conds, r.operandVersions(), r.relatedObjects()); shouldUpdate {
		return r.SyncStatus(ctx, co, conds)
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

// SyncStatus applies the new condition to the ClusterOperator object.
func (r *ClusterOperatorStatusClient) SyncStatus(ctx context.Context, co *configv1.ClusterOperator, conds []configv1.ClusterOperatorStatusCondition) error {
	for _, c := range conds {
		v1helpers.SetStatusCondition(&co.Status.Conditions, c)
	}

	if err := r.Status().Update(ctx, co); err != nil {
		return fmt.Errorf("unable to update cluster-api cluster operator status: %w", err)
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
func NewClusterOperatorStatusCondition(conditionType configv1.ClusterStatusConditionType, conditionStatus configv1.ConditionStatus, reason string, message string) configv1.ClusterOperatorStatusCondition {
	return configv1.ClusterOperatorStatusCondition{
		Type:               conditionType,
		Status:             conditionStatus,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

func unsupportedConditions() []configv1.ClusterOperatorStatusCondition {
	return []configv1.ClusterOperatorStatusCondition{
		NewClusterOperatorStatusCondition(configv1.OperatorAvailable, configv1.ConditionTrue, ReasonAsExpected, "Cluster API is not yet implemented on this platform"),
		NewClusterOperatorStatusCondition(configv1.OperatorProgressing, configv1.ConditionFalse, ReasonAsExpected, ""),
		NewClusterOperatorStatusCondition(configv1.OperatorDegraded, configv1.ConditionFalse, ReasonAsExpected, ""),
		NewClusterOperatorStatusCondition(configv1.OperatorUpgradeable, configv1.ConditionTrue, ReasonAsExpected, ""),
	}
}

func aggregatedStatusConditions(co *configv1.ClusterOperator, releaseVersion string, currentFeatureGates featuregates.FeatureGate) []configv1.ClusterOperatorStatusCondition {
	type controllerCondition struct {
		available configv1.ClusterStatusConditionType
		degraded  configv1.ClusterStatusConditionType
	}

	// Define the controller conditions
	controllerConditions := []controllerCondition{
		{CapiInstallerControllerAvailableCondition, CapiInstallerControllerDegradedCondition},
		{CoreClusterControllerAvailableCondition, CoreClusterControllerDegradedCondition},
		{InfraClusterControllerAvailableCondition, InfraClusterControllerDegradedCondition},
		{KubeconfigControllerAvailableCondition, KubeconfigControllerDegradedCondition},
		{SecretSyncControllerAvailableCondition, SecretSyncControllerDegradedCondition},
	}

	if currentFeatureGates != nil && currentFeatureGates.Enabled(features.FeatureGateMachineAPIMigration) {
		controllerConditions = append(controllerConditions, controllerCondition{MachineSyncControllerAvailableCondition, MachineSyncControllerDegradedCondition})
		controllerConditions = append(controllerConditions, controllerCondition{MachineSetSyncControllerAvailableCondition, MachineSetSyncControllerDegradedCondition})
	}

	// Variables to store the conditions evaluation.
	availableMsg, degradedMsg := []string{}, []string{}
	anyAvailableMissing, anyDegradedMissing := false, false

	// Evaluate each controller's conditions.
	for _, conditions := range controllerConditions {
		evaluateCondition(co, conditions.available, &anyAvailableMissing, &availableMsg, false)
		evaluateCondition(co, conditions.degraded, &anyDegradedMissing, &degradedMsg, true)
	}

	availableCondition := newAvailableCondition(anyAvailableMissing, availableMsg, releaseVersion)
	degradedCondition := newDegradedCondition(anyDegradedMissing, degradedMsg)
	progressingCondition := newCondition(configv1.OperatorProgressing, configv1.ConditionFalse, "", "")

	upgradeableStatus := configv1.ConditionUnknown

	switch {
	case availableCondition.Status == "True" && degradedCondition.Status == "False":
		upgradeableStatus = configv1.ConditionTrue
	case availableCondition.Status == "False" || degradedCondition.Status == "True":
		upgradeableStatus = configv1.ConditionFalse
	}

	upgradeableCondition := newCondition(configv1.OperatorUpgradeable, upgradeableStatus, "", "")

	return []configv1.ClusterOperatorStatusCondition{
		availableCondition,
		degradedCondition,
		progressingCondition,
		upgradeableCondition,
	}
}

// evaluateCondition helps to evaluate conditions and update states.
func evaluateCondition(co *configv1.ClusterOperator, conditionType configv1.ClusterStatusConditionType, anyMissing *bool, messages *[]string, isDegraded bool) {
	condition := findCondition(co.Status.Conditions, conditionType)
	if condition == nil {
		*anyMissing = true
		return
	}

	expectedStatus := configv1.ConditionFalse
	if isDegraded {
		expectedStatus = configv1.ConditionTrue
	}

	if condition.Status == expectedStatus {
		statusString := fmt.Sprintf("%s=%s", conditionType, condition.Status)
		*messages = append(*messages, statusString)
	}
}

// newAvailableCondition helps to create the Available condition.
func newAvailableCondition(anyMissing bool, messages []string, releaseVersion string) configv1.ClusterOperatorStatusCondition {
	if anyMissing {
		return newCondition(configv1.OperatorAvailable, configv1.ConditionUnknown, "", "")
	}

	if len(messages) == 0 {
		return newCondition(configv1.OperatorAvailable, configv1.ConditionTrue, ReasonAsExpected, fmt.Sprintf("Cluster CAPI Operator is available at %s", releaseVersion))
	}

	return newCondition(configv1.OperatorAvailable, configv1.ConditionFalse, "ControllersNotAvailable", fmt.Sprintf("The following controllers available conditions are not as expected: %s", strings.Join(messages, ", ")))
}

// newAvailableCondition helps to create the Degraded condition.
func newDegradedCondition(anyMissing bool, messages []string) configv1.ClusterOperatorStatusCondition {
	if anyMissing {
		return newCondition(configv1.OperatorDegraded, configv1.ConditionUnknown, "", "")
	}

	if len(messages) == 0 {
		return newCondition(configv1.OperatorDegraded, configv1.ConditionFalse, ReasonAsExpected, "")
	}

	return newCondition(configv1.OperatorDegraded, configv1.ConditionTrue, ReasonControllersDegraded, fmt.Sprintf("The following controllers degraded conditions are not as expected: %s", strings.Join(messages, ", ")))
}

// newCondition helps to create a new condition with optional message.
func newCondition(conditionType configv1.ClusterStatusConditionType, status configv1.ConditionStatus, reason, message string) configv1.ClusterOperatorStatusCondition {
	return configv1.ClusterOperatorStatusCondition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
}

// findCondition helps find a condition by type in a slice of conditions.
func findCondition(conditions []configv1.ClusterOperatorStatusCondition, conditionType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
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

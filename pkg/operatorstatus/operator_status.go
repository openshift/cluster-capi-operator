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
	ReasonAsExpected   = "AsExpected"
	ReasonInitializing = "Initializing"
	ReasonSyncing      = "SyncingResources"
	ReasonSyncFailed   = "SyncingFailed"
)

type ClusterOperatorStatusClient struct {
	client.Client
	Recorder         record.EventRecorder
	ManagedNamespace string
	ReleaseVersion   string
}

// setStatusAvailable sets the Available condition to True, with the given reason
// and message, and sets both the Progressing and Degraded conditions to False.
func (r *ClusterOperatorStatusClient) SetStatusAvailable(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)

	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		log.Error(err, "unable to set cluster operator status available")
		return err
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		NewClusterOperatorStatusCondition(configv1.OperatorAvailable, configv1.ConditionTrue, ReasonAsExpected,
			fmt.Sprintf("Cluster CAPI Operator is available at %s", r.ReleaseVersion)),
		NewClusterOperatorStatusCondition(configv1.OperatorProgressing, configv1.ConditionFalse, ReasonAsExpected, ""),
		NewClusterOperatorStatusCondition(configv1.OperatorDegraded, configv1.ConditionFalse, ReasonAsExpected, ""),
		NewClusterOperatorStatusCondition(configv1.OperatorUpgradeable, configv1.ConditionTrue, ReasonAsExpected, ""),
	}

	// Update cluster conditions only if they have been changed
	for _, cond := range conds {
		if !v1helpers.IsStatusConditionPresentAndEqual(co.Status.Conditions, cond.Type, cond.Status) {
			co.Status.Versions = []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}
			log.V(2).Info("syncing status: available")
			return r.SyncStatus(ctx, co, conds)
		}
	}

	return nil
}

// setStatusDegraded sets the Degraded condition to True, with the given reason and
// message, and sets the upgradeable condition.  It does not modify any existing
// Available or Progressing conditions.
func (r *ClusterOperatorStatusClient) SetStatusDegraded(ctx context.Context, reconcileErr error) error {
	log := ctrl.LoggerFrom(ctx)

	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		log.Error(err, "unable to set cluster operator status degraded")
		return err
	}

	desiredVersions := []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}
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

func (r *ClusterOperatorStatusClient) GetOrCreateClusterOperator(ctx context.Context) (*configv1.ClusterOperator, error) {
	log := ctrl.LoggerFrom(ctx)

	co := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name: controllers.ClusterOperatorName,
		},
		Status: configv1.ClusterOperatorStatus{},
	}
	err := r.Client.Get(ctx, client.ObjectKey{Name: controllers.ClusterOperatorName}, co)
	if errors.IsNotFound(err) {
		log.Info("ClusterOperator does not exist, creating a new one.")

		err = r.Client.Create(ctx, co)
		if err != nil {
			return nil, fmt.Errorf("failed to create cluster operator: %v", err)
		}
		return co, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get clusterOperator %q: %v", controllers.ClusterOperatorName, err)
	}
	return co, nil
}

// syncStatus applies the new condition to the ClusterOperator object.
func (r *ClusterOperatorStatusClient) SyncStatus(ctx context.Context, co *configv1.ClusterOperator, conds []configv1.ClusterOperatorStatusCondition) error {
	for _, c := range conds {
		v1helpers.SetStatusCondition(&co.Status.Conditions, c)
	}

	if !equality.Semantic.DeepEqual(co.Status.RelatedObjects, r.relatedObjects()) {
		co.Status.RelatedObjects = r.relatedObjects()
	}

	return r.Client.Status().Update(ctx, co)
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

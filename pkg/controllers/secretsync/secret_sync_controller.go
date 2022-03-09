package secretsync

import (
	"context"
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	managedUserDataSecretName = "worker-user-data"
	SecretSourceNamespace     = "openshift-machine-api"

	// Controller conditions for the Cluster Operator resource
	secretSyncControllerAvailableCondition = "SecretSyncControllerAvailable"
	secretSyncControllerDegradedCondition  = "SecretSyncControllerDegraded"
)

type UserDataSecretController struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme *runtime.Scheme
}

func (r *UserDataSecretController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.Infof("%s emitted event, syncing user data secret", req)

	defaultSourceSecretObjectKey := client.ObjectKey{
		Name: managedUserDataSecretName, Namespace: SecretSourceNamespace,
	}
	sourceSecret := &corev1.Secret{}
	if err := r.Get(ctx, defaultSourceSecretObjectKey, sourceSecret); err != nil {
		klog.Errorf("unable to get secret for sync")
		if err := r.setDegradedCondition(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set conditions for secret sync controller: %v", err)
		}
		return ctrl.Result{}, err
	}

	targetSecret := &corev1.Secret{}
	targetSecretKey := client.ObjectKey{
		Namespace: r.ManagedNamespace,
		Name:      managedUserDataSecretName,
	}

	// If the secret does not exist, it will be created later, so we can ignore a Not Found error
	if err := r.Get(ctx, targetSecretKey, targetSecret); err != nil && !errors.IsNotFound(err) {
		klog.Errorf("unable to get target secret for sync")
		if err := r.setDegradedCondition(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set conditions for secret controller: %v", err)
		}
		return ctrl.Result{}, err
	}

	if r.areSecretsEqual(sourceSecret, targetSecret) {
		klog.Infof("source and target secrets are equal, no sync needed")
		if err := r.setAvailableCondition(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set conditions for user data secret controller: %v", err)
		}
		return ctrl.Result{}, nil
	}

	if err := r.syncSecretData(ctx, sourceSecret, targetSecret); err != nil {
		klog.Errorf("unable to sync user data secret")
		if err := r.setDegradedCondition(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set conditions for user data secret controller: %v", err)
		}
		return ctrl.Result{}, err
	}

	if err := r.setAvailableCondition(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set conditions for user data secret controller: %v", err)
	}

	return ctrl.Result{}, nil
}

func (r *UserDataSecretController) areSecretsEqual(source *corev1.Secret, target *corev1.Secret) bool {
	return source.Immutable == target.Immutable &&
		reflect.DeepEqual(source.Data, target.Data) && reflect.DeepEqual(source.StringData, target.StringData) &&
		source.Type == target.Type
}

func (r *UserDataSecretController) syncSecretData(ctx context.Context, source *corev1.Secret, target *corev1.Secret) error {
	target.SetName(managedUserDataSecretName)
	target.SetNamespace(r.ManagedNamespace)
	target.Data = source.Data
	target.StringData = source.StringData
	target.Immutable = source.Immutable
	target.Type = source.Type

	// check if the target secret exists, create if not
	err := r.Get(ctx, client.ObjectKeyFromObject(target), &corev1.Secret{})
	if err != nil && errors.IsNotFound(err) {
		return r.Create(ctx, target)
	} else if err != nil {
		return err
	}

	return r.Update(ctx, target)
}

// SetupWithManager sets up the controller with the Manager.
func (r *UserDataSecretController) SetupWithManager(mgr ctrl.Manager) error {
	build := ctrl.NewControllerManagedBy(mgr).
		For(
			&corev1.Secret{},
			builder.WithPredicates(userDataSecretPredicate(r.ManagedNamespace)),
		).
		Watches(
			&source.Kind{Type: &corev1.Secret{}},
			handler.EnqueueRequestsFromMapFunc(toUserDataSecret),
			builder.WithPredicates(userDataSecretPredicate(SecretSourceNamespace)),
		)

	return build.Complete(r)
}

func (r *UserDataSecretController) setAvailableCondition(ctx context.Context) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return err
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(secretSyncControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"User Data Secret Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(secretSyncControllerDegradedCondition, configv1.ConditionFalse, operatorstatus.ReasonAsExpected,
			"User Data Secret Controller works as expected"),
	}

	co.Status.Versions = []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}
	klog.Info("User Data Secret Controller is available")
	return r.SyncStatus(ctx, co, conds)
}

func (r *UserDataSecretController) setDegradedCondition(ctx context.Context) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return err
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(secretSyncControllerAvailableCondition, configv1.ConditionFalse, operatorstatus.ReasonSyncFailed,
			"User Data Secret Controller failed to sync secret"),
		operatorstatus.NewClusterOperatorStatusCondition(secretSyncControllerDegradedCondition, configv1.ConditionTrue, operatorstatus.ReasonSyncFailed,
			"User Data Secret Controller failed to sync secret"),
	}

	co.Status.Versions = []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}
	klog.Info("User Data Secret Controller is degraded")
	return r.SyncStatus(ctx, co, conds)
}

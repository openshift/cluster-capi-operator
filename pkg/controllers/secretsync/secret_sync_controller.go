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
package secretsync

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	managedUserDataSecretName = "worker-user-data"

	mapiUserDataKey = "userData"
	capiUserDataKey = "value"

	controllerName = "SecretSyncController"
)

var (
	errSourceSecretMissingUserData = errors.New("source secret does not have user data")
)

// SecretSyncController reconciles a Secret object containing machine user data, from the Machine API to Cluster API namespaces.
type SecretSyncController struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme          *runtime.Scheme
	SourceNamespace string
}

// Reconcile reconciles the user data secret.
func (r *SecretSyncController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)
	log.Info("reconciling worker user data secret")

	defaultSourceSecretObjectKey := client.ObjectKey{
		Namespace: r.SourceNamespace,
		Name:      managedUserDataSecretName,
	}
	sourceSecret := &corev1.Secret{}

	if err := r.Get(ctx, defaultSourceSecretObjectKey, sourceSecret); err != nil {
		log.Error(err, "unable to get source secret for sync")

		return ctrl.Result{}, fmt.Errorf("failed to get source secret: %w", err)
	}

	targetSecret := &corev1.Secret{}
	targetSecretKey := client.ObjectKey{
		Namespace: r.ManagedNamespace,
		Name:      managedUserDataSecretName,
	}

	// If the secret does not exist, it will be created later, so we can ignore a Not Found error
	if err := r.Get(ctx, targetSecretKey, targetSecret); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "unable to get target secret for sync")

		return ctrl.Result{}, fmt.Errorf("failed to get target secret: %w", err)
	}

	if r.areSecretsEqual(sourceSecret, targetSecret) {
		log.Info("user data in source and target secrets is the same, no sync needed")

		if err := r.setControllerConditionsToNormal(ctx, log); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set conditions for secret sync controller: %w", err)
		}

		return ctrl.Result{}, nil
	}

	if err := r.syncSecretData(ctx, sourceSecret, targetSecret); err != nil {
		log.Error(err, "unable to sync user data secret")
		return ctrl.Result{}, err
	}

	if err := r.setControllerConditionsToNormal(ctx, log); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set conditions for secret sync controller: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *SecretSyncController) areSecretsEqual(source *corev1.Secret, target *corev1.Secret) bool {
	return source.Immutable == target.Immutable &&
		reflect.DeepEqual(source.Data[mapiUserDataKey], target.Data[capiUserDataKey]) && reflect.DeepEqual(source.StringData, target.StringData) &&
		source.Type == target.Type
}

func (r *SecretSyncController) syncSecretData(ctx context.Context, source *corev1.Secret, target *corev1.Secret) error {
	userData := source.Data[mapiUserDataKey]
	if userData == nil {
		return errSourceSecretMissingUserData
	}

	target.SetName(managedUserDataSecretName)
	target.SetNamespace(r.ManagedNamespace)
	target.Data = map[string][]byte{
		"value":  userData,
		"format": []byte("ignition"),
	}
	target.StringData = source.StringData
	target.Immutable = source.Immutable
	target.Type = source.Type

	// check if the target secret exists, create if not
	err := r.Get(ctx, client.ObjectKeyFromObject(target), &corev1.Secret{})
	if err != nil && apierrors.IsNotFound(err) {
		if err := r.Create(ctx, target); err != nil {
			return fmt.Errorf("failed to create target secret: %w", err)
		}

		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get target secret: %w", err)
	}

	if err := r.Update(ctx, target); err != nil {
		return fmt.Errorf("failed to update target secret: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretSyncController) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(
			&corev1.Secret{},
			builder.WithPredicates(userDataSecretPredicate(r.ManagedNamespace)),
		).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(toUserDataSecret(r.SourceNamespace)),
			builder.WithPredicates(userDataSecretPredicate(r.SourceNamespace)),
		).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create secret sync controller: %w", err)
	}

	return nil
}

// setControllerConditionsToNormal sets the SecretSyncController conditions to the normal state.
func (r *SecretSyncController) setControllerConditionsToNormal(ctx context.Context, log logr.Logger) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster operator: %w", err)
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.SecretSyncControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"Secret Sync Controller Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.SecretSyncControllerDegradedCondition, configv1.ConditionFalse, operatorstatus.ReasonAsExpected,
			"Secret Sync Controller Controller works as expected"),
	}

	log.V(2).Info("Secret Sync Controller Controller is Available")

	if err := r.SyncStatus(ctx, co, conds); err != nil {
		return fmt.Errorf("failed to sync cluster operator status: %w", err)
	}

	return nil
}

// setControllerConditionDegraded sets the UserDataSecretController conditions to a degraded state.
//
//nolint:unused
func (r *SecretSyncController) setControllerConditionDegraded(ctx context.Context, log logr.Logger, reconcileErr error) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster operator: %w", err)
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.SecretSyncControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"Secret Sync Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.SecretSyncControllerDegradedCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			fmt.Sprintf("Secret Sync Controller is degraded: %s", reconcileErr.Error())),
	}

	log.Info("Secret Sync Controller is Degraded", reconcileErr.Error())

	if err := r.SyncStatus(ctx, co, conds); err != nil {
		return fmt.Errorf("failed to sync cluster operator status: %w", err)
	}

	return nil
}

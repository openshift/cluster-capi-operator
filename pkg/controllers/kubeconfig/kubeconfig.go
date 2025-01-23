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
package kubeconfig

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	controllerName  = "KubeconfigController"
	tokenSecretName = "cluster-capi-operator-secret" //nolint
)

// KubeconfigController reconciles a ClusterOperator object.
type KubeconfigController struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme      *runtime.Scheme
	RestCfg     *rest.Config
	clusterName string
}

// SetupWithManager sets up the controller with the Manager.
func (r *KubeconfigController) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(
			&corev1.Secret{},
			builder.WithPredicates(tokenSecretPredicate(r.ManagedNamespace)),
		).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(toTokenSecret(r.ManagedNamespace)),
			builder.WithPredicates(kubeconfigSecretPredicate(r.ManagedNamespace)),
		).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// Reconcile reconciles the kubeconfig secret.
func (r *KubeconfigController) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)

	infra := &configv1.Infrastructure{}
	if err := r.Get(ctx, client.ObjectKey{Name: controllers.InfrastructureResourceName}, infra); err != nil {
		log.Error(err, "Unable to retrieve Infrastructure object")
		return ctrl.Result{}, fmt.Errorf("unable to retrieve Infrastructure object: %w", err)
	}

	if infra.Status.PlatformStatus == nil {
		log.Info("No platform status exists in infrastructure object. Skipping kubeconfig reconciliation...")

		if err := r.setControllerConditionsToNormal(ctx, log); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set conditions for kubeconfig controller: %w", err)
		}

		return ctrl.Result{}, nil
	}

	r.clusterName = infra.Status.InfrastructureName

	log.Info("Reconciling kubeconfig secret")

	res, err := r.reconcileKubeconfig(ctx, log)
	if err != nil {
		log.Error(err, "Error reconciling kubeconfig")
		return ctrl.Result{}, fmt.Errorf("error reconciling kubeconfig: %w", err)
	}

	if err := r.setControllerConditionsToNormal(ctx, log); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set conditions for kubeconfig controller: %w", err)
	}

	return res, nil
}

func (r *KubeconfigController) reconcileKubeconfig(ctx context.Context, log logr.Logger) (ctrl.Result, error) {
	// Get the token secret.
	tokenSecret := &corev1.Secret{}
	tokenSecretKey := client.ObjectKey{
		Name:      tokenSecretName,
		Namespace: r.ManagedNamespace,
	}

	if err := r.Get(ctx, tokenSecretKey, tokenSecret); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Waiting for token secret to be created")

			return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
		}

		return ctrl.Result{}, fmt.Errorf("unable to retrieve Secret object: %w", err)
	}

	if time.Since(tokenSecret.CreationTimestamp.Time) >= 30*time.Minute {
		log.Info("Token secret is older than 30 minutes. Recreating it...")

		// The token secret is managed by the CVO, it should be recreated shortly after deletion.
		if err := r.Delete(ctx, tokenSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to delete Secret object: %w", err)
		}

		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// Generate kubeconfig.
	kubeconfig, err := generateKubeconfig(kubeconfigOptions{
		token:            tokenSecret.Data["token"],
		caCert:           tokenSecret.Data["ca.crt"],
		apiServerEnpoint: r.RestCfg.Host,
		clusterName:      r.clusterName,
	})

	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error generating kubeconfig: %w", err)
	}

	// Create a secret with generated kubeconfig.
	out, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error writing kubeconfig: %w", err)
	}

	kubeconfigSecret := newKubeConfigSecret(r.clusterName, r.ManagedNamespace, out)
	kubeconfigSecretCopy := kubeconfigSecret.DeepCopy()

	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, kubeconfigSecret, func() error {
		kubeconfigSecret.ObjectMeta = kubeconfigSecretCopy.ObjectMeta
		kubeconfigSecret.Data = kubeconfigSecretCopy.Data
		kubeconfigSecret.Type = kubeconfigSecretCopy.Type

		return nil
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("error reconciling kubeconfig secret: %w", err)
	}

	return ctrl.Result{}, nil
}

func newKubeConfigSecret(clusterName, namespace string, data []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-kubeconfig", clusterName),
			Namespace: namespace,
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: clusterName,
			},
		},
		Data: map[string][]byte{
			"value": data,
		},
		Type: clusterv1.ClusterSecretType,
	}
}

// setControllerConditionsToNormal sets the KubeconfigController conditions to the normal state.
func (r *KubeconfigController) setControllerConditionsToNormal(ctx context.Context, log logr.Logger) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster operator: %w", err)
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.KubeconfigControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"Kubeconfig Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.KubeconfigControllerDegradedCondition, configv1.ConditionFalse, operatorstatus.ReasonAsExpected,
			"Kubeconfig Controller works as expected"),
	}

	log.V(2).Info("Kubeconfig Controller is Available")

	if err := r.SyncStatus(ctx, co, conds); err != nil {
		return fmt.Errorf("failed to sync cluster operator status: %w", err)
	}

	return nil
}

// setControllerConditionDegraded sets the KubeconfigController conditions to the normal state.
//
//nolint:unused
func (r *KubeconfigController) setControllerConditionDegraded(ctx context.Context, log logr.Logger, reconcileErr error) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster operator: %w", err)
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.KubeconfigControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"Kubeconfig Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.KubeconfigControllerDegradedCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			fmt.Sprintf("Kubeconfig Controller is degraded: %s", reconcileErr.Error())),
	}

	log.Info("Kubeconfig Controller is Degraded", reconcileErr.Error())

	if err := r.SyncStatus(ctx, co, conds); err != nil {
		return fmt.Errorf("failed to sync cluster operator status: %w", err)
	}

	return nil
}

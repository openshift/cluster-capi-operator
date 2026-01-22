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

	"errors"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
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

// KubeconfigReconciler reconciles a ClusterOperator object.
type KubeconfigReconciler struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme      *runtime.Scheme
	RestCfg     *rest.Config
	clusterName string
}

// SetupWithManager sets up the controller with the Manager.
func (r *KubeconfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(
			&corev1.Secret{},
			builder.WithPredicates(tokenSecretPredicate()),
		).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(toTokenSecret),
			builder.WithPredicates(kubeconfigSecretPredicate()),
		).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// Reconcile reconciles the kubeconfig secret.
func (r *KubeconfigReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)

	infra := &configv1.Infrastructure{}
	if err := r.Get(ctx, client.ObjectKey{Name: controllers.InfrastructureResourceName}, infra); err != nil {
		log.Error(err, "Unable to retrieve Infrastructure object")

		if err := r.SetStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %w", err)
		}

		return ctrl.Result{}, fmt.Errorf("unable to retrieve Infrastructure object: %w", err)
	}

	if infra.Status.PlatformStatus == nil {
		log.Info("No platform status exists in infrastructure object. Skipping kubeconfig reconciliation...")

		if err := r.SetStatusAvailable(ctx, ""); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %w", err)
		}

		return ctrl.Result{}, nil
	}

	r.clusterName = infra.Status.InfrastructureName

	log.Info("Reconciling kubeconfig secret")

	res, err := r.reconcileKubeconfig(ctx, log)
	if err != nil {
		log.Error(err, "Error reconciling kubeconfig")

		if err := r.SetStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %w", err)
		}

		return ctrl.Result{}, fmt.Errorf("error reconciling kubeconfig: %w", err)
	}

	if err := r.SetStatusAvailable(ctx, ""); err != nil {
		return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %w", err)
	}

	return res, nil
}

// reconcileKubeconfig reconciles the kubeconfig secret.
//
//nolint:funlen
func (r *KubeconfigReconciler) reconcileKubeconfig(ctx context.Context, log logr.Logger) (ctrl.Result, error) {
	// Get the token secret
	tokenSecret := &corev1.Secret{}
	tokenSecretKey := client.ObjectKey{
		Name:      tokenSecretName,
		Namespace: controllers.DefaultCAPINamespace,
	}

	if err := r.Get(ctx, tokenSecretKey, tokenSecret); err != nil {
		if kerrors.IsNotFound(err) {
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

	kubeconfigOptions := kubeconfigOptions{
		token:            tokenSecret.Data["token"],
		caCert:           tokenSecret.Data["ca.crt"],
		apiServerEnpoint: r.RestCfg.Host,
		clusterName:      r.clusterName,
	}

	if err := validateKubeconfigOptions(kubeconfigOptions); err != nil {
		if errors.Is(err, errTokenEmpty) || errors.Is(err, errCACertEmpty) {
			// If the validation fails with these errors it means
			// the token secret has not been populated by the kubernetes control plane yet.
			// Requeue to wait for the token secret to be populated.
			log.Info("Token secret has not been populated by the control plane yet, waiting..")
			return ctrl.Result{}, nil
		}

		// If the validation fails with other errors throw a reconciler error instead.
		return ctrl.Result{}, fmt.Errorf("invalid kubeconfig options: %w", err)
	}

	kubeconfig := generateKubeconfig(kubeconfigOptions)

	// Create a secret with generated kubeconfig
	out, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error writing kubeconfig: %w", err)
	}

	kubeconfigSecret := newKubeConfigSecret(r.clusterName, out)
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

func newKubeConfigSecret(clusterName string, data []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-kubeconfig", clusterName),
			Namespace: controllers.DefaultCAPINamespace,
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

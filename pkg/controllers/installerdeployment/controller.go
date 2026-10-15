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

package installerdeployment

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	fieldManager   = "capi-operator-installer-deployment"
	clusterAPIName = "cluster"
)

// InstallerDeploymentReconciler reconciles the capi-installer Deployment.
type InstallerDeploymentReconciler struct {
	client.Client
	Namespace      string
	ContainerImage string
}

// Reconcile reconciles the capi-installer Deployment by reading provider image refs
// from the ConfigMap and ClusterAPI revisions, then applying the desired deployment.
func (r *InstallerDeploymentReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName("InstallerDeploymentReconciler")

	// Read ConfigMap with current-release provider image refs.
	configMap, err := r.getConfigMap(ctx, log)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	configMapRefs, err := providerimages.ImageRefsFromConfigMap(configMap)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to extract image refs from ConfigMap: %w", err)
	}

	// Read ClusterAPI to get old revision image refs.
	clusterAPI, err := r.getClusterAPI(ctx, log)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get ClusterAPI: %w", err)
	}

	revisionRefs := providerimages.ImageRefsFromRevisions(clusterAPI.Status.Revisions)

	// Union all distinct image refs.
	allImageRefs := configMapRefs.Union(revisionRefs)

	// Build desired deployment.
	desired := buildDesiredDeployment(r.ContainerImage, r.Namespace, allImageRefs)

	// Apply deployment using Server-Side Apply.
	if err := r.applyDeployment(ctx, log, desired); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to apply Deployment: %w", err)
	}

	log.Info("Successfully reconciled capi-installer Deployment")

	return reconcile.Result{}, nil
}

// getConfigMap retrieves the capi-installer-images ConfigMap.
func (r *InstallerDeploymentReconciler) getConfigMap(ctx context.Context, log logr.Logger) (*corev1.ConfigMap, error) {
	configMap := &corev1.ConfigMap{}
	key := types.NamespacedName{
		Name:      providerimages.ConfigMapName,
		Namespace: r.Namespace,
	}

	if err := r.Get(ctx, key, configMap); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ConfigMap not found, using empty image refs", "name", key.Name)

			return &corev1.ConfigMap{Data: map[string]string{}}, nil
		}

		return nil, fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	return configMap, nil
}

// getClusterAPI retrieves the ClusterAPI singleton.
func (r *InstallerDeploymentReconciler) getClusterAPI(ctx context.Context, log logr.Logger) (*operatorv1alpha1.ClusterAPI, error) {
	clusterAPI := &operatorv1alpha1.ClusterAPI{}
	key := types.NamespacedName{
		Name: clusterAPIName,
	}

	if err := r.Get(ctx, key, clusterAPI); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ClusterAPI not found, using empty revisions")

			return &operatorv1alpha1.ClusterAPI{}, nil
		}

		return nil, fmt.Errorf("failed to get ClusterAPI: %w", err)
	}

	return clusterAPI, nil
}

// applyDeployment applies the Deployment using Server-Side Apply.
func (r *InstallerDeploymentReconciler) applyDeployment(ctx context.Context, log logr.Logger, desired *appsv1.Deployment) error {
	// Ensure TypeMeta is set for SSA
	desired.TypeMeta = metav1.TypeMeta{
		APIVersion: appsv1.SchemeGroupVersion.String(),
		Kind:       "Deployment",
	}

	if err := r.Patch(ctx, desired, client.Apply, &client.PatchOptions{
		FieldManager: fieldManager,
		Force:        ptr.To(true),
	}); err != nil {
		return fmt.Errorf("failed to patch Deployment: %w", err)
	}

	log.Info("Applied capi-installer Deployment", "name", desired.Name)

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *InstallerDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == r.Namespace && obj.GetName() == deploymentName
		}))).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.mapConfigMapToReconcile)).
		Watches(&operatorv1alpha1.ClusterAPI{}, handler.EnqueueRequestsFromMapFunc(r.mapClusterAPIToReconcile)).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// mapConfigMapToReconcile maps ConfigMap events to reconcile requests.
func (r *InstallerDeploymentReconciler) mapConfigMapToReconcile(ctx context.Context, obj client.Object) []reconcile.Request {
	if obj.GetName() == providerimages.ConfigMapName && obj.GetNamespace() == r.Namespace {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{
			Name:      deploymentName,
			Namespace: r.Namespace,
		}}}
	}

	return nil
}

// mapClusterAPIToReconcile maps ClusterAPI events to reconcile requests.
func (r *InstallerDeploymentReconciler) mapClusterAPIToReconcile(ctx context.Context, obj client.Object) []reconcile.Request {
	if obj.GetName() == clusterAPIName {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{
			Name:      deploymentName,
			Namespace: r.Namespace,
		}}}
	}

	return nil
}

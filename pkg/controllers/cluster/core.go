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
package cluster

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

// CoreClusterReconciler reconciles a Cluster object.
type CoreClusterReconciler struct {
	operatorstatus.ClusterOperatorStatusClient
	Cluster *clusterv1.Cluster
}

// SetupWithManager sets the CoreClusterReconciler controller up with the given manager.
func (r *CoreClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		For(r.Cluster).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// Reconcile reconciles the core cluster object for the openshift-cluster-api namespace.
func (r *CoreClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName("CoreClusterController")

	cluster := &clusterv1.Cluster{}

	if err := r.Client.Get(ctx, req.NamespacedName, cluster); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get core cluster: %w", err)
	}

	if !cluster.DeletionTimestamp.IsZero() {
		if err := r.SetStatusAvailable(ctx, ""); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set status available: %w", err)
		}

		return ctrl.Result{}, nil
	}

	log.Info("Reconciling core cluster")

	clusterCopy := cluster.DeepCopy()

	conditions.MarkTrue(cluster, clusterv1.ControlPlaneInitializedCondition)

	patch := client.MergeFrom(clusterCopy)

	isRequired, err := util.IsPatchRequired(cluster, patch)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check if patch required: %w", err)
	}

	if isRequired {
		if err := r.Status().Patch(ctx, cluster, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to update core cluster status: %w", err)
		}
	}

	if err := r.SetStatusAvailable(ctx, ""); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set status available: %w", err)
	}

	return ctrl.Result{}, nil
}

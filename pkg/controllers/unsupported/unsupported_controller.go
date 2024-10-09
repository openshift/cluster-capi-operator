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

package unsupported

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	capiUnsupportedPlatformMsg = "Cluster API is not yet implemented on this platform"
	controllerName             = "UnsupportedController"
)

// UnsupportedController on unsupported platforms watches and keeps the cluster-api ClusterObject up to date.
type UnsupportedController struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme *runtime.Scheme
}

// Reconcile reconciles the cluster-api ClusterOperator object.
func (r *UnsupportedController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)
	log.Info(fmt.Sprintf("Reconciling %q ClusterObject", controllers.ClusterOperatorName))

	if err := r.ClusterOperatorStatusClient.SetStatusAvailable(ctx, capiUnsupportedPlatformMsg); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set conditions for %q ClusterObject: %w", controllers.ClusterOperatorName, err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UnsupportedController) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(clusterOperatorPredicates())).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

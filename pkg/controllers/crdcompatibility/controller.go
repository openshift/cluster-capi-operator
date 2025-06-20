/*
Copyright 2024 The OpenShift Authors.

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

package crdcompatibility

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
)

// CRDCompatibilityReconciler reconciles CRDCompatibilityRequirement resources
type CRDCompatibilityReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements/finalizers,verbs=update

// Reconcile handles the reconciliation of CRDCompatibilityRequirement resources
func (r *CRDCompatibilityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the CRDCompatibilityRequirement instance
	crdCompatibility := &operatorv1alpha1.CRDCompatibilityRequirement{}
	if err := r.Get(ctx, req.NamespacedName, crdCompatibility); err != nil {
		// Handle the case where the resource is not found
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("Reconciling CRDCompatibilityRequirement",
		"name", crdCompatibility.Name,
		"namespace", crdCompatibility.Namespace)

	// TODO: Implement reconciliation logic
	// - Validate CRDCompatibilityRequirement spec
	// - Check if required CRDs exist
	// - Update status based on compatibility requirements
	// - Handle any errors and update conditions

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *CRDCompatibilityReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorv1alpha1.CRDCompatibilityRequirement{}).
		Complete(r)
}

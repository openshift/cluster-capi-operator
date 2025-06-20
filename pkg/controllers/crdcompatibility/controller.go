/*
Copyright 2025 The OpenShift Authors.

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
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

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
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch

// Reconcile handles the reconciliation of CRDCompatibilityRequirement resources
func (r *CRDCompatibilityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the CRDCompatibilityRequirement instance
	crdCompatibility := &operatorv1alpha1.CRDCompatibilityRequirement{}
	if err := r.Get(ctx, req.NamespacedName, crdCompatibility); err != nil {
		logger.V(4).Info("Observed CRDCompatibilityRequirement deleted")

		// Handle the case where the resource is not found
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	logger = logger.WithValues("crd", crdCompatibility.Spec.CRDRef)
	ctx = ctrl.LoggerInto(ctx, logger)

	if !crdCompatibility.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, crdCompatibility)
	}

	return r.reconcileCreateOrUpdate(ctx, crdCompatibility)
}

func (r *CRDCompatibilityReconciler) reconcileCreateOrUpdate(ctx context.Context, crdCompatibility *operatorv1alpha1.CRDCompatibilityRequirement) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Reconciling CRDCompatibilityRequirement")

	// Parse the CRD in compatibilityCRD into a CRD object
	compatibilityCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal([]byte(crdCompatibility.Spec.CompatibilityCRD), compatibilityCRD); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse CRD for %s: %w", crdCompatibility.Spec.CRDRef, err)
	}

	// Fetch the CRD referenced by crdRef
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := r.Get(ctx, types.NamespacedName{Name: crdCompatibility.Spec.CRDRef}, crd); err != nil {
		logger.Error(err, "failed to fetch CRD", "crdRef", crdCompatibility.Spec.CRDRef)
		return ctrl.Result{}, err
	}

	// TODO: Implement reconciliation logic
	// - Validate CRDCompatibilityRequirement spec
	// - Check if required CRDs exist
	// - Update status based on compatibility requirements
	// - Handle any errors and update conditions

	return ctrl.Result{}, nil
}

func (r *CRDCompatibilityReconciler) reconcileDelete(ctx context.Context, crdCompatibility *operatorv1alpha1.CRDCompatibilityRequirement) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Reconciling CRDCompatibilityRequirement deletion")

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *CRDCompatibilityReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Create field index for spec.crdRef
	if err := mgr.GetFieldIndexer().IndexField(ctx, &operatorv1alpha1.CRDCompatibilityRequirement{}, "spec.crdRef", func(obj client.Object) []string {
		requirement := obj.(*operatorv1alpha1.CRDCompatibilityRequirement)
		return []string{requirement.Spec.CRDRef}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorv1alpha1.CRDCompatibilityRequirement{}).
		Watches(
			&apiextensionsv1.CustomResourceDefinition{},
			handler.EnqueueRequestsFromMapFunc(r.findCRDCompatibilityRequirementsForCRD),
		).
		Complete(r)
}

// findCRDCompatibilityRequirementsForCRD finds all CRDCompatibilityRequirements that reference the given CRD
func (r *CRDCompatibilityReconciler) findCRDCompatibilityRequirementsForCRD(ctx context.Context, obj client.Object) []reconcile.Request {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil
	}

	// Use field index to find CRDCompatibilityRequirements that reference this CRD
	var requirements operatorv1alpha1.CRDCompatibilityRequirementList
	if err := r.List(ctx, &requirements, client.MatchingFields{"spec.crdRef": crd.Name}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list CRDCompatibilityRequirements for CRD", "crdName", crd.Name)
		return nil
	}

	var requests []reconcile.Request
	for _, requirement := range requirements.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: requirement.Name,
			},
		})
	}

	return requests
}

/*
Copyright 2025 Red Hat, Inc.

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
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
)

const (
	controllerName string = "CRDCompatibilityController"
)

//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements/finalizers,verbs=update
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch

func NewCRDCompatibilityReconciler(client client.Client) *CRDCompatibilityReconciler {
	return &CRDCompatibilityReconciler{
		client: client,
	}
}

// CRDCompatibilityReconciler reconciles CRDCompatibilityRequirement resources
type CRDCompatibilityReconciler struct {
	client client.Client
}

const (
	fieldIndexCRDRef = "spec.crdRef"
)

// SetupWithManager sets up the controller with the Manager
func (r *CRDCompatibilityReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Create field index for spec.crdRef
	if err := mgr.GetFieldIndexer().IndexField(ctx, &operatorv1alpha1.CRDCompatibilityRequirement{}, fieldIndexCRDRef, func(obj client.Object) []string {
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
	if err := r.client.List(ctx, &requirements, client.MatchingFields{fieldIndexCRDRef: crd.Name}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list CRDCompatibilityRequirements for CRD", "crdName", crd.Name, "clientType", fmt.Sprintf("%T", r.client))
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

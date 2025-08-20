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
	"errors"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
)

const (
	fieldIndexCRDRef string = "spec.crdRef"

	controllerName string = "crdcompatibility.operator.openshift.io"
)

//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=operator.openshift.io,resources=crdcompatibilityrequirements/finalizers,verbs=update
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch

// NewCRDCompatibilityReconciler returns a partially initialised CRDCompatibilityReconciler.
func NewCRDCompatibilityReconciler(client client.Client) *CRDCompatibilityReconciler {
	return &CRDCompatibilityReconciler{
		client: client,
	}
}

// CRDCompatibilityReconciler reconciles CRDCompatibilityRequirement resources.
type CRDCompatibilityReconciler struct {
	client client.Client

	validator *crdValidator
}

// SetupWithManager sets up the controller with the Manager.
func (r *CRDCompatibilityReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Create field index for spec.crdRef
	if err := mgr.GetFieldIndexer().IndexField(ctx, &operatorv1alpha1.CRDCompatibilityRequirement{}, fieldIndexCRDRef, func(obj client.Object) []string {
		requirement, ok := obj.(*operatorv1alpha1.CRDCompatibilityRequirement)
		if !ok {
			log.FromContext(ctx).Error(errExpectedCRD, "expected a CRDCompatibilityRequirement", "receivedType", fmt.Sprintf("%T", obj))
			return nil
		}

		return []string{requirement.Spec.CRDRef}
	}); err != nil {
		return fmt.Errorf("failed to add index to CRDCompatibilityRequirements: %w", err)
	}

	// TODO: For safety we need to ensure that we have reconciled every
	// CRDCompatibilityRequirement at least once before we the webhook is
	// registered. The webhook will allow any change for a CRD if it has not
	// seen a CRDCompatibilityRequirement for it. This would be a race
	// immedately after failover.

	crdValidator := &crdValidator{
		client: mgr.GetClient(),
	}
	r.validator = crdValidator

	return errors.Join(
		ctrl.NewWebhookManagedBy(mgr).
			For(&apiextensionsv1.CustomResourceDefinition{}).
			WithValidator(crdValidator).
			Complete(),

		ctrl.NewWebhookManagedBy(mgr).
			For(&operatorv1alpha1.CRDCompatibilityRequirement{}).
			WithValidator(&crdRequirementValidator{}).
			Complete(),

		ctrl.NewControllerManagedBy(mgr).
			// We don't need to reconcile deletion because we use a finalizer
			For(&operatorv1alpha1.CRDCompatibilityRequirement{}, builder.WithPredicates(predicate.Funcs{
				CreateFunc:  func(e event.CreateEvent) bool { return true },
				UpdateFunc:  func(e event.UpdateEvent) bool { return true },
				GenericFunc: func(e event.GenericEvent) bool { return true },
			})).
			Watches(
				&apiextensionsv1.CustomResourceDefinition{},
				handler.EnqueueRequestsFromMapFunc(r.findCRDCompatibilityRequirementsForCRD),
			).
			Complete(r),
	)
}

// findCRDCompatibilityRequirementsForCRD finds all CRDCompatibilityRequirements that reference the given CRD.
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

	requests := make([]reconcile.Request, len(requirements.Items))
	for i := range requirements.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: requirements.Items[i].Name,
			},
		}
	}

	return requests
}

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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/index"
)

const (
	controllerName string = "compatibilityrequirement.operator.openshift.io"
)

//+kubebuilder:rbac:groups=apiextensions.openshift.io,resources=compatibilityrequirements,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apiextensions.openshift.io,resources=compatibilityrequirements/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apiextensions.openshift.io,resources=compatibilityrequirements/finalizers,verbs=update
//+kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch

// NewCompatibilityRequirementReconciler returns a partially initialised CompatibilityRequirementReconciler.
func NewCompatibilityRequirementReconciler(client client.Client) *CompatibilityRequirementReconciler {
	return &CompatibilityRequirementReconciler{
		client: client,
	}
}

// CompatibilityRequirementReconciler reconciles CompatibilityRequirement resources.
type CompatibilityRequirementReconciler struct {
	client client.Client
}

type controllerOption func(*builder.Builder) *builder.Builder

// SetupWithManager sets up the controller with the Manager.
func (r *CompatibilityRequirementReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, opts ...controllerOption) error {
	crdRequirementValidatorBuilder := ctrl.NewWebhookManagedBy(mgr).
		For(&apiextensionsv1alpha1.CompatibilityRequirement{}).
		WithValidator(&crdRequirementValidator{})

	controllerBuilder := ctrl.NewControllerManagedBy(mgr).
		// We don't need to reconcile deletion because we use a finalizer.
		// When a user DELETEs the CompatibilityRequirement, the informer receives an Update event to add the DeletionTimestamp.
		// The informer delete event means the object is removed from the informer cache and we have nothing to do in that case.
		For(&apiextensionsv1alpha1.CompatibilityRequirement{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc:  func(e event.CreateEvent) bool { return true },
			UpdateFunc:  func(e event.UpdateEvent) bool { return true },
			GenericFunc: func(e event.GenericEvent) bool { return true },
		})).
		Watches(
			&apiextensionsv1.CustomResourceDefinition{},
			handler.EnqueueRequestsFromMapFunc(r.findCompatibilityRequirementsForCRD),
		)

	for _, opt := range opts {
		controllerBuilder = opt(controllerBuilder)
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &apiextensionsv1alpha1.CompatibilityRequirement{}, index.FieldCRDByName, index.CRDByName); err != nil {
		return fmt.Errorf("failed to add index to CompatibilityRequirements: %w", err)
	}

	return errors.Join(
		crdRequirementValidatorBuilder.Complete(),
		controllerBuilder.Complete(r),
	)
}

// findCompatibilityRequirementsForCRD finds all CompatibilityRequirements that reference the given CRD.
func (r *CompatibilityRequirementReconciler) findCompatibilityRequirementsForCRD(ctx context.Context, obj client.Object) []reconcile.Request {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil
	}

	// Use field index to find CompatibilityRequirements that reference this CRD
	var requirements apiextensionsv1alpha1.CompatibilityRequirementList
	if err := r.client.List(ctx, &requirements, client.MatchingFields{index.FieldCRDByName: crd.Name}); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list CompatibilityRequirements for CRD", "crdName", crd.Name, "clientType", fmt.Sprintf("%T", r.client))
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

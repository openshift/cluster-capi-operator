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
package upgradeable

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	assessment "github.com/openshift/cluster-capi-operator/pkg/upgradeable"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

const (
	// clusterVersionName is the name of the cluster global ClusterVersion resource.
	clusterVersionName = "version"
	clusterAPIName     = "cluster"

	controllerName = "UpgradeableController"

	// ResultGenerator is the controller result generator for the UpgradeableController.
	ResultGenerator = operatorstatus.ControllerResultGenerator(controllerName)
)

// UpgradeableController assesses whether the cluster is compatible with the
// Cluster API components of the next minor release, and reports the result in
// the Upgradeable condition of the cluster-api ClusterOperator.
type UpgradeableController struct {
	client.Client
	ContainerImage string
	apiReader      client.Reader
}

// Reconcile reports the controller status on the cluster-api ClusterOperator.
func (r *UpgradeableController) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)
	log.Info("Reconciling upgradeability")

	reconcileResult := r.reconcile(ctx)

	// Publish every outcome, including waiting and errors, so a verdict from a
	// previous assessment cannot remain visible while the next one is incomplete.
	if err := reconcileResult.WriteClusterOperatorStatus(ctx, log, r.Client); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to write conditions: %w", err)
	}

	return reconcileResult.Result()
}

// SetupWithManager sets up the controller with the Manager.
// The controller has no primary object and its Reconcile ignores the
// request, but all watches must map to the same target so that their events
// coalesce in the single workqueue.
func (r *UpgradeableController) SetupWithManager(mgr ctrl.Manager) error {
	r.apiReader = mgr.GetAPIReader()
	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		// Notify on creation of the ClusterOperator
		Watches(
			&configv1.ClusterOperator{},
			handler.EnqueueRequestsFromMapFunc(operatorstatus.ToClusterOperator),
			builder.WithPredicates(operatorstatus.ClusterOperatorOnceOnly()),
		).
		// Notify on changes to availableUpdates or conditionalUpdates
		Watches(
			&configv1.ClusterVersion{},
			handler.EnqueueRequestsFromMapFunc(operatorstatus.ToClusterOperator),
			builder.WithPredicates(clusterVersionUpdatesChanged()),
		).
		// Notify on changes to the ClusterAPI configuration.
		Watches(
			&operatorv1alpha1.ClusterAPI{},
			handler.EnqueueRequestsFromMapFunc(operatorstatus.ToClusterOperator),
			builder.WithPredicates(
				predicate.NewPredicateFuncs(func(obj client.Object) bool { return obj.GetName() == clusterAPIName }),
				predicate.GenerationChangedPredicate{},
			),
		).
		// Ownership changes can affect upgradeability without changing schemas
		// or producing a new compatibility verdict.
		Watches(&apiextensionsv1.CustomResourceDefinition{}, handler.EnqueueRequestsFromMapFunc(operatorstatus.ToClusterOperator), builder.WithPredicates(predicate.LabelChangedPredicate{})).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(operatorstatus.ToClusterOperator), builder.WithPredicates(managedAssessment())).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(operatorstatus.ToClusterOperator), builder.WithPredicates(managedAssessment())).
		// CRD remediation changes requirement status without changing the target
		// version. Do not filter these events to generation changes alone.
		Watches(&openshiftapiextensionsv1.CompatibilityRequirement{}, handler.EnqueueRequestsFromMapFunc(operatorstatus.ToClusterOperator), builder.WithPredicates(managedAssessment())).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// clusterVersionUpdatesChanged returns a predicate that triggers on create
// events for the version ClusterVersion, and on update events when
// status.desired.version, status.availableUpdates or status.conditionalUpdates have changed.
func clusterVersionUpdatesChanged() predicate.Funcs {
	isClusterVersion := func(obj runtime.Object) bool {
		clusterVersion, ok := obj.(*configv1.ClusterVersion)
		return ok && clusterVersion.GetName() == clusterVersionName
	}

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return isClusterVersion(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !isClusterVersion(e.ObjectNew) {
				return false
			}

			oldCV, _ := e.ObjectOld.(*configv1.ClusterVersion)
			newCV, _ := e.ObjectNew.(*configv1.ClusterVersion)

			return oldCV.Status.Desired.Version != newCV.Status.Desired.Version ||
				!equality.Semantic.DeepEqual(oldCV.Status.AvailableUpdates, newCV.Status.AvailableUpdates) ||
				!equality.Semantic.DeepEqual(oldCV.Status.ConditionalUpdates, newCV.Status.ConditionalUpdates)
		},
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

func managedAssessment() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetLabels()[assessment.ManagedLabel] == assessment.ManagedLabelValue
	})
}

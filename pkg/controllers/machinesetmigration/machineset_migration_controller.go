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

package machinesetmigration

import (
	"context"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/migrationcommon"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const controllerName = "MachineSetMigrationController"

// MachineSetMigrationReconciler reconciles MachineSet resources for migration.
type MachineSetMigrationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	CAPINamespace string
	MAPINamespace string
}

type machineSetMigratable struct {
	reconciler     *MachineSetMigrationReconciler
	mapiMachineSet *mapiv1beta1.MachineSet
}

// MAPIObject returns the backing Machine API machine set.
func (m *machineSetMigratable) MAPIObject() client.Object {
	return m.mapiMachineSet
}

// DesiredAuthority returns the requested authoritative API from spec.
func (m *machineSetMigratable) DesiredAuthority() mapiv1beta1.MachineAuthority {
	return m.mapiMachineSet.Spec.AuthoritativeAPI
}

// CurrentAuthority returns the observed authoritative API from status.
func (m *machineSetMigratable) CurrentAuthority() mapiv1beta1.MachineAuthority {
	return m.mapiMachineSet.Status.AuthoritativeAPI
}

// SynchronizedAPI returns the last synchronized API recorded in status.
func (m *machineSetMigratable) SynchronizedAPI() mapiv1beta1.SynchronizedAPI {
	return m.mapiMachineSet.Status.SynchronizedAPI
}

// SynchronizedGeneration returns the generation recorded by the sync controller.
func (m *machineSetMigratable) SynchronizedGeneration() int64 {
	return m.mapiMachineSet.Status.SynchronizedGeneration
}

// MAPIConditions returns the Machine API conditions used by migration logic.
func (m *machineSetMigratable) MAPIConditions() []mapiv1beta1.Condition {
	return m.mapiMachineSet.Status.Conditions
}

// EnsureCAPIPaused pauses the primary Cluster API machine set.
func (m *machineSetMigratable) EnsureCAPIPaused(ctx context.Context, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	return m.reconciler.ensureCAPIPaused(ctx, capiMachineSet)
}

// EnsureCAPIUnpaused removes pause from the primary Cluster API machine set.
func (m *machineSetMigratable) EnsureCAPIUnpaused(ctx context.Context, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	return m.reconciler.ensureCAPIUnpaused(ctx, capiMachineSet)
}

// SetupWithManager sets up the MachineSetMigration controller.
func (r *MachineSetMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Allow the namespaces to be set externally for test purposes, when not set,
	// default to the production namespaces.
	if r.CAPINamespace == "" {
		r.CAPINamespace = controllers.DefaultCAPINamespace
	}

	if r.MAPINamespace == "" {
		r.MAPINamespace = controllers.DefaultMAPINamespace
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&mapiv1beta1.MachineSet{}, builder.WithPredicates(util.FilterNamespace(r.MAPINamespace))).
		Watches(
			&clusterv1.MachineSet{},
			handler.EnqueueRequestsFromMapFunc(util.RewriteNamespace(r.MAPINamespace)),
			builder.WithPredicates(util.FilterNamespace(r.CAPINamespace)),
		).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	r.Client = mgr.GetClient()
	r.Scheme = mgr.GetScheme()
	r.Recorder = mgr.GetEventRecorderFor(controllerName)

	return nil
}

// Reconcile performs the reconciliation for a MachineSet.
func (r *MachineSetMigrationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	logger.V(1).Info("Reconciling machine set")
	defer logger.V(1).Info("Finished reconciling machine set")

	mapiMachineSet, found, err := r.getMAPIMachineSet(ctx, req)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !found {
		logger.Info("MachineSet has been deleted. Migration not required")
		return ctrl.Result{}, nil
	}

	result, err := migrationcommon.Reconcile(
		ctx,
		r.Client,
		controllerName,
		r.CAPINamespace,
		machinev1applyconfigs.MachineSet,
		&machineSetMigratable{
			reconciler:     r,
			mapiMachineSet: mapiMachineSet,
		},
	)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile machine set migration state: %w", err)
	}

	return result, nil
}

func (r *MachineSetMigrationReconciler) getMAPIMachineSet(ctx context.Context, req reconcile.Request) (*mapiv1beta1.MachineSet, bool, error) {
	mapiMachineSet := &mapiv1beta1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: req.Name}, mapiMachineSet); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("failed to get MAPI machine set: %w", err)
	}

	return mapiMachineSet, true, nil
}

func (r *MachineSetMigrationReconciler) ensureCAPIPaused(ctx context.Context, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	return r.ensureCAPIMachineSetPaused(ctx, capiMachineSet)
}

func (r *MachineSetMigrationReconciler) ensureCAPIMachineSetPaused(ctx context.Context, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	changed, err := migrationcommon.AddPausedAnnotation(ctx, r.Client, capiMachineSet)
	if err != nil {
		return false, fmt.Errorf("failed to request pause on Cluster API machine set: %w", err)
	}

	if changed {
		return false, nil
	}

	// If the finalizer is present we know that the controller is running. It will observe the paused annotation and will eventually set the paused condition. We must wait for the paused condition because it may be actively reconciling.
	// If the finalizer is not present then either:
	// The controller is not running, in which it is safe to continue.
	// The controller has not yet observed the object, in which case (guaranteed by optimistic locking) it will observe our paused annotation before taking any action, so it is safe to continue.
	if !slices.Contains(capiMachineSet.Finalizers, clusterv1.MachineSetFinalizer) {
		return true, nil
	}

	machineSetPausedCondition := conditions.Get(capiMachineSet, clusterv1.PausedCondition)
	if machineSetPausedCondition == nil {
		return false, nil
	}

	return machineSetPausedCondition.Status == metav1.ConditionTrue, nil
}

func (r *MachineSetMigrationReconciler) ensureCAPIUnpaused(ctx context.Context, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	changed, err := migrationcommon.RemovePausedAnnotation(ctx, r.Client, capiMachineSet)
	if err != nil {
		return false, fmt.Errorf("failed to remove paused annotation from Cluster API machine set: %w", err)
	}

	if changed {
		return false, nil
	}

	machineSetPausedCondition := conditions.Get(capiMachineSet, clusterv1.PausedCondition)
	if machineSetPausedCondition != nil && machineSetPausedCondition.Status == metav1.ConditionTrue {
		return false, nil
	}

	return true, nil
}

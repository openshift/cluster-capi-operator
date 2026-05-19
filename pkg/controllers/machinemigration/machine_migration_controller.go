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

package machinemigration

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/controllers/external"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/synccommon"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const controllerName = "MachineMigrationController"

// MachineMigrationReconciler reconciles Machine resources for migration.
type MachineMigrationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Infra         *configv1.Infrastructure
	Platform      configv1.PlatformType
	InfraTypes    util.InfraTypes
	CAPINamespace string
	MAPINamespace string
}

// SetupWithManager sets up the MachineMigration controller.
func (r *MachineMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
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
		For(&mapiv1beta1.Machine{}, builder.WithPredicates(util.FilterNamespace(r.MAPINamespace))).
		Watches(
			&clusterv1.Machine{},
			handler.EnqueueRequestsFromMapFunc(util.RewriteNamespace(r.MAPINamespace)),
			builder.WithPredicates(util.FilterNamespace(r.CAPINamespace)),
		).
		Watches(
			r.InfraTypes.Machine(),
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

// Reconcile performs the reconciliation for a Machine.
//
//nolint:funlen,cyclop
func (r *MachineMigrationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	// Keep the initial status.AuthoritativeAPI defaulting aligned with the
	// Machine API controllers so migration still works before this controller runs.
	logger.V(1).Info("Reconciling machine")
	defer logger.V(1).Info("Finished reconciling machine")

	mapiMachine, found, err := r.getMAPIMachine(ctx, req)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !found {
		logger.Info("Machine has been deleted. Migration not required")
		return ctrl.Result{}, nil
	}

	if mapiMachine.Spec.AuthoritativeAPI == mapiMachine.Status.AuthoritativeAPI {
		// No migration is being requested for this resource, nothing to do.
		return ctrl.Result{}, nil
	}

	// Initialize status.authoritativeAPI on the first reconciliation.
	if mapiMachine.Status.AuthoritativeAPI == "" {
		if err := r.applyMigrationStatusWithPatch(ctx, mapiMachine, mapiMachine.Spec.AuthoritativeAPI); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
		}

		return ctrl.Result{}, nil
	}

	currentAuthority, desiredAuthority, isMigrating, err := synccommon.MigrationDirection(
		mapiMachine.Status.AuthoritativeAPI,
		mapiMachine.Status.SynchronizedAPI,
		mapiMachine.Spec.AuthoritativeAPI,
	)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to determine current authority while migrating: %w", err)
	}

	// If the resource is already Migrating and spec switches back to the source
	// authority, treat it as a cancellation request.
	if result, handled, err := r.handleMigrationCancellation(ctx, logger, mapiMachine, currentAuthority, desiredAuthority, isMigrating); err != nil {
		return result, err
	} else if handled {
		return result, nil
	}

	// Check that the resource is synchronized and up-to-date.
	//
	// This MUST be checked BEFORE setting status.authoritativeAPI to Migrating,
	// because after that the sync controller will not run to update it and we
	// will deadlock.
	if isSynchronized, err := r.isSynchronized(ctx, mapiMachine); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to check the resource is synchronized and up-to-date with its authority: %w", err)
	} else if !isSynchronized {
		// The to-be Authoritative API resource is not fully synced up yet, requeue to check later.
		logger.Info("Authoritative machine and its copy are not synchronized yet, will retry later")

		return ctrl.Result{}, nil
	}

	// Make sure the authoritativeAPI resource status is set to migrating.
	if !isMigrating {
		logger.Info("Detected migration request for machine")

		if err := r.applyMigrationStatusWithPatch(ctx, mapiMachine, mapiv1beta1.MachineAuthorityMigrating); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to set authoritativeAPI to Migrating: %w", err)
		}

		logger.Info("Acknowledged migration request for machine")

		// Wait for the change to propagate.
		return ctrl.Result{}, nil
	}

	// Request pausing on the authoritative resource.
	if updated, err := r.requestOldAuthoritativeResourcePaused(ctx, mapiMachine, currentAuthority); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to request pause on authoritative machine: %w", err)
	} else if updated {
		logger.Info("Requested pausing for authoritative machine")

		// Wait for the change to propagate.
		// Since there is not a watch for CAPI infra machines here
		// we will not get an event for the CAPI infra machine's paused condition change,
		// as such, manually requeue.
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Check that the authoritative resource is paused.
	if paused, err := r.isOldAuthoritativeResourcePaused(ctx, mapiMachine, currentAuthority); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check paused on authoritative machine: %w", err)
	} else if !paused {
		// The Authoritative API resource is not paused yet, requeue to check later.
		logger.Info("Authoritative machine is not paused yet, will retry later")

		return ctrl.Result{}, nil
	}

	// Only Cluster API exposes a target unpause signal before completing migration.
	// Machine API only clears Paused after status.AuthoritativeAPI leaves Migrating.
	if desiredAuthority == mapiv1beta1.MachineAuthorityClusterAPI {
		if unpaused, err := r.ensureClusterAPITargetUnpaused(ctx, mapiMachine); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to ensure the Cluster API target is unpaused: %w", err)
		} else if !unpaused {
			logger.Info("New authoritative machine is not unpaused yet, waiting for target controller")

			return ctrl.Result{}, nil
		}
	}

	// Set the actual AuthoritativeAPI to the desired one and reset the synchronized generation and condition.
	// SynchronizedAPI will be updated by the sync controller after resync.
	if err := r.applyMigrationStatusAndResetSyncStatusWithPatch(ctx, mapiMachine, mapiMachine.Spec.AuthoritativeAPI); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply authoritativeAPI and reset sync status: %w", err)
	}

	logger.Info("Machine authority switch has now been completed", "authoritativeAPI", mapiMachine.Spec.AuthoritativeAPI)
	logger.Info("Machine migrated successfully")

	return ctrl.Result{}, nil
}

func (r *MachineMigrationReconciler) getMAPIMachine(ctx context.Context, req reconcile.Request) (*mapiv1beta1.Machine, bool, error) {
	mapiMachine := &mapiv1beta1.Machine{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: req.Name}, mapiMachine); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("failed to get MAPI machine: %w", err)
	}

	return mapiMachine, true, nil
}

func (r *MachineMigrationReconciler) handleMigrationCancellation(
	ctx context.Context,
	logger logr.Logger,
	mapiMachine *mapiv1beta1.Machine,
	currentAuthority, desiredAuthority mapiv1beta1.MachineAuthority,
	isMigrating bool,
) (ctrl.Result, bool, error) {
	if !isMigrating || currentAuthority == "" || desiredAuthority != currentAuthority {
		return ctrl.Result{}, false, nil
	}

	logger.Info("Migration cancellation detected, rolling back to source authority",
		"sourceAuthority", currentAuthority)

	switch currentAuthority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		readyToRollback, result, err := r.ensureRollbackToMachineAPIReady(ctx, logger, mapiMachine)
		if err != nil {
			return ctrl.Result{}, true, err
		}

		if !readyToRollback {
			return result, true, nil
		}
	case mapiv1beta1.MachineAuthorityClusterAPI:
		readyToRollback, result, err := r.ensureRollbackToClusterAPIReady(ctx, logger, mapiMachine)
		if err != nil {
			return ctrl.Result{}, true, err
		}

		if !readyToRollback {
			return result, true, nil
		}
	case mapiv1beta1.MachineAuthorityMigrating:
		return ctrl.Result{}, true, fmt.Errorf("%w", synccommon.UnexpectedCurrentAuthorityDuringCancellationError(currentAuthority))
	default:
		return ctrl.Result{}, true, fmt.Errorf("%w", synccommon.UnexpectedCurrentAuthorityDuringCancellationError(currentAuthority))
	}

	// Reset status back to source authority and reset sync status.
	if err := r.applyMigrationStatusAndResetSyncStatusWithPatch(ctx, mapiMachine, currentAuthority); err != nil {
		return ctrl.Result{}, true, fmt.Errorf("failed to rollback migration: %w", err)
	}

	logger.Info("Migration cancelled and rolled back successfully")

	return ctrl.Result{}, true, nil
}

func (r *MachineMigrationReconciler) ensureRollbackToMachineAPIReady(ctx context.Context, logger logr.Logger, mapiMachine *mapiv1beta1.Machine) (bool, ctrl.Result, error) {
	rollbackObserved, err := r.isClusterAPIResourcesInRollbackState(ctx, mapiMachine, true)
	if err != nil {
		return false, ctrl.Result{}, fmt.Errorf("failed to observe re-paused Cluster API resources after cancellation: %w", err)
	}

	if rollbackObserved {
		return true, ctrl.Result{}, nil
	}

	// Rolling back to Machine API means Cluster API was the target. Re-pause it
	// so the cluster returns to a single authoritative API before status is reset.
	if _, err := r.requestClusterAPIResourcesPausedForRollback(ctx, mapiMachine); err != nil {
		return false, ctrl.Result{}, fmt.Errorf("failed to re-pause Cluster API resources after cancellation: %w", err)
	}

	logger.Info("Waiting for Cluster API rollback target to pause before resetting migration status")

	return false, ctrl.Result{RequeueAfter: time.Second}, nil
}

func (r *MachineMigrationReconciler) ensureRollbackToClusterAPIReady(ctx context.Context, logger logr.Logger, mapiMachine *mapiv1beta1.Machine) (bool, ctrl.Result, error) {
	rollbackObserved, err := r.isClusterAPIResourcesInRollbackState(ctx, mapiMachine, false)
	if err != nil {
		return false, ctrl.Result{}, fmt.Errorf("failed to observe unpaused Cluster API resources after cancellation: %w", err)
	}

	if rollbackObserved {
		return true, ctrl.Result{}, nil
	}

	// Rolling back to Cluster API means Cluster API was the source and may have
	// been paused during the migration attempt. Unpause it before resetting status.
	if err := r.ensureUnpauseAfterCancellation(ctx, mapiMachine); err != nil {
		return false, ctrl.Result{}, fmt.Errorf("failed to unpause after cancellation: %w", err)
	}

	logger.Info("Waiting for Cluster API rollback source to unpause before resetting migration status")

	return false, ctrl.Result{RequeueAfter: time.Second}, nil
}

func (r *MachineMigrationReconciler) isClusterAPIResourcesInRollbackState(ctx context.Context, mapiMachine *mapiv1beta1.Machine, wantPaused bool) (bool, error) {
	capiMachine := &clusterv1.Machine{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: mapiMachine.Name}, capiMachine); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}

		return false, fmt.Errorf("failed to get Cluster API machine: %w", err)
	}

	if annotations.HasPaused(capiMachine) != wantPaused {
		return false, nil
	}

	machinePausedCondition := conditions.Get(capiMachine, clusterv1.PausedCondition)
	if wantPaused {
		if machinePausedCondition == nil || machinePausedCondition.Status != metav1.ConditionTrue {
			return false, nil
		}
	} else if machinePausedCondition != nil && machinePausedCondition.Status == metav1.ConditionTrue {
		return false, nil
	}

	infraMachineRef := capiMachine.Spec.InfrastructureRef
	if infraMachineRef.Name == "" {
		return true, nil
	}

	infraMachine, err := external.GetObjectFromContractVersionedRef(ctx, r.Client, infraMachineRef, capiMachine.Namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}

		return false, fmt.Errorf("failed to get Cluster API infra machine: %w", err)
	}

	if annotations.HasPaused(infraMachine) != wantPaused {
		return false, nil
	}

	infraMachinePausedConditionStatus, err := util.GetConditionStatus(infraMachine, clusterv1.PausedCondition)
	if err != nil {
		return false, fmt.Errorf("unable to get paused condition for %s/%s: %w", infraMachine.GetNamespace(), infraMachine.GetName(), err)
	}

	if wantPaused {
		return infraMachinePausedConditionStatus == corev1.ConditionTrue, nil
	}

	return infraMachinePausedConditionStatus != corev1.ConditionTrue, nil
}

// isOldAuthoritativeResourcePaused checks whether the old authoritative resource is paused.
func (r *MachineMigrationReconciler) isOldAuthoritativeResourcePaused(ctx context.Context, m *mapiv1beta1.Machine, sourceAuthority mapiv1beta1.MachineAuthority) (bool, error) {
	if sourceAuthority == mapiv1beta1.MachineAuthorityMachineAPI {
		cond, err := util.GetConditionStatus(m, "Paused")
		if err != nil {
			return false, fmt.Errorf("unable to get paused condition for %s/%s: %w", m.Namespace, m.Name, err)
		}

		return cond == corev1.ConditionTrue, nil
	}

	// For MachineAuthorityClusterAPI, check the corresponding CAPI resource.
	capiMachine := &clusterv1.Machine{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: m.Name}, capiMachine); err != nil {
		return false, fmt.Errorf("failed to get Cluster API machine: %w", err)
	}

	machinePausedCondition := conditions.Get(capiMachine, clusterv1.PausedCondition)
	if machinePausedCondition == nil {
		return false, nil
	}

	infraMachineRef := capiMachine.Spec.InfrastructureRef

	infraMachine, err := external.GetObjectFromContractVersionedRef(ctx, r.Client, infraMachineRef, capiMachine.Namespace)
	if err != nil {
		return false, fmt.Errorf("failed to get Cluster API infra machine: %w", err)
	}

	infraMachinePausedConditionStatus, err := util.GetConditionStatus(infraMachine, clusterv1.PausedCondition)
	if err != nil {
		return false, fmt.Errorf("unable to get paused condition for %s/%s: %w", infraMachine.GetNamespace(), infraMachine.GetName(), err)
	}

	return (machinePausedCondition.Status == metav1.ConditionTrue) && (infraMachinePausedConditionStatus == corev1.ConditionTrue), nil
}

// ensureClusterAPITargetUnpaused requests unpause on the Cluster API target
// resources and reports whether they currently appear unpaused.
func (r *MachineMigrationReconciler) ensureClusterAPITargetUnpaused(ctx context.Context, m *mapiv1beta1.Machine) (bool, error) {
	capiMachine := &clusterv1.Machine{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: m.Name}, capiMachine); err != nil {
		return false, fmt.Errorf("failed to get Cluster API machine: %w", err)
	}

	infraMachineRef := capiMachine.Spec.InfrastructureRef

	infraMachine, err := external.GetObjectFromContractVersionedRef(ctx, r.Client, infraMachineRef, capiMachine.Namespace)
	if err != nil {
		return false, fmt.Errorf("failed to get Cluster API infra machine: %w", err)
	}

	if annotations.HasPaused(capiMachine) {
		capiMachineCopy := capiMachine.DeepCopy()
		delete(capiMachine.Annotations, clusterv1.PausedAnnotation)

		if err := r.Patch(ctx, capiMachine, client.MergeFrom(capiMachineCopy)); err != nil {
			return false, fmt.Errorf("failed to patch Cluster API machine: %w", err)
		}
	}

	if annotations.HasPaused(infraMachine) {
		infraMachineCopy := infraMachine.DeepCopy()

		util.RemoveAnnotation(infraMachine, clusterv1.PausedAnnotation)

		if err := r.Patch(ctx, infraMachine, client.MergeFrom(infraMachineCopy)); err != nil {
			return false, fmt.Errorf("failed to patch Cluster API infra machine: %w", err)
		}
	}

	machinePausedCondition := conditions.Get(capiMachine, clusterv1.PausedCondition)
	if machinePausedCondition != nil && machinePausedCondition.Status == metav1.ConditionTrue {
		return false, nil
	}

	infraPausedCondition, err := util.GetCondition(infraMachine, clusterv1.PausedCondition)
	if err != nil {
		return false, fmt.Errorf("unable to get paused condition for %s/%s: %w", infraMachine.GetNamespace(), infraMachine.GetName(), err)
	}

	if infraPausedCondition == nil {
		// Condition is absent, treat resource as unpaused.
		return true, nil
	}

	return corev1.ConditionStatus(infraPausedCondition.Status) != corev1.ConditionTrue, nil
}

func (r *MachineMigrationReconciler) requestClusterAPIResourcesPaused(ctx context.Context, m *mapiv1beta1.Machine) (bool, error) {
	capiMachine := &clusterv1.Machine{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: m.Name}, capiMachine); err != nil {
		return false, fmt.Errorf("failed to get Cluster API machine: %w", err)
	}

	infraMachineRef := capiMachine.Spec.InfrastructureRef

	infraMachine, err := external.GetObjectFromContractVersionedRef(ctx, r.Client, infraMachineRef, capiMachine.Namespace)
	if err != nil {
		return false, fmt.Errorf("failed to get Cluster API infra machine: %w", err)
	}

	updated := false

	if !annotations.HasPaused(capiMachine) {
		capiMachineCopy := capiMachine.DeepCopy()
		annotations.AddAnnotations(capiMachine, map[string]string{clusterv1.PausedAnnotation: ""})

		if err := r.Patch(ctx, capiMachine, client.MergeFrom(capiMachineCopy)); err != nil {
			return false, fmt.Errorf("failed to patch Cluster API machine: %w", err)
		}

		updated = true
	}

	if !annotations.HasPaused(infraMachine) {
		infraMachineCopy := infraMachine.DeepCopy()

		annotations.AddAnnotations(infraMachine, map[string]string{clusterv1.PausedAnnotation: ""})

		if err := r.Patch(ctx, infraMachine, client.MergeFrom(infraMachineCopy)); err != nil {
			return false, fmt.Errorf("failed to patch Cluster API infra machine: %w", err)
		}

		updated = true
	}

	return updated, nil
}

func (r *MachineMigrationReconciler) requestClusterAPIResourcesPausedForRollback(ctx context.Context, m *mapiv1beta1.Machine) (bool, error) {
	capiMachine := &clusterv1.Machine{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: m.Name}, capiMachine); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}

		return false, fmt.Errorf("failed to get Cluster API machine: %w", err)
	}

	updated := false

	if !annotations.HasPaused(capiMachine) {
		capiMachineCopy := capiMachine.DeepCopy()
		annotations.AddAnnotations(capiMachine, map[string]string{clusterv1.PausedAnnotation: ""})

		if err := r.Patch(ctx, capiMachine, client.MergeFrom(capiMachineCopy)); err != nil {
			return false, fmt.Errorf("failed to patch Cluster API machine: %w", err)
		}

		updated = true
	}

	infraMachineRef := capiMachine.Spec.InfrastructureRef

	infraMachine, err := external.GetObjectFromContractVersionedRef(ctx, r.Client, infraMachineRef, capiMachine.Namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return updated, nil
		}

		return false, fmt.Errorf("failed to get Cluster API infra machine: %w", err)
	}

	if !annotations.HasPaused(infraMachine) {
		infraMachineCopy := infraMachine.DeepCopy()

		annotations.AddAnnotations(infraMachine, map[string]string{clusterv1.PausedAnnotation: ""})

		if err := r.Patch(ctx, infraMachine, client.MergeFrom(infraMachineCopy)); err != nil {
			return false, fmt.Errorf("failed to patch Cluster API infra machine: %w", err)
		}

		updated = true
	}

	return updated, nil
}

// requestOldAuthoritativeResourcePaused requests that the old authoritative resource be paused.
func (r *MachineMigrationReconciler) requestOldAuthoritativeResourcePaused(ctx context.Context, m *mapiv1beta1.Machine, sourceAuthority mapiv1beta1.MachineAuthority) (bool, error) {
	updated := false
	//nolint:wsl
	switch sourceAuthority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		// Switching spec.AuthoritativeAPI already requests pause on the MAPI resource.
	case mapiv1beta1.MachineAuthorityClusterAPI:
		return r.requestClusterAPIResourcesPaused(ctx, m)
	case mapiv1beta1.MachineAuthorityMigrating:
		// Value is disallowed by the openAPI schema validation.
	}

	return updated, nil
}

func (r *MachineMigrationReconciler) isSynchronized(ctx context.Context, mapiMachine *mapiv1beta1.Machine) (bool, error) {
	// Check if the Synchronized condition is set to True.
	// If it is not, this indicates an unmigratable resource and therefore should take no action.
	if cond, err := util.GetConditionStatus(mapiMachine, string(controllers.SynchronizedCondition)); err != nil {
		return false, fmt.Errorf("unable to get synchronized condition for %s/%s: %w", mapiMachine.Namespace, mapiMachine.Name, err)
	} else if cond != corev1.ConditionTrue {
		return false, nil
	}

	// Use SynchronizedAPI to deterministically know which object's generation
	// the SynchronizedGeneration refers to. This avoids the previous heuristic
	// which was not safe when a user aborts an in-progress migration.
	switch mapiMachine.Status.SynchronizedAPI {
	case mapiv1beta1.MachineAPISynchronized:
		return mapiMachine.Status.SynchronizedGeneration == mapiMachine.Generation, nil
	case mapiv1beta1.ClusterAPISynchronized:
		capiMachine := &clusterv1.Machine{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: mapiMachine.Name}, capiMachine); err != nil {
			return false, fmt.Errorf("failed to get Cluster API machine: %w", err)
		}

		return mapiMachine.Status.SynchronizedGeneration == capiMachine.Generation, nil
	case "":
		// SynchronizedAPI not yet set by sync controller - not synchronized
		return false, nil
	default:
		return false, fmt.Errorf("unable to determine synchronization source: %w", synccommon.InvalidSynchronizedAPIError(mapiMachine.Status.SynchronizedAPI))
	}
}

func (r *MachineMigrationReconciler) applyMigrationStatusWithPatch(ctx context.Context, m *mapiv1beta1.Machine, authority mapiv1beta1.MachineAuthority) error {
	return synccommon.ApplyMigrationStatus[*machinev1applyconfigs.MachineStatusApplyConfiguration](ctx, r.Client, controllerName, machinev1applyconfigs.Machine, m, authority)
}

func (r *MachineMigrationReconciler) applyMigrationStatusAndResetSyncStatusWithPatch(ctx context.Context, m *mapiv1beta1.Machine, authority mapiv1beta1.MachineAuthority) error {
	return synccommon.ApplyMigrationStatusAndResetSyncStatus[*machinev1applyconfigs.MachineStatusApplyConfiguration](ctx, r.Client, controllerName, machinev1applyconfigs.Machine, m, authority)
}

// ensureUnpauseAfterCancellation ensures CAPI resources are unpaused after migration cancellation.
// When cancelling a migration, any CAPI resources that may have been paused should be unpaused.
func (r *MachineMigrationReconciler) ensureUnpauseAfterCancellation(ctx context.Context, mapiMachine *mapiv1beta1.Machine) error {
	capiMachine := &clusterv1.Machine{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: mapiMachine.Name}, capiMachine); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to get Cluster API machine: %w", err)
	}

	if err := r.unpauseCAPIMachine(ctx, capiMachine); err != nil {
		return err
	}

	return r.unpauseCAPIInfraMachine(ctx, capiMachine)
}

// unpauseCAPIMachine removes the paused annotation from a CAPI machine if present.
func (r *MachineMigrationReconciler) unpauseCAPIMachine(ctx context.Context, capiMachine *clusterv1.Machine) error {
	if !annotations.HasPaused(capiMachine) {
		return nil
	}

	capiMachineCopy := capiMachine.DeepCopy()
	delete(capiMachine.Annotations, clusterv1.PausedAnnotation)

	if err := r.Patch(ctx, capiMachine, client.MergeFrom(capiMachineCopy)); err != nil {
		return fmt.Errorf("failed to remove paused annotation from Cluster API machine: %w", err)
	}

	return nil
}

// unpauseCAPIInfraMachine removes the paused annotation from the infra machine if present.
func (r *MachineMigrationReconciler) unpauseCAPIInfraMachine(ctx context.Context, capiMachine *clusterv1.Machine) error {
	infraMachineRef := capiMachine.Spec.InfrastructureRef
	if infraMachineRef.Name == "" {
		return nil
	}

	infraMachine, err := external.GetObjectFromContractVersionedRef(ctx, r.Client, infraMachineRef, capiMachine.Namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to get Cluster API infra machine: %w", err)
	}

	if !annotations.HasPaused(infraMachine) {
		return nil
	}

	infraMachineCopy := infraMachine.DeepCopy()

	util.RemoveAnnotation(infraMachine, clusterv1.PausedAnnotation)

	if err := r.Patch(ctx, infraMachine, client.MergeFrom(infraMachineCopy)); err != nil {
		return fmt.Errorf("failed to remove paused annotation from Cluster API infra machine: %w", err)
	}

	return nil
}

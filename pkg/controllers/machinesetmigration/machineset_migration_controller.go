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
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

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

const controllerName = "MachineSetMigrationController"

// MachineSetMigrationReconciler reconciles MachineSet resources for migration.
type MachineSetMigrationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Infra         *configv1.Infrastructure
	Platform      configv1.PlatformType
	InfraTypes    util.InfraTypes
	CAPINamespace string
	MAPINamespace string
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
		Watches(
			r.InfraTypes.Template(),
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
//
//nolint:funlen,cyclop
func (r *MachineSetMigrationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	// Keep the initial status.AuthoritativeAPI defaulting aligned with the
	// Machine API controllers so migration still works before this controller runs.
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

	if mapiMachineSet.Spec.AuthoritativeAPI == mapiMachineSet.Status.AuthoritativeAPI {
		// No migration is being requested for this resource, nothing to do.
		return ctrl.Result{}, nil
	}

	// Initialize status.authoritativeAPI on the first reconciliation.
	if mapiMachineSet.Status.AuthoritativeAPI == "" {
		if err := r.applyMigrationStatusWithPatch(ctx, mapiMachineSet, mapiMachineSet.Spec.AuthoritativeAPI); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status: %w", err)
		}

		return ctrl.Result{}, nil
	}

	currentAuthority, desiredAuthority, isMigrating, err := synccommon.MigrationDirection(
		mapiMachineSet.Status.AuthoritativeAPI,
		mapiMachineSet.Status.SynchronizedAPI,
		mapiMachineSet.Spec.AuthoritativeAPI,
	)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to determine current authority while migrating: %w", err)
	}

	// If the resource is already Migrating and spec switches back to the source
	// authority, treat it as a cancellation request.
	if result, handled, err := r.handleMigrationCancellation(ctx, logger, mapiMachineSet, currentAuthority, desiredAuthority, isMigrating); err != nil {
		return result, err
	} else if handled {
		return result, nil
	}

	// Check that the resource is synchronized and up-to-date.
	//
	// This MUST be checked BEFORE setting status.authoritativeAPI to Migrating,
	// because after that the sync controller will not run to update it and we
	// will deadlock.
	if isSynchronized, err := r.isSynchronized(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to check the resource is synchronized and up-to-date with its authority: %w", err)
	} else if !isSynchronized {
		// The Authoritative API resource is not fully synced up yet, requeue to check later.
		logger.Info("Authoritative machine set and its copy are not synchronized yet, will retry later")

		return ctrl.Result{}, nil
	}

	// Make sure the authoritativeAPI resource status is set to migrating.
	if !isMigrating {
		logger.Info("Detected migration request for machine set")

		if err := r.applyMigrationStatusWithPatch(ctx, mapiMachineSet, mapiv1beta1.MachineAuthorityMigrating); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to set authoritativeAPI to Migrating: %w", err)
		}

		logger.Info("Acknowledged migration request for machine set")

		// Wait for the change to propagate.
		return ctrl.Result{}, nil
	}

	// Request pausing on the authoritative resource.
	if updated, err := r.requestOldAuthoritativeResourcePaused(ctx, mapiMachineSet, currentAuthority); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to request pause on authoritative machine set: %w", err)
	} else if updated {
		logger.Info("Requested pausing for authoritative machine set")

		// Wait for the change to propagate.
		return ctrl.Result{}, nil
	}

	// Check that the old authoritative resource is paused.
	if paused, err := r.isOldAuthoritativeResourcePaused(ctx, mapiMachineSet, currentAuthority); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check paused on old authoritative machine set: %w", err)
	} else if !paused {
		// The Authoritative API resource is not paused yet, requeue to check later.
		logger.Info("Authoritative machine set is not paused yet, will retry later")

		return ctrl.Result{}, nil
	}

	// Only Cluster API exposes a target unpause signal before completing migration.
	// Machine API only clears Paused after status.AuthoritativeAPI leaves Migrating.
	if desiredAuthority == mapiv1beta1.MachineAuthorityClusterAPI {
		if unpaused, err := r.ensureClusterAPITargetUnpaused(ctx, mapiMachineSet); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to ensure the Cluster API target is unpaused: %w", err)
		} else if !unpaused {
			logger.Info("New authoritative machine set is not unpaused yet, waiting for target controller")

			return ctrl.Result{}, nil
		}
	}

	// Set the actual AuthoritativeAPI to the desired one and reset the synchronized generation and condition.
	// SynchronizedAPI will be updated by the sync controller after resync.
	if err := r.applyMigrationStatusAndResetSyncStatusWithPatch(ctx, mapiMachineSet, mapiMachineSet.Spec.AuthoritativeAPI); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply authoritativeAPI and reset sync status: %w", err)
	}

	logger.Info("Machine set authority switch has now been completed", "authoritativeAPI", mapiMachineSet.Spec.AuthoritativeAPI)
	logger.Info("Machine set migrated successfully")

	return ctrl.Result{}, nil
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

func (r *MachineSetMigrationReconciler) handleMigrationCancellation(
	ctx context.Context,
	logger logr.Logger,
	mapiMachineSet *mapiv1beta1.MachineSet,
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
		readyToRollback, result, err := r.ensureRollbackToMachineAPIReady(ctx, logger, mapiMachineSet)
		if err != nil {
			return ctrl.Result{}, true, err
		}

		if !readyToRollback {
			return result, true, nil
		}
	case mapiv1beta1.MachineAuthorityClusterAPI:
		readyToRollback, result, err := r.ensureRollbackToClusterAPIReady(ctx, logger, mapiMachineSet)
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
	if err := r.applyMigrationStatusAndResetSyncStatusWithPatch(ctx, mapiMachineSet, currentAuthority); err != nil {
		return ctrl.Result{}, true, fmt.Errorf("failed to rollback migration: %w", err)
	}

	logger.Info("Migration cancelled and rolled back successfully")

	return ctrl.Result{}, true, nil
}

func (r *MachineSetMigrationReconciler) ensureRollbackToMachineAPIReady(ctx context.Context, logger logr.Logger, mapiMachineSet *mapiv1beta1.MachineSet) (bool, ctrl.Result, error) {
	rollbackObserved, err := r.isClusterAPIResourcesInRollbackState(ctx, mapiMachineSet, true)
	if err != nil {
		return false, ctrl.Result{}, fmt.Errorf("failed to observe re-paused Cluster API resources after cancellation: %w", err)
	}

	if rollbackObserved {
		return true, ctrl.Result{}, nil
	}

	// Rolling back to Machine API means Cluster API was the target. Re-pause it
	// so the cluster returns to a single authoritative API before status is reset.
	if _, err := r.requestClusterAPIResourcesPausedForRollback(ctx, mapiMachineSet); err != nil {
		return false, ctrl.Result{}, fmt.Errorf("failed to re-pause Cluster API resources after cancellation: %w", err)
	}

	logger.Info("Waiting for Cluster API rollback target to pause before resetting migration status")

	return false, ctrl.Result{RequeueAfter: time.Second}, nil
}

func (r *MachineSetMigrationReconciler) ensureRollbackToClusterAPIReady(ctx context.Context, logger logr.Logger, mapiMachineSet *mapiv1beta1.MachineSet) (bool, ctrl.Result, error) {
	rollbackObserved, err := r.isClusterAPIResourcesInRollbackState(ctx, mapiMachineSet, false)
	if err != nil {
		return false, ctrl.Result{}, fmt.Errorf("failed to observe unpaused Cluster API resources after cancellation: %w", err)
	}

	if rollbackObserved {
		return true, ctrl.Result{}, nil
	}

	// Rolling back to Cluster API means Cluster API was the source and may have
	// been paused during the migration attempt. Unpause it before resetting status.
	if err := r.ensureUnpauseAfterCancellation(ctx, mapiMachineSet); err != nil {
		return false, ctrl.Result{}, fmt.Errorf("failed to unpause after cancellation: %w", err)
	}

	logger.Info("Waiting for Cluster API rollback source to unpause before resetting migration status")

	return false, ctrl.Result{RequeueAfter: time.Second}, nil
}

func (r *MachineSetMigrationReconciler) isClusterAPIResourcesInRollbackState(ctx context.Context, ms *mapiv1beta1.MachineSet, wantPaused bool) (bool, error) {
	capiMachineSet := &clusterv1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: ms.Name}, capiMachineSet); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}

		return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
	}

	if annotations.HasPaused(capiMachineSet) != wantPaused {
		return false, nil
	}

	machineSetPausedCondition := conditions.Get(capiMachineSet, clusterv1.PausedCondition)
	if wantPaused {
		return machineSetPausedCondition != nil && machineSetPausedCondition.Status == metav1.ConditionTrue, nil
	}

	if machineSetPausedCondition != nil && machineSetPausedCondition.Status == metav1.ConditionTrue {
		return false, nil
	}

	return true, nil
}

// isOldAuthoritativeResourcePaused checks whether the old authoritative resource is paused.
func (r *MachineSetMigrationReconciler) isOldAuthoritativeResourcePaused(ctx context.Context, ms *mapiv1beta1.MachineSet, sourceAuthority mapiv1beta1.MachineAuthority) (bool, error) {
	if sourceAuthority == mapiv1beta1.MachineAuthorityMachineAPI {
		cond, err := util.GetConditionStatus(ms, "Paused")
		if err != nil {
			return false, fmt.Errorf("unable to get paused condition for %s/%s: %w", ms.Namespace, ms.Name, err)
		}

		return cond == corev1.ConditionTrue, nil
	}

	// For MachineAuthorityClusterAPI, check the corresponding CAPI resource.
	capiMachineSet := &clusterv1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: ms.Name}, capiMachineSet); err != nil {
		return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
	}

	machinePausedCondition := conditions.Get(capiMachineSet, clusterv1.PausedCondition)
	if machinePausedCondition == nil {
		return false, nil
	}

	// InfraMachineTemplate doesn't have a reconciler, so it does not need pausing.
	// The only relevant provider that reconciles the infra machine template is
	// IBM Cloud, PowerVS, and it only updates status.
	// See: https://github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/blob/main/controllers/ibmpowervsmachinetemplate_controller.go
	return (machinePausedCondition.Status == metav1.ConditionTrue), nil
}

// ensureClusterAPITargetUnpaused requests unpause on the Cluster API target
// resources and reports whether they currently appear unpaused.
func (r *MachineSetMigrationReconciler) ensureClusterAPITargetUnpaused(ctx context.Context, ms *mapiv1beta1.MachineSet) (bool, error) {
	capiMachineSet := &clusterv1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: ms.Name}, capiMachineSet); err != nil {
		return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
	}

	if annotations.HasPaused(capiMachineSet) {
		capiMachineSetCopy := capiMachineSet.DeepCopy()
		delete(capiMachineSet.Annotations, clusterv1.PausedAnnotation)

		if err := r.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSetCopy)); err != nil {
			return false, fmt.Errorf("failed to patch Cluster API machine set: %w", err)
		}
	}

	machinePausedCondition := conditions.Get(capiMachineSet, clusterv1.PausedCondition)
	// Only consider the target unpaused once Cluster API has acknowledged the
	// change by setting PausedCondition to False. A nil condition means the
	// target MachineSet has not been reconciled yet, so migration must wait.
	if machinePausedCondition == nil || machinePausedCondition.Status == metav1.ConditionTrue {
		return false, nil
	}

	// InfraMachineTemplate doesn't have its own reconciler, so the MachineSet
	// paused condition is the only readiness signal that matters here.
	return true, nil
}

func (r *MachineSetMigrationReconciler) requestClusterAPIResourcesPaused(ctx context.Context, ms *mapiv1beta1.MachineSet) (bool, error) {
	capiMachineSet := &clusterv1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: ms.Name}, capiMachineSet); err != nil {
		return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
	}

	updated := false

	if !annotations.HasPaused(capiMachineSet) {
		capiMachineSetCopy := capiMachineSet.DeepCopy()
		annotations.AddAnnotations(capiMachineSet, map[string]string{clusterv1.PausedAnnotation: ""})

		if err := r.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSetCopy)); err != nil {
			return false, fmt.Errorf("failed to patch Cluster API machine set: %w", err)
		}

		updated = true
	}

	return updated, nil
}

func (r *MachineSetMigrationReconciler) requestClusterAPIResourcesPausedForRollback(ctx context.Context, ms *mapiv1beta1.MachineSet) (bool, error) {
	capiMachineSet := &clusterv1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: ms.Name}, capiMachineSet); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}

		return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
	}

	updated := false

	if !annotations.HasPaused(capiMachineSet) {
		capiMachineSetCopy := capiMachineSet.DeepCopy()
		annotations.AddAnnotations(capiMachineSet, map[string]string{clusterv1.PausedAnnotation: ""})

		if err := r.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSetCopy)); err != nil {
			return false, fmt.Errorf("failed to patch Cluster API machine set: %w", err)
		}

		updated = true
	}

	return updated, nil
}

// requestOldAuthoritativeResourcePaused requests that the old authoritative resource be paused.
func (r *MachineSetMigrationReconciler) requestOldAuthoritativeResourcePaused(ctx context.Context, ms *mapiv1beta1.MachineSet, sourceAuthority mapiv1beta1.MachineAuthority) (bool, error) {
	updated := false
	//nolint:wsl
	switch sourceAuthority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		// Switching spec.AuthoritativeAPI already requests pause on the MAPI resource.
	case mapiv1beta1.MachineAuthorityClusterAPI:
		return r.requestClusterAPIResourcesPaused(ctx, ms)
	case mapiv1beta1.MachineAuthorityMigrating:
		// Value is disallowed by the openAPI schema validation.
	}

	return updated, nil
}

func (r *MachineSetMigrationReconciler) isSynchronized(ctx context.Context, mapiMachineSet *mapiv1beta1.MachineSet) (bool, error) {
	// Check if the Synchronized condition is set to True.
	// If it is not, this indicates an unmigratable resource and therefore should take no action.
	if cond, err := util.GetConditionStatus(mapiMachineSet, string(controllers.SynchronizedCondition)); err != nil {
		return false, fmt.Errorf("unable to get synchronized condition for %s/%s: %w", mapiMachineSet.Namespace, mapiMachineSet.Name, err)
	} else if cond != corev1.ConditionTrue {
		return false, nil
	}

	// Use SynchronizedAPI to deterministically know which object's generation
	// the SynchronizedGeneration refers to. This avoids the previous heuristic
	// which was not safe when a user aborts an in-progress migration.
	switch mapiMachineSet.Status.SynchronizedAPI {
	case mapiv1beta1.MachineAPISynchronized:
		return mapiMachineSet.Status.SynchronizedGeneration == mapiMachineSet.Generation, nil
	case mapiv1beta1.ClusterAPISynchronized:
		capiMachineSet := &clusterv1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
			return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
		}

		return mapiMachineSet.Status.SynchronizedGeneration == capiMachineSet.Generation, nil
	case "":
		// SynchronizedAPI not yet set by sync controller - not synchronized
		return false, nil
	default:
		return false, fmt.Errorf("unable to determine synchronization source: %w", synccommon.InvalidSynchronizedAPIError(mapiMachineSet.Status.SynchronizedAPI))
	}
}

func (r *MachineSetMigrationReconciler) applyMigrationStatusWithPatch(ctx context.Context, ms *mapiv1beta1.MachineSet, authority mapiv1beta1.MachineAuthority) error {
	return synccommon.ApplyMigrationStatus[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](ctx, r.Client, controllerName, machinev1applyconfigs.MachineSet, ms, authority)
}

func (r *MachineSetMigrationReconciler) applyMigrationStatusAndResetSyncStatusWithPatch(ctx context.Context, ms *mapiv1beta1.MachineSet, authority mapiv1beta1.MachineAuthority) error {
	return synccommon.ApplyMigrationStatusAndResetSyncStatus[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](ctx, r.Client, controllerName, machinev1applyconfigs.MachineSet, ms, authority)
}

// ensureUnpauseAfterCancellation ensures CAPI resources are unpaused after migration cancellation.
// When cancelling a migration, any CAPI resources that may have been paused should be unpaused.
// InfraMachineTemplate doesn't have a reconciler and thus doesn't need unpausing.
func (r *MachineSetMigrationReconciler) ensureUnpauseAfterCancellation(ctx context.Context, mapiMachineSet *mapiv1beta1.MachineSet) error {
	capiMachineSet := &clusterv1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to get Cluster API machine set: %w", err)
	}

	if !annotations.HasPaused(capiMachineSet) {
		return nil
	}

	capiMachineSetCopy := capiMachineSet.DeepCopy()
	delete(capiMachineSet.Annotations, clusterv1.PausedAnnotation)

	if err := r.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSetCopy)); err != nil {
		return fmt.Errorf("failed to remove paused annotation from Cluster API machine set: %w", err)
	}

	return nil
}

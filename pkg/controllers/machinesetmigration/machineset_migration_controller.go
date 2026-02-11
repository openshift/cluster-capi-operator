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
	"errors"
	"fmt"

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

// errInvalidSynchronizedAPI is returned when SynchronizedAPI has an unexpected value.
var errInvalidSynchronizedAPI = errors.New("invalid synchronizedAPI value")

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
//nolint:funlen
func (r *MachineSetMigrationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	logger.V(1).Info("Reconciling machine set")
	defer logger.V(1).Info("Finished reconciling machine set")

	mapiMachineSet := &mapiv1beta1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: req.Name}, mapiMachineSet); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get MAPI machine set: %w", err)
	} else if apierrors.IsNotFound(err) {
		logger.Info("MachineSet has been deleted. Migration not required")
		return ctrl.Result{}, nil
	}

	if mapiMachineSet.Spec.AuthoritativeAPI == mapiMachineSet.Status.AuthoritativeAPI {
		// No migration is being requested for this resource, nothing to do.
		return ctrl.Result{}, nil
	}

	// N.B. Very similar logic is also present in the Machine API machine/machineset controllers
	// to cover for the cases when the migration controller is not running (e.g. on not yet supported platforms),
	// as such if any change is done to this logic, please consider changing it also there. See:
	// https://github.com/openshift/machine-api-operator/pull/1386/files#diff-3a93acbdaa255c0afa7f52535fc7df9c3890d6403035dd4c3bd47b0092eb3a37R177-R194
	// Handle the case where AuthoritativeAPI status field is empty.
	if mapiMachineSet.Status.AuthoritativeAPI == "" {
		// Initialize status.AuthoritativeAPI from spec.AuthoritativeAPI.
		// SynchronizedAPI will be set by the sync controller after first successful sync.
		if err := r.applyMigrationStatusWithPatch(ctx, mapiMachineSet, mapiMachineSet.Spec.AuthoritativeAPI); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status: %w", err)
		}

		return ctrl.Result{}, nil
	}

	currentAuthority, desiredAuthority, isMigrating := synccommon.MigrationDirection(
		mapiMachineSet.Status.AuthoritativeAPI,
		mapiMachineSet.Status.SynchronizedAPI,
		mapiMachineSet.Spec.AuthoritativeAPI,
	)

	// Handle migration cancellation: if already in Migrating state and spec matches
	// the source authority (before migration started), the user wants to cancel.
	if isMigrating && currentAuthority != "" && desiredAuthority == currentAuthority {
		logger.Info("Migration cancellation detected, rolling back to source authority",
			"sourceAuthority", currentAuthority)

		// Unpause any resources that were paused during migration attempt.
		if err := r.ensureUnpauseAfterCancellation(ctx, mapiMachineSet); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to unpause after cancellation: %w", err)
		}

		// Reset status back to source authority and reset sync status.
		if err := r.applyMigrationStatusAndResetSyncStatusWithPatch(ctx, mapiMachineSet, currentAuthority); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to rollback migration: %w", err)
		}

		logger.Info("Migration cancelled and rolled back successfully")

		return ctrl.Result{}, nil
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

	// Make sure the new authoritative resource has been requested to unpause.
	if err := r.ensureUnpauseRequestedOnNewAuthoritativeResource(ctx, mapiMachineSet, desiredAuthority); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure the new AuthoritativeAPI has been un-paused: %w", err)
	}

	// Check that the new authoritative resource has been unpaused by its controller.
	// This ensures the target controller is running and responsive before completing migration.
	if unpaused, err := r.isNewAuthoritativeResourceUnpaused(ctx, mapiMachineSet, desiredAuthority); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check unpaused on new authoritative machine set: %w", err)
	} else if !unpaused {
		// The new authoritative resource is not unpaused yet.
		// The watches on CAPI MachineSet will trigger reconciliation
		// when the target controller updates the Paused condition.
		logger.Info("New authoritative machine set is not unpaused yet, waiting for target controller")

		return ctrl.Result{}, nil
	}

	// Set the actual AuthoritativeAPI to the desired one and reset the synchronized generation and condition.
	// SynchronizedAPI will be updated by the sync controller after resync.
	if err := r.applyMigrationStatusAndResetSyncStatusWithPatch(ctx, mapiMachineSet, mapiMachineSet.Spec.AuthoritativeAPI); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply authoritativeAPI and reset sync status: %w", err)
	}

	logger.Info("Machine set authority switch has now been completed and the resource unpaused", "authoritativeAPI", mapiMachineSet.Spec.AuthoritativeAPI)
	logger.Info("Machine set migrated successfully")

	return ctrl.Result{}, nil
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

	// InfraMachineTemplate doesn't have a reconciler and thus it doesn't need pausing.
	// The only provider we are interested in, that reconciles the inframachinetemplate, is ibmcloud/powervs
	// which only updates its status
	// see: https://github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/blob/main/controllers/ibmpowervsmachinetemplate_controller.go
	return (machinePausedCondition.Status == metav1.ConditionTrue), nil
}

// isNewAuthoritativeResourceUnpaused checks whether the new authoritative resource has been unpaused
// by its controller. This ensures the target controller is running and responsive before completing migration.
func (r *MachineSetMigrationReconciler) isNewAuthoritativeResourceUnpaused(ctx context.Context, ms *mapiv1beta1.MachineSet, targetAuthority mapiv1beta1.MachineAuthority) (bool, error) {
	switch targetAuthority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		cond, err := util.GetConditionStatus(ms, "Paused")
		if err != nil {
			// If we can't get the condition, treat as unpaused
			return true, nil
		}

		return cond != corev1.ConditionTrue, nil
	case mapiv1beta1.MachineAuthorityClusterAPI:
		capiMachineSet := &clusterv1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: ms.Name}, capiMachineSet); err != nil {
			return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
		}

		machinePausedCondition := conditions.Get(capiMachineSet, clusterv1.PausedCondition)
		if machinePausedCondition != nil && machinePausedCondition.Status == metav1.ConditionTrue {
			return false, nil
		}

		return true, nil
	default:
		return false, fmt.Errorf("unsupported target authority: %s", targetAuthority)
	}
}

func (r *MachineSetMigrationReconciler) ensureUnpauseRequestedOnNewAuthoritativeResource(ctx context.Context, mapiMachineSet *mapiv1beta1.MachineSet, targetAuthority mapiv1beta1.MachineAuthority) error {
	// Request that the new authoritative resource reconciliation is un-paused.
	//nolint:wsl
	switch targetAuthority {
	case mapiv1beta1.MachineAuthorityClusterAPI:
		// For requesting unpausing of a CAPI resource, remove the paused annotation on it.
		// So check if the ClusterAPI resource has the paused annotation and if so remove it.
		capiMachineSet := &clusterv1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
			return fmt.Errorf("failed to get Cluster API machine set: %w", err)
		}

		if annotations.HasPaused(capiMachineSet) {
			capiMachineSetCopy := capiMachineSet.DeepCopy()
			delete(capiMachineSet.Annotations, clusterv1.PausedAnnotation)

			if err := r.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSetCopy)); err != nil {
				return fmt.Errorf("failed to patch Cluster API machine set: %w", err)
			}
		}

		// InfraMachineTemplate doesn't have a reconciler and thus it doesn't need pausing.
		// The only provider we are interested in, that reconciles the inframachinetemplate, is ibmcloud/powervs
		// which only updates its status
		// see: https://github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/blob/main/controllers/ibmpowervsmachinetemplate_controller.go
	case mapiv1beta1.MachineAuthorityMachineAPI:
		// For requesting unpausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller. Nothing to do here.
	case mapiv1beta1.MachineAuthorityMigrating:
		// Value is disallowed by the openAPI schema validation.
	}

	return nil
}

// requestOldAuthoritativeResourcePaused requests the old authoritative resource is paused.
func (r *MachineSetMigrationReconciler) requestOldAuthoritativeResourcePaused(ctx context.Context, ms *mapiv1beta1.MachineSet, sourceAuthority mapiv1beta1.MachineAuthority) (bool, error) {
	// Request that the old authoritative resource reconciliation is paused.
	updated := false
	//nolint:wsl
	switch sourceAuthority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		// For requesting pausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller.
	case mapiv1beta1.MachineAuthorityClusterAPI:
		// For requesting pausing of a CAPI resource, set the paused annotation on it.
		// This is required when the old authoritative API is ClusterAPI, so check if the ClusterAPI resource
		// has the paused annotation, otherwise set it.
		capiMachineSet := &clusterv1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: ms.Name}, capiMachineSet); err != nil {
			return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
		}

		if !annotations.HasPaused(capiMachineSet) {
			capiMachineSetCopy := capiMachineSet.DeepCopy()
			annotations.AddAnnotations(capiMachineSet, map[string]string{clusterv1.PausedAnnotation: ""})
			if err := r.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSetCopy)); err != nil {
				return false, fmt.Errorf("failed to patch Cluster API machine set: %w", err)
			}

			updated = true
		}

		// InfraMachineTemplate doesn't have a reconciler and thus it doesn't need pausing.
		// The only provider we are interested in, that reconciles the inframachinetemplate, is ibmcloud/powervs
		// which only updates its status
		// see: https://github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/blob/main/controllers/ibmpowervsmachinetemplate_controller.go
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
		return false, fmt.Errorf("%w: %s", errInvalidSynchronizedAPI, mapiMachineSet.Status.SynchronizedAPI)
	}
}

// applyMigrationStatusWithPatch updates the migration controller status fields using a server-side apply patch.
func (r *MachineSetMigrationReconciler) applyMigrationStatusWithPatch(ctx context.Context, ms *mapiv1beta1.MachineSet, authority mapiv1beta1.MachineAuthority) error {
	return synccommon.ApplyMigrationStatus[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](ctx, r.Client, controllerName, machinev1applyconfigs.MachineSet, ms, authority)
}

// applyMigrationStatusAndResetSyncStatusWithPatch updates the migration controller status and resets sync status.
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

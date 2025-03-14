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
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	controllerName               string = "MachineSetMigrationController"
	migrationControllerFinalizer        = "migration.machine.openshift.io"
)

// MachineSetMigrationReconciler reconciles CAPI and MAPI MachineSets for migration purposes.
type MachineSetMigrationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	CAPINamespace string
	MAPINamespace string
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineSetMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Allow the namespaces to be set externally for test purposes, when not set,
	// default to the production namespaces.
	if r.CAPINamespace == "" {
		r.CAPINamespace = consts.DefaultManagedNamespace
	}

	if r.MAPINamespace == "" {
		r.MAPINamespace = consts.DefaultMAPIManagedNamespace
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&machinev1beta1.MachineSet{}, builder.WithPredicates(util.FilterNamespace(r.MAPINamespace))).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	// Set up API helpers from the manager.
	r.Client = mgr.GetClient()
	r.Scheme = mgr.GetScheme()
	r.Recorder = mgr.GetEventRecorderFor(controllerName)

	return nil
}

func (r *MachineSetMigrationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	logger.V(1).Info("Reconciling machine set")
	defer logger.V(1).Info("Finished reconciling machine set")

	mapiMachineSet := &machinev1beta1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: req.Name}, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get MAPI machine set: %w", err)
	}

	logger.WithValues("machineset", mapiMachineSet.Name)

	// TODO: move this check in the watchPredicates?
	// Observe the spec and status authoritative API fields and look for a difference between them.
	// A difference indicating the cluster admin requested a migration between the two APIs.
	if noMigrationRequested := mapiMachineSet.Spec.AuthoritativeAPI == mapiMachineSet.Status.AuthoritativeAPI; noMigrationRequested {
		// No migration is being requested for this MAPI MachineSet, nothing to do.
		return ctrl.Result{}, nil
	}

	logger.Info("Detected migration request for resource")

	// // Check if the Synchronized condition is set to True.
	// // If it is not, this indicates an unmigratable resource and therefore should take no action.
	// if getConditionStatus(mapiMachineSet, consts.SynchronizedCondition) != corev1.ConditionTrue {
	// 	// The Resource is NOT Syncronized, so the resource is not migrateable, nothing to do.
	// 	logger.Info("Cluster API mirror resource is not yet in sync with the Machine API one, trying again later")
	// 	return ctrl.Result{}, nil
	// }

	// If authoritativeAPI status is empty, it means it is the first time we see this resource.
	// Set the status.authoritativeAPI to match the spec.authoritativeAPI.
	if mapiMachineSet.Status.AuthoritativeAPI == "" {
		if err := r.applyStatusAuthoritativeAPIWithPatch(ctx, mapiMachineSet, mapiMachineSet.Spec.AuthoritativeAPI); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
		}

		return ctrl.Result{Requeue: true}, nil
	}

	// Make sure the authoritativeAPI resource status is set to migrating.
	if mapiMachineSet.Status.AuthoritativeAPI != machinev1beta1.MachineAuthorityMigrating {
		// Set the authoritativeAPI status to Migrating.
		if err := r.applyStatusAuthoritativeAPIWithPatch(ctx, mapiMachineSet, machinev1beta1.MachineAuthorityMigrating); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
		}
		// Then wait for the old authoritativeAPI.
		return ctrl.Result{Requeue: true}, nil
	}

	logger.Info("Acknowledged migration request for resource")

	// Ensure the old authoritativeAPI resource has been requested to pause.
	if err := r.ensurePausedRequestedOnOldAuthoritativeAPI(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure old AuthoritativeAPI has been requested to pause: %w", err)
	}

	logger.Info("Requested pausing for old authoritative resource")

	// Check if the old authoritativeAPI resource is actually paused.
	if hasPausedConditionTrue, err := r.isOldAuthoritativeAPIStatusConditionPaused(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to check old AuthoritativeAPI has paused=true condition: %w", err)
	} else if !hasPausedConditionTrue {
		// The old Authoritative API resource is not paused yet, requeue to check later.
		return ctrl.Result{Requeue: true}, nil
	}

	logger.Info("Confirmed pausing for old authoritative resource")

	// Check if the synchronizedGeneration is up to date with the old authoritativeAPI's resource current generation.
	if isSynchronizedGenMatching, err := r.isSynchronizedGenerationMatchingAuthoritativeAPIGeneration(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to check synchronizedGeneration is aligned: %w", err)
	} else if !isSynchronizedGenMatching {
		// The to-be Authoritative API resource is not fully synced up yet, requeue to check later.
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if the Synchronized condition is set to True.
	// if it is not, this indicates an unmigratable resource and therefore we should error.
	if getConditionStatus(mapiMachineSet, consts.SynchronizedCondition) != corev1.ConditionTrue {
		// The Resource is NOT Synchronized, error.
		return ctrl.Result{}, errors.New("unable to proceed migrating, resources are not synchronized")
	}

	logger.Info("Confirmed syncronized status for to-be authoritative resource")

	// Add finalizer to new authoritative API.
	// This will ensure no status changes on the same reconcile.
	// The finalizer must be present on the object before we take any actions.
	if addedFinalizer, err := r.ensureFinalizerOnNewAuthoritativeAPI(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("error adding finalizer: %w", err)
	} else if addedFinalizer {
		return ctrl.Result{Requeue: true}, nil
	}

	logger.Info("Confirmed finalizer on to-be authoritative resource")

	// Remove finalizer from the old authoritative API.
	// This will ensure no status changes on the same reconcile.
	// The finalizer must be removed from the object before we take any actions.
	if removedFinalizer, err := r.ensureFinalizerRemovedOnOldAuthoritativeAPI(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	} else if removedFinalizer {
		return ctrl.Result{Requeue: true}, nil
	}

	logger.Info("Confirmed finalizer removed on old authoritative resource")

	// Set the actual AuthoritativeAPI to the desired one.
	if err := r.applyStatusAuthoritativeAPIWithPatch(ctx, mapiMachineSet, mapiMachineSet.Spec.AuthoritativeAPI); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
	}

	logger.Info("Confirmed authoritativeAPI switch for resource")

	// Make sure the new authoritativeAPI resource has been requested to unpause.
	if err := r.ensureUnpausedRequestedOnNewAuthoritativeAPI(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure the new AuthoritativeAPI has been un-paused: %w", err)
	}

	logger.Info("Confirmed new authoritativeAPI resource is now unpaused")

	return ctrl.Result{}, nil
}

// isOldAuthoritativeAPIPaused checks whether the old authoritativeAPI resources are paused.
func (r *MachineSetMigrationReconciler) isOldAuthoritativeAPIStatusConditionPaused(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet) (bool, error) {
	var isPaused bool
	if mapiMachineSet.Spec.AuthoritativeAPI == machinev1beta1.MachineAuthorityClusterAPI {
		// The old authoritativeAPI was MachineAPI.
		// Check if the MAPI resource is paused.
		isPaused = getConditionStatus(mapiMachineSet, machinev1beta1.ConditionType("Paused")) == corev1.ConditionTrue
	} else {
		// The old authoritativeAPI was ClusterAPI.
		// Check if the corresponding CAPI resources are paused.
		capiMachineSet := &capiv1beta1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
			return false, fmt.Errorf("failed to get CAPI machine set: %w", err)
		}

		// TODO: check also the paused annotation on the capiInfraMachineTemplate, for that to work
		// the CAPI provider for the InfraMachine/InfraMachineTemplate needs to honor the
		// provider contract changes added in: https://github.com/kubernetes-sigs/cluster-api/pull/11275
		// To to this we can devise the provider from the mapiMachineSet providerspec and call
		// conditions.Get() with the right capiInfraMachineTemplate type.
		// We then need to aggregate the result with the capiMachineSet pausedCondition below.
		pausedCondition := conditions.Get(capiMachineSet, capiv1beta1.ConditionType("Paused"))
		if pausedCondition == nil {
			isPaused = false
		} else {
			switch pausedCondition.Status {
			case corev1.ConditionUnknown:
				isPaused = false
			case corev1.ConditionFalse:
				isPaused = false
			case corev1.ConditionTrue:
				isPaused = true
			}
		}
	}

	return isPaused, nil
}

func (r *MachineSetMigrationReconciler) ensureUnpausedRequestedOnNewAuthoritativeAPI(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet) error {
	// Request that the new authoritative resource reconciliation is un-paused.
	switch mapiMachineSet.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// For requesting unpausing of a CAPI resource, remove the paused annotation on it.
		// So check if the ClusterAPI resource has the paused annotation and if so remove it.
		capiMachineSet := &capiv1beta1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
			return fmt.Errorf("failed to get CAPI machine set: %w", err)
		}

		if annotations.HasPaused(capiMachineSet) {
			capiMachineSetCopy := capiMachineSet.DeepCopy()
			delete(capiMachineSet.Annotations, capiv1beta1.PausedAnnotation)

			if err := r.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSetCopy)); err != nil {
				return fmt.Errorf("failed to patch CAPI machine set: %w", err)
			}
		}
	case machinev1beta1.MachineAuthorityMachineAPI:
		// For requesting unpausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller. Nothing to do here.
	default:
		// Any other value is disallowed by the openAPI schema validation.
	}

	return nil
}

func (r *MachineSetMigrationReconciler) ensurePausedRequestedOnOldAuthoritativeAPI(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet) error {
	// Request that the old authoritative resource reconciliation is paused.
	switch mapiMachineSet.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// For requesting pausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller.
	case machinev1beta1.MachineAuthorityMachineAPI:
		// For requesting pausing of a CAPI resource, set the paused annotation on it.
		// The spec.AuthoritativeAPI is set to MachineAPI, meaning that the old authoritativeAPI was ClusterAPI.
		// So Check if the ClusterAPI resource has the paused annotation, otherwise set it.
		capiMachineSet := &capiv1beta1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
			return fmt.Errorf("failed to get CAPI machine set: %w", err)
		}

		if !annotations.HasPaused(capiMachineSet) {
			capiMachineSetCopy := capiMachineSet.DeepCopy()
			annotations.AddAnnotations(capiMachineSet, map[string]string{capiv1beta1.PausedAnnotation: ""})
			if err := r.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSetCopy)); err != nil {
				return fmt.Errorf("failed to patch CAPI machine set: %w", err)
			}
		}
	default:
		// Any other value is disallowed by the openAPI schema validation.
	}

	return nil
}

// getConditionStatus returns the status for the condition.
func getConditionStatus(mapiMachineSet *machinev1beta1.MachineSet, condition machinev1beta1.ConditionType) corev1.ConditionStatus {
	for _, c := range mapiMachineSet.Status.Conditions {
		if c.Type == condition {
			return c.Status
		}
	}

	return corev1.ConditionUnknown
}

func (r *MachineSetMigrationReconciler) isSynchronizedGenerationMatchingAuthoritativeAPIGeneration(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet) (bool, error) {
	// Request that the old authoritative resource reconciliation is paused.
	switch mapiMachineSet.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// MachineAPI is currently authoritative, check its generation against the synchronized one.
		return mapiMachineSet.Status.SynchronizedGeneration == mapiMachineSet.Generation, nil
	case machinev1beta1.MachineAuthorityMachineAPI:
		// ClusterAPI is currently authoritative, check its generation against the synchronized one.
		// TODO: InfraMachineTemplate.
		capiMachineSet := &capiv1beta1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
			return false, fmt.Errorf("failed to get CAPI machine set: %w", err)
		}

		return mapiMachineSet.Status.SynchronizedGeneration == capiMachineSet.Generation, nil
	default:
		// Any other value is disallowed by the openAPI schema validation.
	}

	return false, nil
}

// applyStatusAuthoritativeAPIWithPatch updates the authoritativeAPI field
// using a server side apply patch. We do this to force ownership of the field.
func (r *MachineSetMigrationReconciler) applyStatusAuthoritativeAPIWithPatch(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) error {
	statusAc := machinev1applyconfigs.MachineSetStatus().WithAuthoritativeAPI(authority)
	msAc := machinev1applyconfigs.MachineSet(mapiMachineSet.GetName(), mapiMachineSet.GetNamespace()).
		WithStatus(statusAc)

	if err := r.Status().Patch(ctx, mapiMachineSet, util.ApplyConfigPatch(msAc), client.ForceOwnership, client.FieldOwner(controllerName+"-AuthoritativeAPI")); err != nil {
		return fmt.Errorf("failed to patch MAPI machine set status with authoritativeAPI: %w", err)
	}

	return nil
}

// ensureFinalizerOnNewAuthoritativeAPI adds a finalizer to the resource if required.
// If the finalizer already exists, this function should be a no-op.
// If the finalizer is added, the function will return true so that the reconciler can requeue the object.
// Adding the finalizer in a separate reconcile ensures that spec updates are separate from status updates.
func (r *MachineSetMigrationReconciler) ensureFinalizerOnNewAuthoritativeAPI(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet) (bool, error) {
	var newAuthoritativeResource client.Object
	switch mapiMachineSet.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// TODO: InfraMachineTemplate.
		capiMachineSet := &capiv1beta1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
			return false, fmt.Errorf("failed to get CAPI machine set: %w", err)
		}

		newAuthoritativeResource = capiMachineSet
	case machinev1beta1.MachineAuthorityMachineAPI:
		newAuthoritativeResource = mapiMachineSet
	default:
		// Any other value is disallowed by the openAPI schema validation.
	}

	// Check if we need to add the finalizer.
	for _, finalizer := range newAuthoritativeResource.GetFinalizers() {
		if finalizer == migrationControllerFinalizer {
			return false, nil
		}
	}

	newAuthoritativeResource.SetFinalizers(append(newAuthoritativeResource.GetFinalizers(), migrationControllerFinalizer))

	if err := r.Update(ctx, newAuthoritativeResource); err != nil {
		return false, fmt.Errorf("error updating authoritativeAPI resource: %w", err)
	}

	return true, nil
}

// ensureFinalizerRemovedOnOldAuthoritativeAPI removes the finalizer to the resource if required.
// If the finalizer is not present, this function should be a no-op.
// If the finalizer is present, the function will return true once removed so that the reconciler can requeue the object.
// Removing the finalizer in a separate reconcile ensures that spec updates are separate from status updates.
func (r *MachineSetMigrationReconciler) ensureFinalizerRemovedOnOldAuthoritativeAPI(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet) (bool, error) {
	var oldAuthoritativeResource client.Object
	switch mapiMachineSet.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// TODO: InfraMachineTemplate.
		capiMachineSet := &capiv1beta1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
			return false, fmt.Errorf("failed to get CAPI machine set: %w", err)
		}

		oldAuthoritativeResource = capiMachineSet.DeepCopy()
	case machinev1beta1.MachineAuthorityMachineAPI:
		oldAuthoritativeResource = mapiMachineSet.DeepCopy()
	default:
		// Any other value is disallowed by the openAPI schema validation.
	}

	// Remove finalizer from the old authoritative API.
	if finalizerUpdated := controllerutil.RemoveFinalizer(oldAuthoritativeResource, migrationControllerFinalizer); finalizerUpdated {
		if err := r.Update(ctx, oldAuthoritativeResource); err != nil {
			return false, fmt.Errorf("failed to update old authoritativeAPI resource: %w", err)
		}
	}

	return true, nil
}

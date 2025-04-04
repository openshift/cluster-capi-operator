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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/annotations"
	conditionsv1beta2 "sigs.k8s.io/cluster-api/util/conditions/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const controllerName = "MachineSetMigrationController"

// MachineSetMigrationReconciler reconciles MachineSet resources for migration.
type MachineSetMigrationReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Recorder      record.EventRecorder
	CAPINamespace string
	MAPINamespace string
}

// SetupWithManager sets up the MachineSetMigration controller.
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

	r.Client = mgr.GetClient()
	r.Scheme = mgr.GetScheme()
	r.Recorder = mgr.GetEventRecorderFor(controllerName)

	return nil
}

// Reconcile performs the reconciliation for a MachineSet.
func (r *MachineSetMigrationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	logger.V(1).Info("Reconciling machine set")
	defer logger.V(1).Info("Finished reconciling machine set")

	mapiMachineSet := &machinev1beta1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: req.Name}, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get MAPI machine set: %w", err)
	}

	logger.WithValues("machine set", mapiMachineSet.Name)

	if mapiMachineSet.Spec.AuthoritativeAPI == mapiMachineSet.Status.AuthoritativeAPI {
		// No migration is being requested for this resource, nothing to do.
		return ctrl.Result{}, nil
	}

	// If authoritativeAPI status is empty, it means it is the first time we see this resource.
	// Set the status.authoritativeAPI to match the spec.authoritativeAPI.
	if mapiMachineSet.Status.AuthoritativeAPI == "" {
		if err := r.applyStatusAuthoritativeAPIWithPatch(ctx, mapiMachineSet, mapiMachineSet.Spec.AuthoritativeAPI); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
		}

		// Check again later.
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Check if the Synchronized condition is set to True.
	// If it is not, this indicates an unmigratable resource and therefore should take no action.
	if cond, err := util.GetConditionStatus(mapiMachineSet, string(consts.SynchronizedCondition)); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to get synchronizedCondition for %s/%s: %w", mapiMachineSet.Namespace, mapiMachineSet.Name, err)
	} else if cond != corev1.ConditionTrue {
		logger.Info("New machine set not yet in sync with the old authoritative one, will retry later")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	logger.Info("Detected migration request for machine set")

	// Make sure the authoritativeAPI resource status is set to migrating.
	if mapiMachineSet.Status.AuthoritativeAPI != machinev1beta1.MachineAuthorityMigrating {
		if err := r.applyStatusAuthoritativeAPIWithPatch(ctx, mapiMachineSet, machinev1beta1.MachineAuthorityMigrating); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to set authoritativeAPI %q to status: %w", machinev1beta1.MachineAuthorityMigrating, err)
		}
		// Then wait for it to take effect.
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	logger.Info("Acknowledged migration request for machine set")

	// Request pausing on the old authoritative resource.
	if updated, err := r.requestOldAuthoritativeResourcePaused(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to request pause on old authoritative machine set: %w", err)
	} else if updated {
		// Wait for it to take effect.
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	logger.Info("Requested pausing for old authoritative machine set")

	// Check that the old authoritative resource is paused.
	if paused, err := r.isOldAuthoritativeResourcePaused(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check paused on old authoritative machine set: %w", err)
	} else if !paused {
		// The old Authoritative API resource is not paused yet, requeue to check later.
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	logger.Info("Old authoritative machine set is now paused")

	// Check if the synchronizedGeneration matches the old authoritativeAPI's resource current generation.
	if isSynchronizedGenMatchingOldAuthority, err := r.isSynchronizedGenerationMatchingOldAuthoritativeAPIGeneration(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to check synchronizedGeneration matches old authority: %w", err)
	} else if !isSynchronizedGenMatchingOldAuthority {
		// The to-be Authoritative API resource is not fully synced up yet, requeue to check later.
		logger.Info("To-be authoritative and old machine set are not synced yet, will retry later")

		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	logger.Info("New machine set is now in sync with the old authoritative one")

	// Add finalizer to new authoritative API, this ensures no status changes on the same reconcile.
	if added, err := r.ensureFinalizerOnNewAuthoritativeResource(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure finalizer on resource: %w", err)
	} else if added {
		// Wait for it to take effect.
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Remove finalizer from the old authoritative API, this ensures no status changes on the same reconcile.
	if removed, err := r.ensureNoFinalizerOnOldAuthoritativeResource(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from old resource: %w", err)
	} else if removed {
		// Wait for it to take effect.
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	logger.Info("Switching authoritativeAPI for machine set")

	// Set the actual AuthoritativeAPI to the desired one, reset the synchronized generation and condition.
	if err := r.setNewAuthoritativeAPIAndResetSynchronized(ctx, mapiMachineSet, mapiMachineSet.Spec.AuthoritativeAPI); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
	}

	logger.Info("Successfully switched authoritativeAPI for machine set")

	// Make sure the new authoritative resource has been requested to unpause.
	if err := r.ensureUnpauseRequestedOnNewAuthoritativeResource(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure the new AuthoritativeAPI has been un-paused: %w", err)
	}

	logger.Info("Successfully unpaused new authoritative machine set")
	logger.Info("Migration completed for machine set")

	return ctrl.Result{}, nil
}

// isOldAuthoritativeResourcePaused checks whether the old authoritative resource is paused.
func (r *MachineSetMigrationReconciler) isOldAuthoritativeResourcePaused(ctx context.Context, ms *machinev1beta1.MachineSet) (bool, error) {
	if ms.Spec.AuthoritativeAPI == machinev1beta1.MachineAuthorityClusterAPI {
		cond, err := util.GetConditionStatus(ms, "Paused")
		if err != nil {
			return false, fmt.Errorf("unable to get paused condition for %s/%s: %w", ms.Namespace, ms.Name, err)
		}

		return cond == corev1.ConditionTrue, nil
	}

	// For MachineAuthorityMachineAPI, check the corresponding CAPI resource.
	capiMachineSet := &capiv1beta1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: ms.Name}, capiMachineSet); err != nil {
		return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
	}

	machinePausedCondition := conditionsv1beta2.Get(capiMachineSet, capiv1beta1.PausedV1Beta2Condition)
	if machinePausedCondition == nil {
		return false, nil
	}

	// InfraMachineTemplate doesn't have a reconciler and thus it doesn't need pausing.
	// The only provider we are interested in, that reconciles the inframachinetemplate, is ibmcloud/powervs
	// which only updates its status
	// see: https://github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/blob/main/controllers/ibmpowervsmachinetemplate_controller.go
	return (machinePausedCondition.Status == metav1.ConditionTrue), nil
}

func (r *MachineSetMigrationReconciler) ensureUnpauseRequestedOnNewAuthoritativeResource(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet) error {
	// Request that the new authoritative resource reconciliation is un-paused.
	//nolint:wsl,exhaustive
	switch mapiMachineSet.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// For requesting unpausing of a CAPI resource, remove the paused annotation on it.
		// So check if the ClusterAPI resource has the paused annotation and if so remove it.
		capiMachineSet := &capiv1beta1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
			return fmt.Errorf("failed to get Cluster API machine set: %w", err)
		}

		if annotations.HasPaused(capiMachineSet) {
			capiMachineSetCopy := capiMachineSet.DeepCopy()
			delete(capiMachineSet.Annotations, capiv1beta1.PausedAnnotation)

			if err := r.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSetCopy)); err != nil {
				return fmt.Errorf("failed to patch Cluster API machine set: %w", err)
			}
		}

		// InfraMachineTemplate doesn't have a reconciler and thus it doesn't need pausing.
		// The only provider we are interested in, that reconciles the inframachinetemplate, is ibmcloud/powervs
		// which only updates its status
		// see: https://github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/blob/main/controllers/ibmpowervsmachinetemplate_controller.go
	case machinev1beta1.MachineAuthorityMachineAPI:
		// For requesting unpausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller. Nothing to do here.
	default:
		// Any other value is disallowed by the openAPI schema validation.
	}

	return nil
}

// requestOldAuthoritativeResourcePaused requests the old authoritative resource is paused.
func (r *MachineSetMigrationReconciler) requestOldAuthoritativeResourcePaused(ctx context.Context, ms *machinev1beta1.MachineSet) (bool, error) {
	// Request that the old authoritative resource reconciliation is paused.
	updated := false
	//nolint:wsl,exhaustive
	switch ms.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// For requesting pausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller.
	case machinev1beta1.MachineAuthorityMachineAPI:
		// For requesting pausing of a CAPI resource, set the paused annotation on it.
		// The spec.AuthoritativeAPI is set to MachineAPI, meaning that the old authoritativeAPI was ClusterAPI.
		// So Check if the ClusterAPI resource has the paused annotation, otherwise set it.
		capiMachineSet := &capiv1beta1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: ms.Name}, capiMachineSet); err != nil {
			return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
		}

		if !annotations.HasPaused(capiMachineSet) {
			capiMachineSetCopy := capiMachineSet.DeepCopy()
			annotations.AddAnnotations(capiMachineSet, map[string]string{capiv1beta1.PausedAnnotation: ""})
			if err := r.Patch(ctx, capiMachineSet, client.MergeFrom(capiMachineSetCopy)); err != nil {
				return false, fmt.Errorf("failed to patch Cluster API machine set: %w", err)
			}

			updated = true
		}

		// InfraMachineTemplate doesn't have a reconciler and thus it doesn't need pausing.
		// The only provider we are interested in, that reconciles the inframachinetemplate, is ibmcloud/powervs
		// which only updates its status
		// see: https://github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/blob/main/controllers/ibmpowervsmachinetemplate_controller.go
	default:
		// Any other value is disallowed by the openAPI schema validation.
	}

	return updated, nil
}

func (r *MachineSetMigrationReconciler) isSynchronizedGenerationMatchingOldAuthoritativeAPIGeneration(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet) (bool, error) {
	//nolint:wsl,exhaustive
	switch mapiMachineSet.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// ClusterAPI is the desired authoritative API but
		// MachineAPI is currently still the authoritative one.
		// Check MachineAPI generation against the synchronized one.
		return mapiMachineSet.Status.SynchronizedGeneration == mapiMachineSet.Generation, nil
	case machinev1beta1.MachineAuthorityMachineAPI:
		// MachineAPI is the desired authoritative API but
		// ClusterAPI is currently still the authoritative one.
		// Check ClusterAPI generation against the synchronized one.
		capiMachineSet := &capiv1beta1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
			return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
		}

		// Given the CAPI infra machine template is immutable
		// we do not check for its generation to be synced up with the generation of the MAPI machine set.
		return (mapiMachineSet.Status.SynchronizedGeneration == capiMachineSet.Generation), nil
	default:
		// Any other value is disallowed by the openAPI schema validation.
	}

	return false, nil
}

// applyStatusAuthoritativeAPIWithPatch updates the resource status.authoritativeAPI using a server-side apply patch.
func (r *MachineSetMigrationReconciler) applyStatusAuthoritativeAPIWithPatch(ctx context.Context, ms *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) error {
	logger := log.FromContext(ctx)
	logger.Info(fmt.Sprintf("Setting AuthoritativeAPI status to %q for resource", authority))

	ac := machinev1applyconfigs.MachineSet(ms.Name, ms.Namespace).
		WithStatus(machinev1applyconfigs.MachineSetStatus().WithAuthoritativeAPI(authority))

	if err := r.Status().Patch(ctx, ms, util.ApplyConfigPatch(ac), client.ForceOwnership, client.FieldOwner(controllerName+"-AuthoritativeAPI")); err != nil {
		return fmt.Errorf("failed to patch Machine API machine set status with authoritativeAPI %q: %w", authority, err)
	}

	return nil
}

// setNewAuthoritativeAPIAndResetSynchronized updates the MachineSet status to the new authoritativeAPI,
// resets the synchronized generation, and removes the Synchronized condition.
func (r *MachineSetMigrationReconciler) setNewAuthoritativeAPIAndResetSynchronized(ctx context.Context, ms *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) error {
	var newConditions []*machinev1applyconfigs.ConditionApplyConfiguration

	for _, cond := range ms.Status.Conditions {
		if cond.Type != consts.SynchronizedCondition {
			newConditions = append(newConditions, &machinev1applyconfigs.ConditionApplyConfiguration{
				LastTransitionTime: &cond.LastTransitionTime,
				Message:            &cond.Message,
				Reason:             &cond.Reason,
				Status:             &cond.Status,
				Severity:           &cond.Severity,
				Type:               &cond.Type,
			})
		}
	}

	statusAc := machinev1applyconfigs.MachineSetStatus().
		WithAuthoritativeAPI(authority).
		WithConditions(newConditions...).
		WithSynchronizedGeneration(0)

	ac := machinev1applyconfigs.MachineSet(ms.Name, ms.Namespace).WithStatus(statusAc)

	if err := r.Status().Patch(ctx, ms, util.ApplyConfigPatch(ac), client.ForceOwnership, client.FieldOwner(controllerName+"-AuthoritativeAPI")); err != nil {
		return fmt.Errorf("failed to patch Machine API machine set status with authoritativeAPI %q: %w", authority, err)
	}

	return nil
}

// ensureFinalizerOnNewAuthoritativeResource adds a finalizer to the resource if required.
// If the finalizer already exists, this function should be a no-op.
// If the finalizer is added, the function will return true so that the reconciler can requeue the object.
// Adding the finalizer in a separate reconcile ensures that spec updates are separate from status updates.
func (r *MachineSetMigrationReconciler) ensureFinalizerOnNewAuthoritativeResource(ctx context.Context, ms *machinev1beta1.MachineSet) (bool, error) {
	if ms.Spec.AuthoritativeAPI != machinev1beta1.MachineAuthorityClusterAPI {
		return false, nil
	}

	capiMachineSet := &capiv1beta1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: ms.Name}, capiMachineSet); err != nil {
		return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
	}

	updated, err := util.EnsureFinalizer(ctx, r.Client, capiMachineSet, capiv1beta1.MachineSetFinalizer)
	if err != nil {
		return false, fmt.Errorf("failed to ensure finalizer on Cluster API machine set: %w", err)
	}

	return updated, nil
}

// ensureNoFinalizerOnOldAuthoritativeResource removes the finalizer from the resource if required.
// If the finalizer doesn't exists, this function should be a no-op.
// If the finalizer gets removed, the function will return true so that the reconciler can requeue the object.
func (r *MachineSetMigrationReconciler) ensureNoFinalizerOnOldAuthoritativeResource(ctx context.Context, ms *machinev1beta1.MachineSet) (bool, error) {
	if ms.Spec.AuthoritativeAPI == machinev1beta1.MachineAuthorityClusterAPI {
		return false, nil
	}

	// TODO: uncomment this, at the moment there is a bug in the cluster-api controllers that
	// set the finalizer before checking for the paused condition, as such the finalizer is always
	// repopulted even though the controller is paused.
	// ref: https://github.com/kubernetes-sigs/cluster-api/blob/c70dca0fc387b44457c69b71a719132a0d9bed58/internal/controllers/machine/machine_controller.go#L207-L210
	// capiMachineSet := &capiv1beta1.MachineSet{}
	// if err := r.Get(ctx, client.ObjectKey{Namespace: consts.DefaultManagedNamespace, Name: ms.Name}, capiMachineSet); err != nil {
	// 	return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
	// }

	// updated, err := util.RemoveFinalizer(ctx, r.Client, capiMachineSet, capiv1beta1.MachineSetFinalizer)
	// if err != nil {
	// 	return false, fmt.Errorf("failed to remove finalizer from Cluster API machine set: %w", err)
	// }
	updated := false

	return updated, nil
}

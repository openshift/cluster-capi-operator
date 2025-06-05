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

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	"sigs.k8s.io/cluster-api/util/annotations"
	conditionsv1beta2 "sigs.k8s.io/cluster-api/util/conditions/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
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
	CAPINamespace string
	MAPINamespace string
}

// SetupWithManager sets up the MachineSetMigration controller.
func (r *MachineSetMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	infraMachineTemplate, _, err := controllers.InitInfraMachineTemplateAndInfraClusterFromProvider(r.Platform)
	if err != nil {
		return fmt.Errorf("failed to get infrastructure machine template from Provider: %w", err)
	}

	// Allow the namespaces to be set externally for test purposes, when not set,
	// default to the production namespaces.
	if r.CAPINamespace == "" {
		r.CAPINamespace = controllers.DefaultManagedNamespace
	}

	if r.MAPINamespace == "" {
		r.MAPINamespace = controllers.DefaultMAPIManagedNamespace
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&machinev1beta1.MachineSet{}, builder.WithPredicates(util.FilterNamespace(r.MAPINamespace))).
		Watches(
			&clusterv1.MachineSet{},
			handler.EnqueueRequestsFromMapFunc(util.RewriteNamespace(r.MAPINamespace)),
			builder.WithPredicates(util.FilterNamespace(r.CAPINamespace)),
		).
		Watches(
			infraMachineTemplate,
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
	logger := log.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	logger.V(1).Info("Reconciling machine set")
	defer logger.V(1).Info("Finished reconciling machine set")

	mapiMachineSet := &machinev1beta1.MachineSet{}
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

	// If authoritativeAPI status is empty, it means it is the first time we see this resource.
	// Set the status.authoritativeAPI to match the spec.authoritativeAPI.
	if mapiMachineSet.Status.AuthoritativeAPI == "" {
		if err := r.applyStatusAuthoritativeAPIWithPatch(ctx, mapiMachineSet, mapiMachineSet.Spec.AuthoritativeAPI); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
		}

		// Wait for the patching to take effect.
		return ctrl.Result{}, nil
	}

	// Check if the Synchronized condition is set to True.
	// If it is not, this indicates an unmigratable resource and therefore should take no action.
	if cond, err := util.GetConditionStatus(mapiMachineSet, string(controllers.SynchronizedCondition)); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to get synchronizedCondition for %s/%s: %w", mapiMachineSet.Namespace, mapiMachineSet.Name, err)
	} else if cond != corev1.ConditionTrue {
		logger.Info("Machine set not synchronized with latest authoritative generation, will retry later")

		return ctrl.Result{}, nil
	}

	// Make sure the authoritativeAPI resource status is set to migrating.
	if mapiMachineSet.Status.AuthoritativeAPI != machinev1beta1.MachineAuthorityMigrating {
		logger.Info("Detected migration request for machine set")

		if err := r.applyStatusAuthoritativeAPIWithPatch(ctx, mapiMachineSet, machinev1beta1.MachineAuthorityMigrating); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to set authoritativeAPI %q to status: %w", machinev1beta1.MachineAuthorityMigrating, err)
		}

		logger.Info("Acknowledged migration request for machine set")

		// Wait for the change to propagate.
		return ctrl.Result{}, nil
	}

	// Request pausing on the authoritative resource.
	if updated, err := r.requestOldAuthoritativeResourcePaused(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to request pause on authoritative machine set: %w", err)
	} else if updated {
		logger.Info("Requested pausing for authoritative machine set")

		// Wait for the change to propagate.
		return ctrl.Result{}, nil
	}

	// Check that the old authoritative resource is paused.
	if paused, err := r.isOldAuthoritativeResourcePaused(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check paused on old authoritative machine set: %w", err)
	} else if !paused {
		// The Authoritative API resource is not paused yet, requeue to check later.
		logger.Info("Authoritative machine set is not paused yet, will retry later")

		return ctrl.Result{}, nil
	}

	// Check if the synchronizedGeneration matches the old authoritativeAPI's resource current generation.
	if isSynchronizedGenMatchingOldAuthority, err := r.isSynchronizedGenerationMatchingOldAuthoritativeAPIGeneration(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to check synchronizedGeneration matches old authority: %w", err)
	} else if !isSynchronizedGenMatchingOldAuthority {
		// The Authoritative API resource is not fully synced up yet, requeue to check later.
		logger.Info("Authoritative machine set and its copy are not synchronized yet, will retry later")

		return ctrl.Result{}, nil
	}

	// Make sure the new authoritative resource has been requested to unpause.
	if err := r.ensureUnpauseRequestedOnNewAuthoritativeResource(ctx, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure the new AuthoritativeAPI has been un-paused: %w", err)
	}

	// Set the actual AuthoritativeAPI to the desired one, reset the synchronized generation and condition.
	if err := r.setNewAuthoritativeAPIAndResetSynchronized(ctx, mapiMachineSet, mapiMachineSet.Spec.AuthoritativeAPI); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
	}

	logger.Info("Machine set authority switch has now been completed and the resource unpaused")
	logger.Info("Machine set migrated successfully")

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
	capiMachineSet := &clusterv1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: ms.Name}, capiMachineSet); err != nil {
		return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
	}

	machinePausedCondition := conditionsv1beta2.Get(capiMachineSet, clusterv1.PausedV1Beta2Condition)
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
	//nolint:wsl
	switch mapiMachineSet.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
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
	case machinev1beta1.MachineAuthorityMachineAPI:
		// For requesting unpausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller. Nothing to do here.
	case machinev1beta1.MachineAuthorityMigrating:
		// Value is disallowed by the openAPI schema validation.
	}

	return nil
}

// requestOldAuthoritativeResourcePaused requests the old authoritative resource is paused.
func (r *MachineSetMigrationReconciler) requestOldAuthoritativeResourcePaused(ctx context.Context, ms *machinev1beta1.MachineSet) (bool, error) {
	// Request that the old authoritative resource reconciliation is paused.
	updated := false
	//nolint:wsl
	switch ms.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// For requesting pausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller.
	case machinev1beta1.MachineAuthorityMachineAPI:
		// For requesting pausing of a CAPI resource, set the paused annotation on it.
		// The spec.AuthoritativeAPI is set to MachineAPI, meaning that the old authoritativeAPI was ClusterAPI.
		// So Check if the ClusterAPI resource has the paused annotation, otherwise set it.
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
	case machinev1beta1.MachineAuthorityMigrating:
		// Value is disallowed by the openAPI schema validation.
	}

	return updated, nil
}

func (r *MachineSetMigrationReconciler) isSynchronizedGenerationMatchingOldAuthoritativeAPIGeneration(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet) (bool, error) {
	//nolint:wsl
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
		capiMachineSet := &clusterv1.MachineSet{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: mapiMachineSet.Name}, capiMachineSet); err != nil {
			return false, fmt.Errorf("failed to get Cluster API machine set: %w", err)
		}

		// Given the CAPI infra machine template is immutable
		// we do not check for its generation to be synced up with the generation of the MAPI machine set.
		return (mapiMachineSet.Status.SynchronizedGeneration == capiMachineSet.Generation), nil
	case machinev1beta1.MachineAuthorityMigrating:
		// Value is disallowed by the openAPI schema validation.
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
		if cond.Type != controllers.SynchronizedCondition {
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

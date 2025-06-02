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
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
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

const controllerName = "MachineMigrationController"

// MachineMigrationReconciler reconciles Machine resources for migration.
type MachineMigrationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Infra         *configv1.Infrastructure
	Platform      configv1.PlatformType
	CAPINamespace string
	MAPINamespace string
}

// SetupWithManager sets up the MachineMigration controller.
func (r *MachineMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	infraMachine, _, err := controllers.InitInfraMachineAndInfraClusterFromProvider(r.Platform)
	if err != nil {
		return fmt.Errorf("failed to get infrastructure machine from Provider: %w", err)
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
		For(&machinev1beta1.Machine{}, builder.WithPredicates(util.FilterNamespace(r.MAPINamespace))).
		Watches(
			&capiv1beta1.Machine{},
			handler.EnqueueRequestsFromMapFunc(util.RewriteNamespace(r.MAPINamespace)),
			builder.WithPredicates(util.FilterNamespace(r.CAPINamespace)),
		).
		Watches(
			infraMachine,
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
//nolint:funlen
func (r *MachineMigrationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	logger.V(1).Info("Reconciling machine")
	defer logger.V(1).Info("Finished reconciling machine")

	mapiMachine := &machinev1beta1.Machine{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: req.Name}, mapiMachine); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get MAPI machine: %w", err)
	} else if apierrors.IsNotFound(err) {
		logger.Info("Machine has been deleted. Migration not required")
		return ctrl.Result{}, nil
	}

	if mapiMachine.Spec.AuthoritativeAPI == mapiMachine.Status.AuthoritativeAPI {
		// No migration is being requested for this resource, nothing to do.
		return ctrl.Result{}, nil
	}

	// Check if the Synchronized condition is set to True.
	// If it is not, this indicates an unmigratable resource and therefore should take no action.
	if cond, err := util.GetConditionStatus(mapiMachine, string(controllers.SynchronizedCondition)); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to get synchronizedCondition for %s/%s: %w", mapiMachine.Namespace, mapiMachine.Name, err)
	} else if cond != corev1.ConditionTrue {
		logger.Info("Machine not synchronized with latest authoritative generation, will retry later")

		return ctrl.Result{}, nil
	}

	// Make sure the authoritativeAPI resource status is set to migrating.
	if mapiMachine.Status.AuthoritativeAPI != machinev1beta1.MachineAuthorityMigrating {
		logger.Info("Detected migration request for machine")

		if err := r.applyStatusAuthoritativeAPIWithPatch(ctx, mapiMachine, machinev1beta1.MachineAuthorityMigrating); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to set authoritativeAPI %q to status: %w", machinev1beta1.MachineAuthorityMigrating, err)
		}

		logger.Info("Acknowledged migration request for machine")

		// Wait for the change to propagate.
		return ctrl.Result{}, nil
	}

	// Request pausing on the authoritative resource.
	if updated, err := r.requestOldAuthoritativeResourcePaused(ctx, mapiMachine); err != nil {
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
	if paused, err := r.isOldAuthoritativeResourcePaused(ctx, mapiMachine); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check paused on authoritative machine: %w", err)
	} else if !paused {
		// The Authoritative API resource is not paused yet, requeue to check later.
		logger.Info("Authoritative machine is not paused yet, will retry later")

		return ctrl.Result{}, nil
	}

	// Check if the synchronizedGeneration matches the old authoritativeAPI's resource current generation.
	if isSynchronizedGenMatchingOldAuthority, err := r.isSynchronizedGenerationMatchingOldAuthoritativeAPIGeneration(ctx, mapiMachine); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to check synchronizedGeneration matches old authority: %w", err)
	} else if !isSynchronizedGenMatchingOldAuthority {
		// The to-be Authoritative API resource is not fully synced up yet, requeue to check later.
		logger.Info("Authoritative machine and its copy are not synchronized yet, will retry later")

		return ctrl.Result{}, nil
	}

	// Set the actual AuthoritativeAPI to the desired one, reset the synchronized generation and condition.
	if err := r.setNewAuthoritativeAPIAndResetSynchronized(ctx, mapiMachine, mapiMachine.Spec.AuthoritativeAPI); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
	}

	// Make sure the new authoritative resource has been requested to unpause.
	if err := r.ensureUnpauseRequestedOnNewAuthoritativeResource(ctx, mapiMachine); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure the new AuthoritativeAPI has been un-paused: %w", err)
	}

	logger.Info("Machine authority switch has now been completed and the resource unpaused")
	logger.Info("Machine migrated successfully")

	return ctrl.Result{}, nil
}

// isOldAuthoritativeResourcePaused checks whether the old authoritative resource is paused.
func (r *MachineMigrationReconciler) isOldAuthoritativeResourcePaused(ctx context.Context, m *machinev1beta1.Machine) (bool, error) {
	if m.Spec.AuthoritativeAPI == machinev1beta1.MachineAuthorityClusterAPI {
		cond, err := util.GetConditionStatus(m, "Paused")
		if err != nil {
			return false, fmt.Errorf("unable to get paused condition for %s/%s: %w", m.Namespace, m.Name, err)
		}

		return cond == corev1.ConditionTrue, nil
	}

	// For MachineAuthorityMachineAPI, check the corresponding CAPI resource.
	capiMachine := &capiv1beta1.Machine{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: m.Name}, capiMachine); err != nil {
		return false, fmt.Errorf("failed to get Cluster API machine: %w", err)
	}

	machinePausedCondition := conditionsv1beta2.Get(capiMachine, capiv1beta1.PausedV1Beta2Condition)
	if machinePausedCondition == nil {
		return false, nil
	}

	infraMachineRef := capiMachine.Spec.InfrastructureRef

	infraMachine, err := util.GetReferencedObject(ctx, r.Client, r.Scheme, infraMachineRef)
	if err != nil {
		return false, fmt.Errorf("failed to get Cluster API infra machine: %w", err)
	}

	infraMachinePausedConditionStatus, err := util.GetConditionStatus(infraMachine, capiv1beta1.PausedV1Beta2Condition)
	if err != nil {
		return false, fmt.Errorf("unable to get paused condition for %s/%s: %w", infraMachine.GetNamespace(), infraMachine.GetName(), err)
	}

	return (machinePausedCondition.Status == metav1.ConditionTrue) && (infraMachinePausedConditionStatus == corev1.ConditionTrue), nil
}

func (r *MachineMigrationReconciler) ensureUnpauseRequestedOnNewAuthoritativeResource(ctx context.Context, mapiMachine *machinev1beta1.Machine) error {
	// Request that the new authoritative resource reconciliation is un-paused.
	//nolint:wsl
	switch mapiMachine.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// For requesting unpausing of a CAPI resource, remove the paused annotation on it.
		// So check if the ClusterAPI resource has the paused annotation and if so remove it.
		capiMachine := &capiv1beta1.Machine{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: mapiMachine.Name}, capiMachine); err != nil {
			return fmt.Errorf("failed to get Cluster API machine: %w", err)
		}

		infraMachineRef := capiMachine.Spec.InfrastructureRef

		infraMachine, err := util.GetReferencedObject(ctx, r.Client, r.Scheme, infraMachineRef)
		if err != nil {
			return fmt.Errorf("failed to get Cluster API infra machine: %w", err)
		}

		if annotations.HasPaused(capiMachine) {
			capiMachineCopy := capiMachine.DeepCopy()
			delete(capiMachine.Annotations, capiv1beta1.PausedAnnotation)

			if err := r.Patch(ctx, capiMachine, client.MergeFrom(capiMachineCopy)); err != nil {
				return fmt.Errorf("failed to patch Cluster API machine: %w", err)
			}
		}

		if annotations.HasPaused(infraMachine) {
			infraMachineCopy, ok := infraMachine.DeepCopyObject().(client.Object)
			if !ok {
				return fmt.Errorf("unable to assert Cluster API infra machine as client.Object: %w", err)
			}
			util.RemoveAnnotation(infraMachine, capiv1beta1.PausedAnnotation)

			if err := r.Patch(ctx, infraMachine, client.MergeFrom(infraMachineCopy)); err != nil {
				return fmt.Errorf("failed to patch Cluster API infra machine: %w", err)
			}
		}
	case machinev1beta1.MachineAuthorityMachineAPI:
		// For requesting unpausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller. Nothing to do here.
	case machinev1beta1.MachineAuthorityMigrating:
		// Value is disallowed by the openAPI schema validation.
	}

	return nil
}

// requestOldAuthoritativeResourcePaused requests the old authoritative resource is paused.
func (r *MachineMigrationReconciler) requestOldAuthoritativeResourcePaused(ctx context.Context, m *machinev1beta1.Machine) (bool, error) {
	// Request that the old authoritative resource reconciliation is paused.
	updated := false
	//nolint:wsl
	switch m.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// For requesting pausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller.
	case machinev1beta1.MachineAuthorityMachineAPI:
		// For requesting pausing of a CAPI resource, set the paused annotation on it.
		// The spec.AuthoritativeAPI is set to MachineAPI, meaning that the old authoritativeAPI was ClusterAPI.
		// So Check if the ClusterAPI resource has the paused annotation, otherwise set it.
		capiMachine := &capiv1beta1.Machine{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: m.Name}, capiMachine); err != nil {
			return false, fmt.Errorf("failed to get Cluster API machine: %w", err)
		}

		infraMachineRef := capiMachine.Spec.InfrastructureRef

		infraMachine, err := util.GetReferencedObject(ctx, r.Client, r.Scheme, infraMachineRef)
		if err != nil {
			return false, fmt.Errorf("failed to get Cluster API infra machine: %w", err)
		}

		if !annotations.HasPaused(capiMachine) {
			capiMachineCopy := capiMachine.DeepCopy()
			annotations.AddAnnotations(capiMachine, map[string]string{capiv1beta1.PausedAnnotation: ""})
			if err := r.Patch(ctx, capiMachine, client.MergeFrom(capiMachineCopy)); err != nil {
				return false, fmt.Errorf("failed to patch Cluster API machine: %w", err)
			}

			updated = true
		}

		if !annotations.HasPaused(infraMachine) {
			infraMachineCopy, ok := infraMachine.DeepCopyObject().(client.Object)
			if !ok {
				return false, fmt.Errorf("unable to assert Cluster API infra machine as client.Object: %w", err)
			}
			annotations.AddAnnotations(infraMachine, map[string]string{capiv1beta1.PausedAnnotation: ""})
			if err := r.Patch(ctx, infraMachine, client.MergeFrom(infraMachineCopy)); err != nil {
				return false, fmt.Errorf("failed to patch Cluster API infra machine: %w", err)
			}

			updated = true
		}
	case machinev1beta1.MachineAuthorityMigrating:
		// Value is disallowed by the openAPI schema validation.
	}

	return updated, nil
}

func (r *MachineMigrationReconciler) isSynchronizedGenerationMatchingOldAuthoritativeAPIGeneration(ctx context.Context, mapiMachine *machinev1beta1.Machine) (bool, error) {
	//nolint:wsl
	switch mapiMachine.Spec.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityClusterAPI:
		// ClusterAPI is the desired authoritative API but
		// MachineAPI is currently still the authoritative one.
		// Check MachineAPI generation against the synchronized one.
		return mapiMachine.Status.SynchronizedGeneration == mapiMachine.Generation, nil
	case machinev1beta1.MachineAuthorityMachineAPI:
		// MachineAPI is the desired authoritative API but
		// ClusterAPI is currently still the authoritative one.
		// Check ClusterAPI generation against the synchronized one.
		capiMachine := &capiv1beta1.Machine{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: mapiMachine.Name}, capiMachine); err != nil {
			return false, fmt.Errorf("failed to get Cluster API machine: %w", err)
		}

		// Do not check this for the CAPI infra machine.
		// As its ref is embedded in the capiMachine and they get updated together.
		return (mapiMachine.Status.SynchronizedGeneration == capiMachine.Generation), nil
	case machinev1beta1.MachineAuthorityMigrating:
		// Value is disallowed by the openAPI schema validation.
	}

	return false, nil
}

// applyStatusAuthoritativeAPIWithPatch updates the resource status.authoritativeAPI using a server-side apply patch.
func (r *MachineMigrationReconciler) applyStatusAuthoritativeAPIWithPatch(ctx context.Context, m *machinev1beta1.Machine, authority machinev1beta1.MachineAuthority) error {
	logger := log.FromContext(ctx)
	logger.Info(fmt.Sprintf("Setting AuthoritativeAPI status to %q for resource", authority))

	ac := machinev1applyconfigs.Machine(m.Name, m.Namespace).
		WithStatus(machinev1applyconfigs.MachineStatus().WithAuthoritativeAPI(authority))

	if err := r.Status().Patch(ctx, m, util.ApplyConfigPatch(ac), client.ForceOwnership, client.FieldOwner(controllerName+"-AuthoritativeAPI")); err != nil {
		return fmt.Errorf("failed to patch Machine API machine status with authoritativeAPI %q: %w", authority, err)
	}

	return nil
}

// setNewAuthoritativeAPIAndResetSynchronized updates the Machine status to the new authoritativeAPI,
// resets the synchronized generation, and removes the Synchronized condition.
func (r *MachineMigrationReconciler) setNewAuthoritativeAPIAndResetSynchronized(ctx context.Context, m *machinev1beta1.Machine, authority machinev1beta1.MachineAuthority) error {
	var newConditions []*machinev1applyconfigs.ConditionApplyConfiguration

	for _, cond := range m.Status.Conditions {
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

	statusAc := machinev1applyconfigs.MachineStatus().
		WithAuthoritativeAPI(authority).
		WithConditions(newConditions...).
		WithSynchronizedGeneration(0)

	ac := machinev1applyconfigs.Machine(m.Name, m.Namespace).WithStatus(statusAc)

	if err := r.Status().Patch(ctx, m, util.ApplyConfigPatch(ac), client.ForceOwnership, client.FieldOwner(controllerName+"-AuthoritativeAPI")); err != nil {
		return fmt.Errorf("failed to patch Machine API machine status with authoritativeAPI %q: %w", authority, err)
	}

	return nil
}

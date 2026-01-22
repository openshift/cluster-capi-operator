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
	logger := logf.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	logger.V(1).Info("Reconciling machine")
	defer logger.V(1).Info("Finished reconciling machine")

	mapiMachine := &mapiv1beta1.Machine{}
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

	// If authoritativeAPI status is empty, it means it is the first time we see this resource.
	// Set the status.authoritativeAPI to match the spec.authoritativeAPI.
	//
	// N.B. Very similar logic is also present in the Machine API machine/machineset controllers
	// to cover for the cases when the migration controller is not running (e.g. on not yet supported platforms),
	// as such if any change is done to this logic, please consider changing it also there. See:
	// https://github.com/openshift/machine-api-operator/pull/1386/files#diff-8a4a734efbb8fef769f9f6ba5d30d94f19433a0b1eaeb1be4f2a55aa226c3b3dR180-R197
	if mapiMachine.Status.AuthoritativeAPI == "" {
		if err := r.applyStatusAuthoritativeAPIWithPatch(ctx, mapiMachine, mapiMachine.Spec.AuthoritativeAPI); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
		}

		// Wait for the patching to take effect.
		return ctrl.Result{}, nil
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
	if mapiMachine.Status.AuthoritativeAPI != mapiv1beta1.MachineAuthorityMigrating {
		logger.Info("Detected migration request for machine")

		if err := r.applyStatusAuthoritativeAPIWithPatch(ctx, mapiMachine, mapiv1beta1.MachineAuthorityMigrating); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to set authoritativeAPI %q to status: %w", mapiv1beta1.MachineAuthorityMigrating, err)
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

	// Make sure the new authoritative resource has been requested to unpause.
	if err := r.ensureUnpauseRequestedOnNewAuthoritativeResource(ctx, mapiMachine); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure the new AuthoritativeAPI has been un-paused: %w", err)
	}

	// Set the actual AuthoritativeAPI to the desired one, reset the synchronized generation and condition.
	if err := synccommon.ApplyAuthoritativeAPIAndResetSyncStatus[*machinev1applyconfigs.MachineStatusApplyConfiguration](ctx, r.Client, controllerName, machinev1applyconfigs.Machine, mapiMachine, mapiMachine.Spec.AuthoritativeAPI); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply authoritativeAPI and reset sync status: %w", err)
	}

	logger.Info("Machine authority switch has now been completed and the resource unpaused")
	logger.Info("Machine migrated successfully")

	return ctrl.Result{}, nil
}

// isOldAuthoritativeResourcePaused checks whether the old authoritative resource is paused.
func (r *MachineMigrationReconciler) isOldAuthoritativeResourcePaused(ctx context.Context, m *mapiv1beta1.Machine) (bool, error) {
	if m.Spec.AuthoritativeAPI == mapiv1beta1.MachineAuthorityClusterAPI {
		cond, err := util.GetConditionStatus(m, "Paused")
		if err != nil {
			return false, fmt.Errorf("unable to get paused condition for %s/%s: %w", m.Namespace, m.Name, err)
		}

		return cond == corev1.ConditionTrue, nil
	}

	// For MachineAuthorityMachineAPI, check the corresponding CAPI resource.
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

func (r *MachineMigrationReconciler) ensureUnpauseRequestedOnNewAuthoritativeResource(ctx context.Context, mapiMachine *mapiv1beta1.Machine) error {
	// Request that the new authoritative resource reconciliation is un-paused.
	//nolint:wsl
	switch mapiMachine.Spec.AuthoritativeAPI {
	case mapiv1beta1.MachineAuthorityClusterAPI:
		// For requesting unpausing of a CAPI resource, remove the paused annotation on it.
		// So check if the ClusterAPI resource has the paused annotation and if so remove it.
		capiMachine := &clusterv1.Machine{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: mapiMachine.Name}, capiMachine); err != nil {
			return fmt.Errorf("failed to get Cluster API machine: %w", err)
		}

		infraMachineRef := capiMachine.Spec.InfrastructureRef

		infraMachine, err := external.GetObjectFromContractVersionedRef(ctx, r.Client, infraMachineRef, capiMachine.Namespace)
		if err != nil {
			return fmt.Errorf("failed to get Cluster API infra machine: %w", err)
		}

		if annotations.HasPaused(capiMachine) {
			capiMachineCopy := capiMachine.DeepCopy()
			delete(capiMachine.Annotations, clusterv1.PausedAnnotation)

			if err := r.Patch(ctx, capiMachine, client.MergeFrom(capiMachineCopy)); err != nil {
				return fmt.Errorf("failed to patch Cluster API machine: %w", err)
			}
		}

		if annotations.HasPaused(infraMachine) {
			infraMachineCopy, ok := infraMachine.DeepCopyObject().(client.Object)
			if !ok {
				return fmt.Errorf("unable to assert Cluster API infra machine as client.Object: %w", err)
			}
			util.RemoveAnnotation(infraMachine, clusterv1.PausedAnnotation)

			if err := r.Patch(ctx, infraMachine, client.MergeFrom(infraMachineCopy)); err != nil {
				return fmt.Errorf("failed to patch Cluster API infra machine: %w", err)
			}
		}
	case mapiv1beta1.MachineAuthorityMachineAPI:
		// For requesting unpausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller. Nothing to do here.
	case mapiv1beta1.MachineAuthorityMigrating:
		// Value is disallowed by the openAPI schema validation.
	}

	return nil
}

// requestOldAuthoritativeResourcePaused requests the old authoritative resource is paused.
func (r *MachineMigrationReconciler) requestOldAuthoritativeResourcePaused(ctx context.Context, m *mapiv1beta1.Machine) (bool, error) {
	// Request that the old authoritative resource reconciliation is paused.
	updated := false
	//nolint:wsl
	switch m.Spec.AuthoritativeAPI {
	case mapiv1beta1.MachineAuthorityClusterAPI:
		// For requesting pausing of a MAPI resource, it is sufficient to switch the spec.AuthoritativeAPI field on the MAPI resource.
		// which is already done before this code runs in this controller.
	case mapiv1beta1.MachineAuthorityMachineAPI:
		// For requesting pausing of a CAPI resource, set the paused annotation on it.
		// The spec.AuthoritativeAPI is set to MachineAPI, meaning that the old authoritativeAPI was ClusterAPI.
		// So Check if the ClusterAPI resource has the paused annotation, otherwise set it.
		capiMachine := &clusterv1.Machine{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: m.Name}, capiMachine); err != nil {
			return false, fmt.Errorf("failed to get Cluster API machine: %w", err)
		}

		infraMachineRef := capiMachine.Spec.InfrastructureRef

		infraMachine, err := external.GetObjectFromContractVersionedRef(ctx, r.Client, infraMachineRef, capiMachine.Namespace)
		if err != nil {
			return false, fmt.Errorf("failed to get Cluster API infra machine: %w", err)
		}

		if !annotations.HasPaused(capiMachine) {
			capiMachineCopy := capiMachine.DeepCopy()
			annotations.AddAnnotations(capiMachine, map[string]string{clusterv1.PausedAnnotation: ""})
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
			annotations.AddAnnotations(infraMachine, map[string]string{clusterv1.PausedAnnotation: ""})
			if err := r.Patch(ctx, infraMachine, client.MergeFrom(infraMachineCopy)); err != nil {
				return false, fmt.Errorf("failed to patch Cluster API infra machine: %w", err)
			}

			updated = true
		}
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

	// Because we are in a migration (spec.authoritativeAPI !=
	// status.authoritativeAPI), we assume that spec.authoritativeAPI is
	// currently the migration target, not the migration source. So:
	//
	// target: spec.authoritativeAPI
	// source: opposite of target
	//
	// We want to assert that source has been synched to target, so we need to
	// treat spec.AuthoritativeAPI as the opposite of the direction we want to
	// check.
	//
	// We may revisit this, as this assumption is not safe if a user aborts an
	// in-progress migration by resetting spec.authoritativeAPI to its original
	// value.

	switch mapiMachine.Spec.AuthoritativeAPI {
	case mapiv1beta1.MachineAuthorityClusterAPI:
		return mapiMachine.Status.SynchronizedGeneration == mapiMachine.Generation, nil
	case mapiv1beta1.MachineAuthorityMachineAPI:
		capiMachine := &clusterv1.Machine{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: mapiMachine.Name}, capiMachine); err != nil {
			return false, fmt.Errorf("failed to get Cluster API machine: %w", err)
		}

		// Given the CAPI infra machine template is immutable
		// we do not check for its generation to be synced up with the generation of the MAPI machine set.
		return (mapiMachine.Status.SynchronizedGeneration == capiMachine.Generation), nil
	case mapiv1beta1.MachineAuthorityMigrating:
	}

	// Should have been prevented by validation
	return false, fmt.Errorf("%w: %s", controllers.ErrInvalidSpecAuthoritativeAPI, mapiMachine.Spec.AuthoritativeAPI)
}

// applyStatusAuthoritativeAPIWithPatch updates the resource status.authoritativeAPI using a server-side apply patch.
func (r *MachineMigrationReconciler) applyStatusAuthoritativeAPIWithPatch(ctx context.Context, m *mapiv1beta1.Machine, authority mapiv1beta1.MachineAuthority) error {
	return synccommon.ApplyAuthoritativeAPI[*machinev1applyconfigs.MachineStatusApplyConfiguration](ctx, r.Client, controllerName, machinev1applyconfigs.Machine, m, authority)
}

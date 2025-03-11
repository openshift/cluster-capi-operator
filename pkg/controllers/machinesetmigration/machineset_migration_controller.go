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
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const controllerName string = "MachineSetMigrationController"

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
		For(&machinev1beta1.MachineSet{}, builder.WithPredicates(util.FilterNamespace(r.MAPINamespace))).
		Watches(
			&capiv1beta1.MachineSet{},
			handler.EnqueueRequestsFromMapFunc(util.RewriteNamespace(r.MAPINamespace)),
			builder.WithPredicates(util.FilterNamespace(r.CAPINamespace)),
		).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	// Set up API helpers from the manager.
	r.Client = mgr.GetClient()
	r.Scheme = mgr.GetScheme()
	r.Recorder = mgr.GetEventRecorderFor(controllerName)

	return nil
}

// Reconcile reconciles CAPI and MAPI MachineSets for their respective namespaces.
func (r *MachineSetMigrationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	logger.V(1).Info("Reconciling machine set")
	defer logger.V(1).Info("Finished reconciling machine set")

	mapiMachineSet := &machinev1beta1.MachineSet{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: req.Name}, mapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get MAPI machine set: %w", err)
	}

	// Observe the spec and status authoritative API fields and look for a difference between them.
	// A difference indicating the cluster admin requested a migration between the two APIs.
	if noMigrationRequested := !(mapiMachineSet.Spec.AuthoritativeAPI == mapiMachineSet.Status.AuthoritativeAPI); noMigrationRequested {
		// No migration is being requested for this MAPI MachineSet, nothing to do.
		return ctrl.Result{}, nil
	}

	// Check if the Synchronized condition is set to True.
	// if it is not, this indicates an unmigratable resource and therefore should take no action.
	if getConditionStatus(mapiMachineSet, consts.SynchronizedCondition) != corev1.ConditionTrue {
		// The Resource is NOT Syncronized, so the resource is not migrateable, nothing to do.
		return ctrl.Result{}, nil
	}

	if mapiMachineSet.Status.AuthoritativeAPI != machinev1beta1.MachineAuthorityMigrating {
		// Set the authoritativeAPI status to Migrating.
		if err := r.applyAuthoritativeAPIWithPatch(ctx, mapiMachineSet, machinev1beta1.MachineAuthorityMigrating); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
		}

		// Then wait for the old authoritative resource to set the Paused=True condition on the resource.
		return ctrl.Result{}, nil
	}

	_, err, isPaused := r.isOldAuthoritativeAPIPaused(ctx, mapiMachineSet)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure old AuthoritativeAPI is Paused: %w", err)
	}

	if !isPaused {
		// The old Authoritative API is not Paused yet, requeue to check later.
		return ctrl.Result{RequeueAfter: time.Second * 2}, nil
	}

	// Once the Paused condition is observed,
	// move the status.authoritativeAPI to match the spec.authoritativeAPI.

	// TODO, considering step 7 on the enhancement:
	// https://github.com/openshift/enhancements/blob/master/enhancements/machine-api/converting-machine-api-to-cluster-api.md#workflow-description
	//
	// 7: The migration controller verifies that
	// the move from Machine API to Cluster API is valid
	// by checking that the synchronized generation is up to date.
	//
	// Do we need to do this if we are checking Syncronized=True above already?

	// Set the actual AuthoritativeAPI to the desired one.
	if err := r.applyAuthoritativeAPIWithPatch(ctx, mapiMachineSet, mapiMachineSet.Spec.AuthoritativeAPI); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status with patch: %w", err)
	}

	return ctrl.Result{}, nil
}

// isOldAuthoritativeAPIPaused checks whether the old authoritativeAPI resources are paused.
func (r *MachineSetMigrationReconciler) isOldAuthoritativeAPIPaused(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet) (ctrl.Result, error, bool) {
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
			return ctrl.Result{}, fmt.Errorf("failed to get CAPI machine set: %w", err), false
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

	return ctrl.Result{}, nil, isPaused
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

// applyAuthoritativeAPIWithPatch updates the authoritativeAPI field
// using a server side apply patch. We do this to force ownership of the field.
func (r *MachineSetMigrationReconciler) applyAuthoritativeAPIWithPatch(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, authority machinev1beta1.MachineAuthority) error {
	statusAc := machinev1applyconfigs.MachineSetStatus().WithAuthoritativeAPI(authority)
	msAc := machinev1applyconfigs.MachineSet(mapiMachineSet.GetName(), mapiMachineSet.GetNamespace()).
		WithStatus(statusAc)

	if err := r.Status().Patch(ctx, mapiMachineSet, util.ApplyConfigPatch(msAc), client.ForceOwnership, client.FieldOwner(controllerName+"-AuthoritativeAPI")); err != nil {
		return fmt.Errorf("failed to patch MAPI machine set status with authoritativeAPI: %w", err)
	}

	return nil
}

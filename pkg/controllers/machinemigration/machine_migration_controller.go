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
	"slices"

	"github.com/go-logr/logr"
	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/migrationcommon"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/controllers/external"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

type machineMigratable struct {
	reconciler  *MachineMigrationReconciler
	mapiMachine *mapiv1beta1.Machine
}

// MAPIObject returns the backing Machine API machine.
func (m *machineMigratable) MAPIObject() client.Object {
	return m.mapiMachine
}

// DesiredAuthority returns the requested authoritative API from spec.
func (m *machineMigratable) DesiredAuthority() mapiv1beta1.MachineAuthority {
	return m.mapiMachine.Spec.AuthoritativeAPI
}

// CurrentAuthority returns the observed authoritative API from status.
func (m *machineMigratable) CurrentAuthority() mapiv1beta1.MachineAuthority {
	return m.mapiMachine.Status.AuthoritativeAPI
}

// SynchronizedAPI returns the last synchronized API recorded in status.
func (m *machineMigratable) SynchronizedAPI() mapiv1beta1.SynchronizedAPI {
	return m.mapiMachine.Status.SynchronizedAPI
}

// SynchronizedGeneration returns the generation recorded by the sync controller.
func (m *machineMigratable) SynchronizedGeneration() int64 {
	return m.mapiMachine.Status.SynchronizedGeneration
}

// MAPIConditions returns the Machine API conditions used by migration logic.
func (m *machineMigratable) MAPIConditions() []mapiv1beta1.Condition {
	return m.mapiMachine.Status.Conditions
}

// EnsureCAPIPaused pauses the primary Cluster API machine and its infra object.
func (m *machineMigratable) EnsureCAPIPaused(ctx context.Context, capiMachine *clusterv1.Machine) (bool, error) {
	return m.reconciler.ensureCAPIPaused(ctx, capiMachine)
}

// EnsureCAPIUnpaused removes pause from the primary Cluster API machine and its infra object.
func (m *machineMigratable) EnsureCAPIUnpaused(ctx context.Context, capiMachine *clusterv1.Machine) (bool, error) {
	return m.reconciler.ensureCAPIUnpaused(ctx, capiMachine)
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
func (r *MachineMigrationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

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

	result, err := migrationcommon.Reconcile(
		ctx,
		r.Client,
		controllerName,
		r.CAPINamespace,
		machinev1applyconfigs.Machine,
		&machineMigratable{
			reconciler:  r,
			mapiMachine: mapiMachine,
		},
	)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile machine migration state: %w", err)
	}

	return result, nil
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

func (r *MachineMigrationReconciler) getCAPIInfraMachine(ctx context.Context, capiMachine *clusterv1.Machine) (client.Object, bool, error) {
	if capiMachine.Spec.InfrastructureRef.Name == "" {
		return nil, false, nil
	}

	infraMachine, err := external.GetObjectFromContractVersionedRef(ctx, r.Client, capiMachine.Spec.InfrastructureRef, capiMachine.Namespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("failed to get Cluster API infra machine: %w", err)
	}

	return infraMachine, true, nil
}

func (r *MachineMigrationReconciler) ensureCAPIPaused(ctx context.Context, capiMachine *clusterv1.Machine) (bool, error) {
	paused, err := r.ensureCAPIMachinePaused(ctx, capiMachine)
	if err != nil {
		return false, err
	}

	if !paused {
		return false, nil
	}

	infraMachine, found, err := r.getCAPIInfraMachine(ctx, capiMachine)
	if err != nil {
		return false, err
	}

	if !found {
		return true, nil
	}

	return r.ensureCAPIInfraMachinePaused(ctx, infraMachine)
}

func (r *MachineMigrationReconciler) ensureCAPIMachinePaused(ctx context.Context, capiMachine *clusterv1.Machine) (bool, error) {
	changed, err := migrationcommon.AddPausedAnnotation(ctx, r.Client, capiMachine)
	if err != nil {
		return false, fmt.Errorf("failed to request pause on Cluster API machine: %w", err)
	}

	if changed {
		return false, nil
	}

	// Same reasoning as below in ensureCAPIInfraMachinePaused
	if !slices.Contains(capiMachine.Finalizers, clusterv1.MachineFinalizer) {
		return true, nil
	}

	machinePausedCondition := conditions.Get(capiMachine, clusterv1.PausedCondition)
	if machinePausedCondition == nil {
		return false, nil
	}

	return machinePausedCondition.Status == metav1.ConditionTrue, nil
}

func (r *MachineMigrationReconciler) ensureCAPIInfraMachinePaused(ctx context.Context, infraMachine client.Object) (bool, error) {
	changed, err := migrationcommon.AddPausedAnnotation(ctx, r.Client, infraMachine)
	if err != nil {
		return false, fmt.Errorf("failed to request pause on Cluster API infra machine: %w", err)
	}

	if changed {
		return false, nil
	}

	finalizer, err := capiInfraMachineFinalizerForPlatform(r.Platform)
	if err != nil {
		return false, err
	}

	// If the finalizer is present we know that the controller is running. It will observe the paused annotation and will eventually set the paused condition. We must wait for the paused condition because it may be actively reconciling.
	// If the finalizer is not present then either:
	// The controller is not running, in which it is safe to continue.
	// The controller has not yet observed the object, in which case (guaranteed by optimistic locking) it will observe our paused annotation before taking any action, so it is safe to continue.
	if !slices.Contains(infraMachine.GetFinalizers(), finalizer) {
		return true, nil
	}

	pausedStatus, err := util.GetConditionStatus(infraMachine, clusterv1.PausedCondition)
	if err != nil {
		return false, fmt.Errorf("unable to get paused condition for %s/%s: %w", infraMachine.GetNamespace(), infraMachine.GetName(), err)
	}

	return pausedStatus == corev1.ConditionTrue, nil
}

func (r *MachineMigrationReconciler) ensureCAPIUnpaused(ctx context.Context, capiMachine *clusterv1.Machine) (bool, error) {
	changed, err := migrationcommon.RemovePausedAnnotation(ctx, r.Client, capiMachine)
	if err != nil {
		return false, fmt.Errorf("failed to remove paused annotation from Cluster API machine: %w", err)
	}

	if changed {
		return false, nil
	}

	infraMachine, found, err := r.getCAPIInfraMachine(ctx, capiMachine)
	if err != nil {
		return false, err
	}

	if found {
		changed, err = migrationcommon.RemovePausedAnnotation(ctx, r.Client, infraMachine)
		if err != nil {
			return false, fmt.Errorf("failed to remove paused annotation from Cluster API infra machine: %w", err)
		}

		if changed {
			return false, nil
		}
	}

	machinePausedCondition := conditions.Get(capiMachine, clusterv1.PausedCondition)
	if machinePausedCondition != nil && machinePausedCondition.Status == metav1.ConditionTrue {
		return false, nil
	}

	if !found {
		return true, nil
	}

	infraPausedStatus, err := util.GetConditionStatus(infraMachine, clusterv1.PausedCondition)
	if err != nil {
		return false, fmt.Errorf("unable to get paused condition for %s/%s: %w", infraMachine.GetNamespace(), infraMachine.GetName(), err)
	}

	return infraPausedStatus != corev1.ConditionTrue, nil
}

func capiInfraMachineFinalizerForPlatform(platform configv1.PlatformType) (string, error) {
	switch platform {
	case configv1.AWSPlatformType:
		return awsv1.MachineFinalizer, nil
	case configv1.AzurePlatformType:
		return azurev1.MachineFinalizer, nil
	case configv1.GCPPlatformType:
		return gcpv1.MachineFinalizer, nil
	case configv1.PowerVSPlatformType:
		return ibmpowervsv1.IBMPowerVSMachineFinalizer, nil
	case configv1.VSpherePlatformType:
		return vspherev1.MachineFinalizer, nil
	case configv1.OpenStackPlatformType:
		return openstackv1.MachineFinalizer, nil
	case configv1.BareMetalPlatformType:
		return metal3v1.MachineFinalizer, nil
	default:
		return "", fmt.Errorf("%w: %s", util.ErrUnsupportedPlatform, platform)
	}
}

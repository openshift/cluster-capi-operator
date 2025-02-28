/*
Copyright 2024 Red Hat, Inc.

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

package machinesync

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	"github.com/openshift/cluster-capi-operator/pkg/util"

	"github.com/go-test/deep"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/record"
	capav1beta2 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	// errAssertingCAPIAWSMachine is returned when we encounter an issue asserting a client.Object into a AWSMachine.
	errAssertingCAPIAWSMachine = errors.New("error asserting the CAPI AWSMachine object")

	// errAssertingCAPIPowerVSMachine is returned when we encounter an issue asserting a client.Object into a IBMPowerVSMachine.
	errAssertingCAPIIBMPowerVSMachine = errors.New("error asserting the CAPI IBMPowerVSMachine object")
)

const (
	reasonFailedToGetCAPIInfraResources       = "FailedToGetCAPIInfraResources"
	reasonFailedToConvertMAPIMachineToCAPI    = "FailedToConvertMAPIMachineToCAPI"
	reasonFailedToUpdateCAPIInfraMachine      = "FailedToUpdateCAPIInfraMachine"
	reasonFailedToCreateCAPIInfraMachine      = "FailedToCreateCAPIInfraMachine"
	reasonFailedToUpdateCAPIMachine           = "FailedToUpdateCAPIMachine"
	reasonFailedToCreateCAPIMachine           = "FailedToCreateCAPIMachine"
	reasonProgressingToCreateCAPIInfraMachine = "ProgressingToCreateCAPIInfraMachine"

	capiNamespace  string = "openshift-cluster-api"
	mapiNamespace  string = "openshift-machine-api"
	machineSetKind string = "MachineSet"
	controllerName string = "MachineSyncController"

	messageSuccessfullySynchronizedCAPItoMAPI = "Successfully synchronized CAPI Machine to MAPI"
	messageSuccessfullySynchronizedMAPItoCAPI = "Successfully synchronized MAPI Machine to CAPI"
	progressingToSynchronizeMAPItoCAPI        = "Progressing to synchronize MAPI Machine to CAPI"
)

var (
	// errPlatformNotSupported is returned when the platform is not supported.
	errPlatformNotSupported = errors.New("error determining InfraMachine type, platform not supported")
	// errUnrecognizedConditionStatus is returned when the condition status is not recognized.
	errUnrecognizedConditionStatus = errors.New("error unrecognized condition status")
)

// MachineSyncReconciler reconciles CAPI and MAPI machines.
type MachineSyncReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Infra         *configv1.Infrastructure
	Platform      configv1.PlatformType
	CAPINamespace string
	MAPINamespace string
}

// SetupWithManager sets the CoreClusterReconciler controller up with the given manager.
func (r *MachineSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	infraMachine, _, err := initInfraMachineAndInfraClusterFromProvider(r.Platform)
	if err != nil {
		return fmt.Errorf("failed to get InfraMachine from Provider: %w", err)
	}

	// Allow the namespaces to be set externally for test purposes, when not set,
	// default to the production namespaces.
	if r.CAPINamespace == "" {
		r.CAPINamespace = capiNamespace
	}

	if r.MAPINamespace == "" {
		r.MAPINamespace = mapiNamespace
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

	// Set up API helpers from the manager.
	r.Client = mgr.GetClient()
	r.Scheme = mgr.GetScheme()
	r.Recorder = mgr.GetEventRecorderFor("machine-sync-controller")

	return nil
}

// Reconcile reconciles CAPI and MAPI machines for their respective namespaces.
//
//nolint:funlen
func (r *MachineSyncReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "namespace", req.Namespace, "name", req.Name)

	logger.V(1).Info("Reconciling machine")
	defer logger.V(1).Info("Finished reconciling machine")

	var mapiMachineNotFound, capiMachineNotFound bool

	// Get the MAPI Machine.
	mapiMachine := &machinev1beta1.Machine{}
	mapiNamespacedName := client.ObjectKey{
		Namespace: r.MAPINamespace,
		Name:      req.Name,
	}

	if err := r.Get(ctx, mapiNamespacedName, mapiMachine); apierrors.IsNotFound(err) {
		logger.Info("MAPI Machine not found")

		mapiMachineNotFound = true
	} else if err != nil {
		logger.Error(err, "Failed to get MAPI Machine")
		return ctrl.Result{}, fmt.Errorf("failed to get MAPI machine: %w", err)
	}

	// Get the corresponding CAPI Machine.
	capiMachine := &capiv1beta1.Machine{}
	capiNamespacedName := client.ObjectKey{
		Namespace: r.CAPINamespace,
		Name:      req.Name,
	}

	if err := r.Get(ctx, capiNamespacedName, capiMachine); apierrors.IsNotFound(err) {
		logger.Info("CAPI Machine not found")

		capiMachine = nil
		capiMachineNotFound = true
	} else if err != nil {
		logger.Error(err, "Failed to get CAPI Machine")
		return ctrl.Result{}, fmt.Errorf("failed to get CAPI machine:: %w", err)
	}

	if mapiMachineNotFound && capiMachineNotFound {
		logger.Info("CAPI and MAPI machines not found, nothing to do")
		return ctrl.Result{}, nil
	}

	// We mirror a CAPI Machine to a MAPI Machine if the CAPI machine is owned by a CAPI MachineSet which has a MAPI MachineSet counterpart.
	// This is because we want to be able to migrate in both directions.
	if mapiMachineNotFound {
		if shouldReconcile, err := r.shouldMirrorCAPIMachineToMAPIMachine(ctx, logger, capiMachine); err != nil {
			return ctrl.Result{}, err
		} else if shouldReconcile {
			return r.reconcileCAPIMachinetoMAPIMachine(ctx, capiMachine, mapiMachine)
		}
	}

	switch mapiMachine.Status.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityMachineAPI:
		return r.reconcileMAPIMachinetoCAPIMachine(ctx, mapiMachine, capiMachine)
	case machinev1beta1.MachineAuthorityClusterAPI:
		return r.reconcileCAPIMachinetoMAPIMachine(ctx, capiMachine, mapiMachine)
	case machinev1beta1.MachineAuthorityMigrating:
		logger.Info("machine currently migrating", "machine", mapiMachine.GetName())
		return ctrl.Result{}, nil
	default:
		logger.Info("machine AuthoritativeAPI has unexpected value", "AuthoritativeAPI", mapiMachine.Status.AuthoritativeAPI)
		return ctrl.Result{}, nil
	}
}

// reconcileCAPIMachinetoMAPIMachine reconciles a CAPI Machine to a MAPI Machine.
func (r *MachineSyncReconciler) reconcileCAPIMachinetoMAPIMachine(ctx context.Context, capiMachine *capiv1beta1.Machine, mapiMachine *machinev1beta1.Machine) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// reconcileMAPIMachinetoCAPIMachine a MAPI Machine to a CAPI Machine.
func (r *MachineSyncReconciler) reconcileMAPIMachinetoCAPIMachine(ctx context.Context, mapiMachine *machinev1beta1.Machine, capiMachine *capiv1beta1.Machine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	newCAPIMachine, newCAPIInfraMachine, warns, err := r.convertMAPIToCAPIMachine(mapiMachine)
	if err != nil {
		conversionErr := fmt.Errorf("failed to convert MAPI machine to CAPI machine: %w", err)
		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineToCAPI, conversionErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{conversionErr, condErr})
		}

		return ctrl.Result{}, conversionErr
	}

	for _, warning := range warns {
		logger.Info("Warning during conversion", "warning", warning)
		r.Recorder.Event(mapiMachine, corev1.EventTypeWarning, "ConversionWarning", warning)
	}

	newCAPIMachine.SetResourceVersion(util.GetResourceVersion(client.Object(capiMachine)))
	newCAPIMachine.SetNamespace(r.CAPINamespace)
	newCAPIMachine.Spec.InfrastructureRef.Namespace = r.CAPINamespace

	_, infraMachine, err := r.fetchCAPIInfraResources(ctx, newCAPIMachine)
	if err != nil && !apierrors.IsNotFound(err) {
		fetchErr := fmt.Errorf("failed to fetch CAPI infra resources: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, fetchErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{fetchErr, condErr})
		}

		return ctrl.Result{}, fetchErr
	}

	if infraMachine != nil {
		newCAPIInfraMachine.SetGeneration(infraMachine.GetGeneration())
		newCAPIInfraMachine.SetUID(infraMachine.GetUID())
		newCAPIInfraMachine.SetCreationTimestamp(infraMachine.GetCreationTimestamp())
		newCAPIInfraMachine.SetManagedFields(infraMachine.GetManagedFields())
	}

	newCAPIInfraMachine.SetResourceVersion(util.GetResourceVersion(infraMachine))
	newCAPIInfraMachine.SetNamespace(r.CAPINamespace)

	result, syncronizationIsProgressing, err := r.createOrUpdateCAPIInfraMachine(ctx, mapiMachine, infraMachine, newCAPIInfraMachine)
	if err != nil {
		return result, fmt.Errorf("unable to ensure CAPI infra machine: %w", err)
	}

	if result, err := r.createOrUpdateCAPIMachine(ctx, mapiMachine, capiMachine, newCAPIMachine); err != nil {
		return result, fmt.Errorf("unable to ensure CAPI machine: %w", err)
	}

	if syncronizationIsProgressing {
		return ctrl.Result{}, r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionUnknown,
			reasonProgressingToCreateCAPIInfraMachine, progressingToSynchronizeMAPItoCAPI, nil)
	}

	return ctrl.Result{}, r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionTrue,
		consts.ReasonResourceSynchronized, messageSuccessfullySynchronizedMAPItoCAPI, &mapiMachine.Generation)
}

// convertMAPIToCAPIMachine converts a MAPI Machine to a CAPI Machine, selecting the correct converter based on the platform.
func (r *MachineSyncReconciler) convertMAPIToCAPIMachine(mapiMachine *machinev1beta1.Machine) (*capiv1beta1.Machine, client.Object, []string, error) {
	switch r.Platform {
	case configv1.AWSPlatformType:
		return mapi2capi.FromAWSMachineAndInfra(mapiMachine, r.Infra).ToMachineAndInfrastructureMachine() //nolint:wrapcheck
	case configv1.PowerVSPlatformType:
		return mapi2capi.FromPowerVSMachineAndInfra(mapiMachine, r.Infra).ToMachineAndInfrastructureMachine() //nolint:wrapcheck
	default:
		return nil, nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, r.Platform)
	}
}

// createOrUpdateCAPIInfraMachine creates a CAPI infra machine from a MAPI machine, or updates if it exists and it is out of date.
//
//nolint:funlen
func (r *MachineSyncReconciler) createOrUpdateCAPIInfraMachine(ctx context.Context, mapiMachine *machinev1beta1.Machine, infraMachine client.Object, newCAPIInfraMachine client.Object) (ctrl.Result, bool, error) { //nolint:unparam
	logger := log.FromContext(ctx)
	// This variable tracks whether or not we are still progressing
	// towards syncronizing the MAPI machine with the CAPI infra machine.
	// It is then passed up the stack so the syncronized condition can be set accordingly.
	syncronizationIsProgressing := false

	if infraMachine == nil {
		if err := r.Create(ctx, newCAPIInfraMachine); err != nil {
			logger.Error(err, "Failed to create CAPI infra machine")
			createErr := fmt.Errorf("failed to create CAPI infra machine: %w", err)

			if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToCreateCAPIInfraMachine, createErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, syncronizationIsProgressing, utilerrors.NewAggregate([]error{createErr, condErr})
			}

			return ctrl.Result{}, syncronizationIsProgressing, createErr
		}

		logger.Info("Successfully created CAPI infra machine")

		return ctrl.Result{}, syncronizationIsProgressing, nil
	}

	capiInfraMachinesDiff, err := compareCAPIInfraMachines(r.Platform, infraMachine, newCAPIInfraMachine)
	if err != nil {
		logger.Error(err, "Failed to check CAPI infra machine diff")
		updateErr := fmt.Errorf("failed to check CAPI infra machine diff: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachine, updateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, syncronizationIsProgressing, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return ctrl.Result{}, syncronizationIsProgressing, updateErr
	}

	if len(capiInfraMachinesDiff) == 0 {
		logger.Info("No changes detected in CAPI infra machine")
		return ctrl.Result{}, syncronizationIsProgressing, nil
	}

	logger.Info("Deleting the corresponding CAPI infra machine as it is out of date, it will be recreated", "diff", capiInfraMachinesDiff)

	if err := r.Delete(ctx, infraMachine); err != nil {
		logger.Error(err, "Failed to delete CAPI infra machine")

		deleteErr := fmt.Errorf("failed to delete CAPI infra machine: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachine, deleteErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, syncronizationIsProgressing, utilerrors.NewAggregate([]error{deleteErr, condErr})
		}

		return ctrl.Result{}, syncronizationIsProgressing, deleteErr
	}

	// The outdated outdated CAPI infra machine has been deleted.
	// We will try and recreate an up-to-date one later.
	logger.Info("Successfully deleted outdated CAPI infra machine")

	// Set the syncronized as progressing to signal the caller
	// we are still progressing and aren't fully synced yet.
	syncronizationIsProgressing = true

	return ctrl.Result{}, syncronizationIsProgressing, nil
}

// createOrUpdateCAPIMachine creates a CAPI machine from a MAPI one, or updates if it exists and it is out of date.
func (r *MachineSyncReconciler) createOrUpdateCAPIMachine(ctx context.Context, mapiMachine *machinev1beta1.Machine, capiMachine *capiv1beta1.Machine, newCAPIMachine *capiv1beta1.Machine) (ctrl.Result, error) { //nolint:unparam
	logger := log.FromContext(ctx)

	if capiMachine == nil {
		if err := r.Create(ctx, newCAPIMachine); err != nil {
			logger.Error(err, "Failed to create CAPI machine")

			createErr := fmt.Errorf("failed to create CAPI machine: %w", err)
			if condErr := r.applySynchronizedConditionWithPatch(
				ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToCreateCAPIMachine, createErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, utilerrors.NewAggregate([]error{createErr, condErr})
			}

			return ctrl.Result{}, createErr
		}

		logger.Info("Successfully created CAPI machine")

		return ctrl.Result{}, nil
	}

	capiMachinesDiff := compareCAPIMachines(capiMachine, newCAPIMachine)

	if len(capiMachinesDiff) == 0 {
		logger.Info("No changes detected in CAPI machine")
		return ctrl.Result{}, nil
	}

	logger.Info("Changes detected, updating CAPI machine", "diff", capiMachinesDiff)

	if err := r.Update(ctx, newCAPIMachine); err != nil {
		logger.Error(err, "Failed to update CAPI machine")

		updateErr := fmt.Errorf("failed to update CAPI machine: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachine, updateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return ctrl.Result{}, updateErr
	}

	logger.Info("Successfully updated CAPI machine")

	return ctrl.Result{}, nil
}

// initInfraMachineAndInfraClusterFromProvider returns the correct InfraMachine and InfraCluster implementation
// for a given provider.
//
// As we implement other cloud providers, we'll need to update this list.
func initInfraMachineAndInfraClusterFromProvider(platform configv1.PlatformType) (client.Object, client.Object, error) {
	switch platform {
	case configv1.AWSPlatformType:
		return &capav1beta2.AWSMachine{}, &capav1beta2.AWSCluster{}, nil
	case configv1.PowerVSPlatformType:
		return &capibmv1.IBMPowerVSMachine{}, &capibmv1.IBMPowerVSCluster{}, nil
	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

// shouldMirrorCAPIMachineToMAPIMachine takes a CAPI machine and determines if there should
// be a MAPI mirror, it returns true only if:
//
// 1. The CAPI machine is owned by a CAPI machineset,
// 2. That owning CAPI machineset has a MAPI machineset Mirror.
func (r *MachineSyncReconciler) shouldMirrorCAPIMachineToMAPIMachine(ctx context.Context, logger logr.Logger, machine *capiv1beta1.Machine) (bool, error) {
	logger.WithName("shouldMirrorCAPIMachineToMAPIMachine").
		Info("checking if CAPI machine should be mirrored", "machine", machine.GetName())

	// Check if the CAPI machine has an ownerReference that points to a CAPI machineset.
	for _, ref := range machine.ObjectMeta.OwnerReferences {
		if ref.Kind != machineSetKind || ref.APIVersion != capiv1beta1.GroupVersion.String() {
			continue
		}

		logger.Info("CAPI machine is owned by a CAPI machineset", "machine", machine.GetName(), "machineset", ref.Name)

		// Checks if the CAPI machineset has a MAPI machineset mirror (same name) in MAPI namespace.
		key := client.ObjectKey{
			Namespace: r.MAPINamespace,
			Name:      ref.Name, // same name as the CAPI machineset.
		}
		mapiMachineSet := &machinev1beta1.MachineSet{}

		if err := r.Get(ctx, key, mapiMachineSet); apierrors.IsNotFound(err) {
			logger.Info("MAPI machineset mirror not found for the CAPI machineset, nothing to do", "machine", machine.GetName(), "machineset", ref.Name)

			return false, nil
		} else if err != nil {
			logger.Error(err, "Failed to get MAPI machineset mirror")

			return false, fmt.Errorf("failed to get MAPI machineset: %w", err)
		}

		return true, nil
	}

	logger.Info("CAPI machine is not owned by a machineset, nothing to do", "machine", machine.GetName())

	return false, nil
}

// fetchCAPIInfraResources fetches the provider specific infrastructure resources depending on which provider is set.
func (r *MachineSyncReconciler) fetchCAPIInfraResources(ctx context.Context, capiMachine *capiv1beta1.Machine) (client.Object, client.Object, error) {
	var infraCluster, infraMachine client.Object

	infraClusterKey := client.ObjectKey{
		Namespace: capiMachine.Namespace,
		Name:      capiMachine.Spec.ClusterName,
	}

	infraMachineRef := capiMachine.Spec.InfrastructureRef
	infraMachineKey := client.ObjectKey{
		Namespace: infraMachineRef.Namespace,
		Name:      infraMachineRef.Name,
	}

	infraMachine, infraCluster, err := initInfraMachineAndInfraClusterFromProvider(r.Platform)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to devise CAPI infra resources: %w", err)
	}

	if err := r.Get(ctx, infraClusterKey, infraCluster); err != nil {
		return nil, nil, fmt.Errorf("failed to get CAPI infrastructure cluster: %w", err)
	}

	if err := r.Get(ctx, infraMachineKey, infraMachine); err != nil {
		return nil, nil, fmt.Errorf("failed to get CAPI infrastructure machine: %w", err)
	}

	return infraCluster, infraMachine, nil
}

// compareCAPIMachines compares CAPI machines a and b, and returns a list of differences, or none if there are none.
func compareCAPIMachines(capiMachine1, capiMachine2 *capiv1beta1.Machine) []string {
	var diff []string
	diff = append(diff, deep.Equal(capiMachine1.Spec, capiMachine2.Spec)...)
	diff = append(diff, util.ObjectMetaEqual(capiMachine1.ObjectMeta, capiMachine2.ObjectMeta)...)

	return diff
}

// compareCAPIInfraMachines compares CAPI infra machines a and b, and returns a list of differences, or none if there are none.
func compareCAPIInfraMachines(platform configv1.PlatformType, infraMachine1, infraMachine2 client.Object) ([]string, error) {
	switch platform {
	case configv1.AWSPlatformType:
		typedInfraMachine1, ok := infraMachine1.(*capav1beta2.AWSMachine)
		if !ok {
			return nil, errAssertingCAPIAWSMachine
		}

		typedinfraMachine2, ok := infraMachine2.(*capav1beta2.AWSMachine)
		if !ok {
			return nil, errAssertingCAPIAWSMachine
		}

		var diff []string
		diff = append(diff, deep.Equal(typedInfraMachine1.Spec, typedinfraMachine2.Spec)...)
		diff = append(diff, util.ObjectMetaEqual(typedInfraMachine1.ObjectMeta, typedinfraMachine2.ObjectMeta)...)

		return diff, nil
	case configv1.PowerVSPlatformType:
		typedInfraMachine1, ok := infraMachine1.(*capibmv1.IBMPowerVSMachine)
		if !ok {
			return nil, errAssertingCAPIIBMPowerVSMachine
		}

		typedinfraMachine2, ok := infraMachine2.(*capibmv1.IBMPowerVSMachine)
		if !ok {
			return nil, errAssertingCAPIIBMPowerVSMachine
		}

		var diff []string
		diff = append(diff, deep.Equal(typedInfraMachine1.Spec, typedinfraMachine2.Spec)...)
		diff = append(diff, util.ObjectMetaEqual(typedInfraMachine1.ObjectMeta, typedinfraMachine2.ObjectMeta)...)

		return diff, nil
	default:
		return nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

// applySynchronizedConditionWithPatch updates the synchronized condition
// using a server side apply patch. We do this to force ownership of the
// 'Synchronized' condition and 'SynchronizedGeneration'.
func (r *MachineSyncReconciler) applySynchronizedConditionWithPatch(ctx context.Context, mapiMachine *machinev1beta1.Machine, status corev1.ConditionStatus, reason, message string, generation *int64) error {
	var (
		severity               machinev1beta1.ConditionSeverity
		synchronizedGeneration int64
	)

	switch status {
	case corev1.ConditionTrue:
		severity = machinev1beta1.ConditionSeverityNone

		if generation != nil {
			// Update the SynchronizedGeneration to the newer Generation value.
			synchronizedGeneration = *generation
		}
	case corev1.ConditionFalse:
		severity = machinev1beta1.ConditionSeverityError
		// Restore the old SynchronizedGeneration, otherwise if that's not set the existing one will be cleared.
		synchronizedGeneration = mapiMachine.Status.SynchronizedGeneration
	case corev1.ConditionUnknown:
		severity = machinev1beta1.ConditionSeverityInfo
		// Restore the old SynchronizedGeneration, otherwise if that's not set the existing one will be cleared.
		synchronizedGeneration = mapiMachine.Status.SynchronizedGeneration
	default:
		return fmt.Errorf("%w: %s", errUnrecognizedConditionStatus, status)
	}

	conditionAc := machinev1applyconfigs.Condition().
		WithType(consts.SynchronizedCondition).
		WithStatus(status).
		WithReason(reason).
		WithMessage(message).
		WithSeverity(severity)

	util.SetLastTransitionTime(consts.SynchronizedCondition, mapiMachine.Status.Conditions, conditionAc)

	statusAc := machinev1applyconfigs.MachineStatus().
		WithConditions(conditionAc).
		WithSynchronizedGeneration(synchronizedGeneration)

	msAc := machinev1applyconfigs.Machine(mapiMachine.GetName(), mapiMachine.GetNamespace()).
		WithStatus(statusAc)

	if err := r.Status().Patch(ctx, mapiMachine, util.ApplyConfigPatch(msAc), client.ForceOwnership, client.FieldOwner(controllerName+"-SynchronizedCondition")); err != nil {
		return fmt.Errorf("failed to patch MAPI machine status with synchronized condition: %w", err)
	}

	return nil
}

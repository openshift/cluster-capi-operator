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
	"time"

	"github.com/go-logr/logr"

	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	"github.com/openshift/cluster-capi-operator/pkg/util"

	"github.com/go-test/deep"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	capav1beta2 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/labels/format"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	reasonCAPIMachineNotFound                 = "CAPIMachineNotFound"
	reasonFailedToConvertCAPIMachineToMAPI    = "FailedToConvertCAPIMachineToMAPI"
	reasonFailedToConvertMAPIMachineToCAPI    = "FailedToConvertMAPIMachineToCAPI"
	reasonFailedToCreateCAPIInfraMachine      = "FailedToCreateCAPIInfraMachine"
	reasonFailedToCreateCAPIMachine           = "FailedToCreateCAPIMachine"
	reasonFailedToCreateMAPIMachine           = "FailedToCreateMAPIMachine"
	reasonFailedToGetCAPIInfraResources       = "FailedToGetCAPIInfraResources"
	reasonFailedToUpdateCAPIInfraMachine      = "FailedToUpdateCAPIInfraMachine"
	reasonFailedToUpdateCAPIMachine           = "FailedToUpdateCAPIMachine"
	reasonFailedToUpdateMAPIMachine           = "FailedToUpdateMAPIMachine"
	reasonProgressingToCreateCAPIInfraMachine = "ProgressingToCreateCAPIInfraMachine"

	capiNamespace  string = "openshift-cluster-api"
	machineKind    string = "Machine"
	machineSetKind string = "MachineSet"
	cpmsKind       string = "ControlPlaneMachineSet"
	controllerName string = "MachineSyncController"
	mapiNamespace  string = "openshift-machine-api"

	messageSuccessfullySynchronizedCAPItoMAPI = "Successfully synchronized CAPI Machine to MAPI"
	messageSuccessfullySynchronizedMAPItoCAPI = "Successfully synchronized MAPI Machine to CAPI"
	progressingToSynchronizeMAPItoCAPI        = "Progressing to synchronize MAPI Machine to CAPI"
)

var (
	// errAssertingCAPIAWSMachine is returned when we encounter an issue asserting a client.Object into a AWSMachine.
	errAssertingCAPIAWSMachine = errors.New("error asserting the Cluster API AWSMachine object")

	// errAssertingCAPIPowerVSMachine is returned when we encounter an issue asserting a client.Object into a IBMPowerVSMachine.
	errAssertingCAPIIBMPowerVSMachine = errors.New("error asserting the Cluster API IBMPowerVSMachine object")

	// errCAPIMachineNotFound is returned when the AuthoritativeAPI is set to CAPI on the MAPI machine,
	// but we can't find the CAPI machine.
	//lint:ignore ST1005 Cluster API is a name.
	//nolint:stylecheck
	errCAPIMachineNotFound = errors.New("Cluster API machine not found")

	// errPlatformNotSupported is returned when the platform is not supported.
	errPlatformNotSupported = errors.New("error determining InfraMachine type, platform not supported")

	// errUnrecognizedConditionStatus is returned when the condition status is not recognized.
	errUnrecognizedConditionStatus = errors.New("error unrecognized condition status")

	// errUnexpectedInfraMachineType is returned when we receive an unexpected InfraMachine type.
	errUnexpectedInfraMachineType = errors.New("unexpected InfraMachine type")

	// errUnexpectedInfraClusterType is returned when we receive an unexpected InfraCluster type.
	errUnexpectedInfraClusterType = errors.New("unexpected InfraCluster type")

	// errUnsuportedOwnerKindForConversion is returned when attempting to convert unsupported ownerReference.
	errUnsuportedOwnerKindForConversion = errors.New("unsupported owner kind for owner reference conversion")

	// errUnsupportedCPMSOwnedMachineConversion is returned when attempting to convert ControlPlaneMachineSet owned machines.
	errUnsupportedCPMSOwnedMachineConversion = errors.New("conversion of control plane machines owned by control plane machine set is currently not supported")
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

		mapiMachine = nil
		mapiMachineNotFound = true
	} else if err != nil {
		logger.Error(err, "Failed to get Machine API Machine")
		return ctrl.Result{}, fmt.Errorf("failed to get Machine API machine: %w", err)
	}

	// Get the corresponding CAPI Machine.
	capiMachine := &capiv1beta1.Machine{}
	capiNamespacedName := client.ObjectKey{
		Namespace: r.CAPINamespace,
		Name:      req.Name,
	}

	if err := r.Get(ctx, capiNamespacedName, capiMachine); apierrors.IsNotFound(err) {
		logger.Info("Cluster API Machine not found")

		capiMachine = nil
		capiMachineNotFound = true
	} else if err != nil {
		logger.Error(err, "Failed to get Cluster API Machine")
		return ctrl.Result{}, fmt.Errorf("failed to get Cluster API machine:: %w", err)
	}

	if mapiMachineNotFound && capiMachineNotFound {
		logger.Info("Cluster API and Machine API machines not found, nothing to do")
		return ctrl.Result{}, nil
	}

	// We mirror a CAPI Machine to a MAPI Machine if the CAPI machine is owned by
	// a CAPI MachineSet which has a MAPI MachineSet counterpart. This is because
	// we want to be able to migrate in both directions.
	if mapiMachineNotFound {
		if shouldReconcile, err := r.shouldMirrorCAPIMachineToMAPIMachine(ctx, logger, capiMachine); err != nil {
			return ctrl.Result{}, err
		} else if shouldReconcile {
			return r.reconcileCAPIMachinetoMAPIMachine(ctx, capiMachine, mapiMachine)
		}
		// We have triggered reconciliation from a CAPI machine, likely independent
		// of MAPI. We aren't in the scenario where we want to reconcile CAPI ->
		// MAPI.
		return ctrl.Result{}, nil
	}

	switch mapiMachine.Status.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityMachineAPI:
		return r.reconcileMAPIMachinetoCAPIMachine(ctx, mapiMachine, capiMachine)
	case machinev1beta1.MachineAuthorityClusterAPI:
		return r.reconcileCAPIMachinetoMAPIMachine(ctx, capiMachine, mapiMachine)
	case machinev1beta1.MachineAuthorityMigrating:
		logger.Info("Machine currently migrating", "machine", mapiMachine.GetName())
		return ctrl.Result{}, nil
	case "":
		logger.Info("Machine status.authoritativeAPI is empty, will check again later", "AuthoritativeAPI", mapiMachine.Status.AuthoritativeAPI)
		return ctrl.Result{}, nil
	default:
		logger.Info("Machine status.authoritativeAPI has unexpected value", "AuthoritativeAPI", mapiMachine.Status.AuthoritativeAPI)
		return ctrl.Result{}, nil
	}
}

// reconcileCAPIMachinetoMAPIMachine reconciles a CAPI Machine to a MAPI Machine.
//
//nolint:gocognit,funlen
func (r *MachineSyncReconciler) reconcileCAPIMachinetoMAPIMachine(ctx context.Context, capiMachine *capiv1beta1.Machine, mapiMachine *machinev1beta1.Machine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if capiMachine == nil {
		logger.Error(errCAPIMachineNotFound, "machine", mapiMachine.Name)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachine, corev1.ConditionFalse, reasonCAPIMachineNotFound, errCAPIMachineNotFound.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{errCAPIMachineNotFound, condErr})
		}

		return ctrl.Result{}, errCAPIMachineNotFound
	}

	newMAPIOwnerReferences, err := r.convertCAPIMachineOwnerReferencesToMAPI(ctx, capiMachine)
	//nolint:nestif
	if err != nil {
		var fe *field.Error
		if errors.As(err, &fe) {
			if mapiMachine != nil {
				if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToConvertCAPIMachineToMAPI, fe.Detail, nil); condErr != nil {
					return ctrl.Result{}, utilerrors.NewAggregate([]error{err, condErr})
				}
			}

			logger.Error(err, "unable to convert Cluster API machine to Machine API, unsupported owner reference in conversion")

			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to convert Cluster API machine owner references to Machine API: %w", err)
	}

	infraCluster, infraMachine, err := r.fetchCAPIInfraResources(ctx, capiMachine)
	if err != nil {
		fetchErr := fmt.Errorf("failed to fetch Cluster API infra resources: %w", err)

		if mapiMachine == nil {
			r.Recorder.Event(capiMachine, corev1.EventTypeWarning, "SynchronizationWarning", fetchErr.Error())
			return ctrl.Result{}, fetchErr
		}

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, fetchErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{fetchErr, condErr})
		}

		return ctrl.Result{}, fetchErr
	}

	newMapiMachine, warns, err := r.convertCAPIToMAPIMachine(capiMachine, infraMachine, infraCluster)
	if err != nil {
		conversionErr := fmt.Errorf("failed to convert Cluster API machine to Machine API machine: %w", err)

		if mapiMachine == nil {
			r.Recorder.Event(capiMachine, corev1.EventTypeWarning, "SynchronizationWarning", conversionErr.Error())
			return ctrl.Result{}, conversionErr
		}

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToConvertCAPIMachineToMAPI, conversionErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{conversionErr, condErr})
		}

		return ctrl.Result{}, conversionErr
	}

	for _, warning := range warns {
		logger.Info("Warning during conversion", "warning", warning)
		r.Recorder.Event(mapiMachine, corev1.EventTypeWarning, "ConversionWarning", warning)
	}

	newMapiMachine.SetNamespace(r.MAPINamespace)
	newMapiMachine.SetOwnerReferences(newMAPIOwnerReferences)

	if mapiMachine != nil {
		newMapiMachine.SetResourceVersion(util.GetResourceVersion(mapiMachine))
		// Restore authoritativeness to the current one.
		newMapiMachine.Spec.AuthoritativeAPI = mapiMachine.Spec.AuthoritativeAPI
		// Restore finalizers to the current one.
		newMapiMachine.ObjectMeta.Finalizers = mapiMachine.Finalizers
	} else {
		// If there is no existing MAPI machine it means we are creating a MAPI machine
		// from scratch from CAPI one, hence set the authoritativeness for it to Cluster API.
		newMapiMachine.Spec.AuthoritativeAPI = machinev1beta1.MachineAuthorityClusterAPI
	}

	if result, err := r.createOrUpdateMAPIMachine(ctx, mapiMachine, newMapiMachine); err != nil {
		createUpdateErr := fmt.Errorf("unable to ensure Machine API machine: %w", err)

		if mapiMachine == nil {
			r.Recorder.Event(capiMachine, corev1.EventTypeWarning, "SynchronizationWarning", createUpdateErr.Error())
			return ctrl.Result{}, createUpdateErr
		}

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToConvertCAPIMachineToMAPI, createUpdateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{createUpdateErr, condErr})
		}

		return result, createUpdateErr
	}

	return ctrl.Result{}, r.applySynchronizedConditionWithPatch(ctx, newMapiMachine, corev1.ConditionTrue,
		consts.ReasonResourceSynchronized, messageSuccessfullySynchronizedCAPItoMAPI, &capiMachine.Generation)
}

// reconcileMAPIMachinetoCAPIMachine a MAPI Machine to a CAPI Machine.
//
//nolint:funlen
func (r *MachineSyncReconciler) reconcileMAPIMachinetoCAPIMachine(ctx context.Context, mapiMachine *machinev1beta1.Machine, capiMachine *capiv1beta1.Machine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	newCAPIOwnerReferences, err := r.convertMAPIMachineOwnerReferencesToCAPI(ctx, mapiMachine)
	//nolint:nestif
	if err != nil {
		var fe *field.Error
		if errors.As(err, &fe) {
			if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineToCAPI, fe.Detail, nil); condErr != nil {
				return ctrl.Result{}, utilerrors.NewAggregate([]error{err, condErr})
			}

			if fe.Detail == errUnsupportedCPMSOwnedMachineConversion.Error() {
				logger.Info("Not converting control plane Machine. Conversion of Machine API machines owned by control plane machine set is currently not supported")
				return ctrl.Result{}, nil
			}

			logger.Error(err, "unable to convert Machine API machine to Cluster API, unsupported owner reference in conversion")

			return ctrl.Result{}, nil
		}

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineToCAPI, fmt.Errorf("failed to convert Machine API machine owner references to Cluster API: %w", err).Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{err, condErr})
		}

		return ctrl.Result{}, fmt.Errorf("failed to convert Machine API machine owner references to Cluster API: %w", err)
	}

	newCAPIMachine, newCAPIInfraMachine, warns, err := r.convertMAPIToCAPIMachine(mapiMachine)
	if err != nil {
		conversionErr := fmt.Errorf("failed to convert Machine API machine to Cluster API machine: %w", err)
		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineToCAPI, conversionErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{conversionErr, condErr})
		}

		return ctrl.Result{}, conversionErr
	}

	for _, warning := range warns {
		logger.Info("Warning during conversion", "warning", warning)
		r.Recorder.Event(mapiMachine, corev1.EventTypeWarning, "ConversionWarning", warning)
	}

	if capiMachine != nil {
		newCAPIMachine.SetGeneration(capiMachine.GetGeneration())
		newCAPIMachine.SetUID(capiMachine.GetUID())
		newCAPIMachine.SetCreationTimestamp(capiMachine.GetCreationTimestamp())
		newCAPIMachine.SetManagedFields(capiMachine.GetManagedFields())
		newCAPIMachine.SetResourceVersion(util.GetResourceVersion(client.Object(capiMachine)))
		// Needed to account for additional labels/annotations that might have been down-propagated in-place
		// from an authoritative CAPI MachineSet to its existing and non-authoritative child CAPI Machine.
		// ref: https://github.com/kubernetes-sigs/cluster-api/issues/7731
		newCAPIMachine.Labels = util.MergeMaps(capiMachine.Labels, newCAPIMachine.Labels)
		newCAPIMachine.Annotations = util.MergeMaps(capiMachine.Annotations, newCAPIMachine.Annotations)
		// Restore finalizers.
		newCAPIMachine.SetFinalizers(capiMachine.GetFinalizers())
	}

	newCAPIMachine.SetNamespace(r.CAPINamespace)
	newCAPIMachine.Spec.InfrastructureRef.Namespace = r.CAPINamespace
	newCAPIMachine.OwnerReferences = newCAPIOwnerReferences

	if len(newCAPIMachine.OwnerReferences) == 1 && newCAPIMachine.OwnerReferences[0].Kind == machineSetKind {
		// For CAPI Machine that is owned by a CAPI MachineSet we must set the capiv1beta1.MachineSetNameLabel
		// as this is what the CAPI machineset controller sets on the CAPI Machine when it creates it, an it is then later used
		// by other CAPI tooling for filtering purposes.
		// This check should be safe as in the above convertMAPIMachineOwnerReferencesToCAPI(), we make sure
		// there is only one owning MachineSet reference for a machine, if any.
		newCAPIMachine.Labels[capiv1beta1.MachineSetNameLabel] = format.MustFormatValue(newCAPIMachine.OwnerReferences[0].Name)
	}

	// Set the paused annotation on the new CAPI Machine, as we want to create it paused.
	annotations.AddAnnotations(newCAPIMachine, map[string]string{capiv1beta1.PausedAnnotation: ""})

	_, infraMachine, err := r.fetchCAPIInfraResources(ctx, newCAPIMachine)
	if err != nil && !apierrors.IsNotFound(err) {
		fetchErr := fmt.Errorf("failed to fetch Cluster API infra resources: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, fetchErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{fetchErr, condErr})
		}

		return ctrl.Result{}, fetchErr
	}

	if !util.IsNilObject(infraMachine) {
		newCAPIInfraMachine.SetGeneration(infraMachine.GetGeneration())
		newCAPIInfraMachine.SetUID(infraMachine.GetUID())
		newCAPIInfraMachine.SetCreationTimestamp(infraMachine.GetCreationTimestamp())
		newCAPIInfraMachine.SetManagedFields(infraMachine.GetManagedFields())
		newCAPIInfraMachine.SetResourceVersion(util.GetResourceVersion(infraMachine))
		// Needed to account for additional labels/annotations that might have been down-propagated in-place
		// from an authoritative CAPI MachineSet to its existing and non-authoritative child CAPI Machine.
		// ref: https://github.com/kubernetes-sigs/cluster-api/issues/7731
		newCAPIInfraMachine.SetLabels(util.MergeMaps(infraMachine.GetLabels(), newCAPIInfraMachine.GetLabels()))
		newCAPIInfraMachine.SetAnnotations(util.MergeMaps(infraMachine.GetAnnotations(), newCAPIInfraMachine.GetAnnotations()))
		// Restore finalizers.
		newCAPIInfraMachine.SetFinalizers(infraMachine.GetFinalizers())
	}

	newCAPIInfraMachine.SetNamespace(r.CAPINamespace)

	// Set the paused annotation on the new CAPI InfraMachine, as we want to create it paused.
	annotations.AddAnnotations(newCAPIInfraMachine, map[string]string{capiv1beta1.PausedAnnotation: ""})

	if result, err := r.createOrUpdateCAPIMachine(ctx, mapiMachine, capiMachine, newCAPIMachine); err != nil {
		return result, fmt.Errorf("unable to ensure Cluster API machine: %w", err)
	}

	newCAPIInfraMachine.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion:         capiv1beta1.GroupVersion.String(),
		Kind:               machineKind,
		Name:               newCAPIMachine.Name,
		UID:                newCAPIMachine.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}})

	result, syncronizationIsProgressing, err := r.createOrUpdateCAPIInfraMachine(ctx, mapiMachine, infraMachine, newCAPIInfraMachine)
	if err != nil {
		return result, fmt.Errorf("unable to ensure Cluster API infra machine: %w", err)
	}

	if syncronizationIsProgressing {
		return ctrl.Result{RequeueAfter: time.Second * 1}, r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionUnknown,
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

func (r *MachineSyncReconciler) convertCAPIToMAPIMachine(capiMachine *capiv1beta1.Machine, infraMachine client.Object, infraCluster client.Object) (*machinev1beta1.Machine, []string, error) {
	switch r.Platform {
	case configv1.AWSPlatformType:
		awsMachine, ok := infraMachine.(*capav1beta2.AWSMachine)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected AWSMachine, got %T", errUnexpectedInfraMachineType, infraMachine)
		}

		awsCluster, ok := infraCluster.(*capav1beta2.AWSCluster)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected AWSCluster, got %T", errUnexpectedInfraClusterType, infraCluster)
		}

		return capi2mapi.FromMachineAndAWSMachineAndAWSCluster(capiMachine, awsMachine, awsCluster).ToMachine() //nolint:wrapcheck
	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, r.Platform)
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

	if util.IsNilObject(infraMachine) {
		if err := r.Create(ctx, newCAPIInfraMachine); err != nil {
			logger.Error(err, "Failed to create Cluster API infra machine")
			createErr := fmt.Errorf("failed to create Cluster API infra machine: %w", err)

			if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToCreateCAPIInfraMachine, createErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, syncronizationIsProgressing, utilerrors.NewAggregate([]error{createErr, condErr})
			}

			return ctrl.Result{}, syncronizationIsProgressing, createErr
		}

		logger.Info("Successfully created Cluster API infra machine")

		return ctrl.Result{}, syncronizationIsProgressing, nil
	}

	capiInfraMachinesDiff, err := compareCAPIInfraMachines(r.Platform, infraMachine, newCAPIInfraMachine)
	if err != nil {
		logger.Error(err, "Failed to check Cluster API infra machine diff")
		updateErr := fmt.Errorf("failed to check Cluster API infra machine diff: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachine, updateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, syncronizationIsProgressing, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return ctrl.Result{}, syncronizationIsProgressing, updateErr
	}

	if len(capiInfraMachinesDiff) == 0 {
		logger.Info("No changes detected in Cluster API infra machine")
		return ctrl.Result{}, syncronizationIsProgressing, nil
	}

	logger.Info("Deleting the corresponding Cluster API infra machine as it is out of date, it will be recreated", "diff", fmt.Sprintf("%+v", capiInfraMachinesDiff))

	if err := r.Delete(ctx, infraMachine); err != nil {
		logger.Error(err, "Failed to delete Cluster API infra machine")

		deleteErr := fmt.Errorf("failed to delete Cluster API infra machine: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachine, deleteErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, syncronizationIsProgressing, utilerrors.NewAggregate([]error{deleteErr, condErr})
		}

		return ctrl.Result{}, syncronizationIsProgressing, deleteErr
	}

	// The outdated outdated CAPI infra machine has been deleted.
	// We will try and recreate an up-to-date one later.
	logger.Info("Successfully deleted outdated Cluster API infra machine")

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
			logger.Error(err, "Failed to create Cluster API machine")

			createErr := fmt.Errorf("failed to create Cluster API machine: %w", err)
			if condErr := r.applySynchronizedConditionWithPatch(
				ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToCreateCAPIMachine, createErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, utilerrors.NewAggregate([]error{createErr, condErr})
			}

			return ctrl.Result{}, createErr
		}

		logger.Info("Successfully created Cluster API machine")

		return ctrl.Result{}, nil
	}

	capiMachinesDiff := compareCAPIMachines(capiMachine, newCAPIMachine)

	if len(capiMachinesDiff) == 0 {
		logger.Info("No changes detected in Cluster API machine")
		return ctrl.Result{}, nil
	}

	logger.Info("Changes detected, updating Cluster API machine", "diff", fmt.Sprintf("%+v", capiMachinesDiff))

	if err := r.Update(ctx, newCAPIMachine); err != nil {
		logger.Error(err, "Failed to update Cluster API machine")

		updateErr := fmt.Errorf("failed to update Cluster API machine: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachine, updateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return ctrl.Result{}, updateErr
	}

	logger.Info("Successfully updated Cluster API machine")

	return ctrl.Result{}, nil
}

// createOrUpdateMAPIMachine creates a MAPI machine from a CAPI one, or updates
// if it exists and it is out of date.
func (r *MachineSyncReconciler) createOrUpdateMAPIMachine(ctx context.Context, mapiMachine *machinev1beta1.Machine, newMAPIMachine *machinev1beta1.Machine) (ctrl.Result, error) { //nolint:unparam
	logger := log.FromContext(ctx)

	if mapiMachine == nil {
		if err := r.Create(ctx, newMAPIMachine); err != nil {
			logger.Error(err, "Failed to create Machine API machine")
			return ctrl.Result{}, fmt.Errorf("failed to create Machine API machine: %w", err)
		}

		logger.Info("Successfully created Machine API machine")

		return ctrl.Result{}, nil
	}

	mapiMachinesDiff, err := compareMAPIMachines(mapiMachine, newMAPIMachine)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to compare Machine API machines: %w", err)
	}

	if len(mapiMachinesDiff) == 0 {
		logger.Info("No changes detected in Machine API machine")
		return ctrl.Result{}, nil
	}

	logger.Info("Changes detected, updating Machine API machine", "diff", mapiMachinesDiff)

	if err := r.Update(ctx, newMAPIMachine); err != nil {
		logger.Error(err, "Failed to update Machine API machine")

		updateErr := fmt.Errorf("failed to update Machine API machine: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateMAPIMachine, updateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return ctrl.Result{}, updateErr
	}

	logger.Info("Successfully updated Machine API machine")

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
	logger.V(4).WithName("shouldMirrorCAPIMachineToMAPIMachine").
		Info("Checking if Cluster API machine should be mirrored", "machine", machine.GetName())

	// Check if the CAPI machine has an ownerReference that points to a CAPI machineset.
	for _, ref := range machine.ObjectMeta.OwnerReferences {
		if ref.Kind != machineSetKind || ref.APIVersion != capiv1beta1.GroupVersion.String() {
			continue
		}

		logger.V(4).Info("Cluster API machine is owned by a Cluster API machineset", "machine", machine.GetName(), "machineset", ref.Name)

		// Checks if the CAPI machineset has a MAPI machineset mirror (same name) in MAPI namespace.
		key := client.ObjectKey{
			Namespace: r.MAPINamespace,
			Name:      ref.Name, // same name as the CAPI machineset.
		}
		mapiMachineSet := &machinev1beta1.MachineSet{}

		if err := r.Get(ctx, key, mapiMachineSet); apierrors.IsNotFound(err) {
			logger.V(4).Info("Machine API machineset mirror not found for the Cluster API machineset, nothing to do", "machine", machine.GetName(), "machineset", ref.Name)

			return false, nil
		} else if err != nil {
			logger.Error(err, "Failed to get Machine API machineset mirror")

			return false, fmt.Errorf("failed to get Machine API machineset: %w", err)
		}

		return true, nil
	}

	logger.V(4).Info("Cluster API machine is not owned by a machineset, nothing to do", "machine", machine.GetName())

	return false, nil
}

// convertMAPIMachineOwnerReferencesToCAPI converts MAPI machine ownerReferences to CAPI ownerReferences.
func (r *MachineSyncReconciler) convertMAPIMachineOwnerReferencesToCAPI(ctx context.Context, mapiMachine *machinev1beta1.Machine) ([]metav1.OwnerReference, error) {
	capiOwnerReferences := []metav1.OwnerReference{}

	if len(mapiMachine.OwnerReferences) == 0 {
		return capiOwnerReferences, nil
	}

	if len(mapiMachine.OwnerReferences) > 1 {
		return nil, field.TooMany(field.NewPath("metadata", "ownerReferences"), len(mapiMachine.OwnerReferences), 1)
	}

	mapiOwnerReference := mapiMachine.OwnerReferences[0]
	if mapiOwnerReference.Kind == cpmsKind {
		return nil, field.Invalid(field.NewPath("metadata", "ownerReferences"), mapiMachine.OwnerReferences, errUnsupportedCPMSOwnedMachineConversion.Error())
	}

	if mapiOwnerReference.Kind != machineSetKind || mapiOwnerReference.APIVersion != machinev1beta1.GroupVersion.String() {
		return nil, field.Invalid(field.NewPath("metadata", "ownerReferences"), mapiMachine.OwnerReferences, errUnsuportedOwnerKindForConversion.Error())
	}

	key := types.NamespacedName{
		Namespace: r.CAPINamespace,
		Name:      mapiOwnerReference.Name,
	}

	capiMachineSet := capiv1beta1.MachineSet{}
	// Get the CAPI machineSet with same name as MAPI machineSet
	if err := r.Get(ctx, key, &capiMachineSet); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("could not find Cluster API machine set: %w", err)
		} else {
			return nil, fmt.Errorf("error getting Cluster API machine set: %w", err)
		}
	}

	capiOwnerReference := metav1.OwnerReference{
		Kind:               capiMachineSet.Kind,
		APIVersion:         capiMachineSet.APIVersion,
		Name:               capiMachineSet.Name,
		Controller:         mapiOwnerReference.Controller,
		BlockOwnerDeletion: mapiOwnerReference.BlockOwnerDeletion,
		UID:                capiMachineSet.UID,
	}

	capiOwnerReferences = append(capiOwnerReferences, capiOwnerReference)

	return capiOwnerReferences, nil
}

// convertCAPIMachineOwnerReferencesToMAPI converts CAPI machine ownerReferences to MAPI ownerReferences.
func (r *MachineSyncReconciler) convertCAPIMachineOwnerReferencesToMAPI(ctx context.Context, capiMachine *capiv1beta1.Machine) ([]metav1.OwnerReference, error) {
	mapiOwnerReferences := []metav1.OwnerReference{}

	if len(capiMachine.OwnerReferences) == 0 {
		return mapiOwnerReferences, nil
	}

	if len(capiMachine.OwnerReferences) > 1 {
		return nil, field.TooMany(field.NewPath("metadata", "ownerReferences"), len(capiMachine.OwnerReferences), 1)
	}

	capiOwnerReference := capiMachine.OwnerReferences[0]
	if capiOwnerReference.Kind != machineSetKind || capiOwnerReference.APIVersion != capiv1beta1.GroupVersion.String() {
		return nil, field.Invalid(field.NewPath("metadata", "ownerReferences"), capiMachine.OwnerReferences, errUnsuportedOwnerKindForConversion.Error())
	}

	key := types.NamespacedName{
		Namespace: r.MAPINamespace,
		Name:      capiOwnerReference.Name,
	}

	mapiMachineSet := machinev1beta1.MachineSet{}
	// Get the MAPI machineSet with same name as CAPI machineSet
	if err := r.Get(ctx, key, &mapiMachineSet); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("could not find Machine API machine set: %w", err)
		} else {
			return nil, fmt.Errorf("error getting Machine API machine set: %w", err)
		}
	}

	mapiOwnerReference := metav1.OwnerReference{
		Kind:               mapiMachineSet.Kind,
		APIVersion:         mapiMachineSet.APIVersion,
		Name:               mapiMachineSet.Name,
		Controller:         capiOwnerReference.Controller,
		BlockOwnerDeletion: capiOwnerReference.BlockOwnerDeletion,
		UID:                mapiMachineSet.UID,
	}

	mapiOwnerReferences = append(mapiOwnerReferences, mapiOwnerReference)

	return mapiOwnerReferences, nil
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
		return nil, nil, fmt.Errorf("unable to devise Cluster API infra resources: %w", err)
	}

	if err := r.Get(ctx, infraClusterKey, infraCluster); err != nil {
		return nil, nil, fmt.Errorf("failed to get Cluster API infrastructure cluster: %w", err)
	}

	if err := r.Get(ctx, infraMachineKey, infraMachine); err != nil {
		return nil, nil, fmt.Errorf("failed to get Cluster API infrastructure machine: %w", err)
	}

	return infraCluster, infraMachine, nil
}

// compareCAPIMachines compares CAPI machines a and b, and returns a list of differences, or none if there are none.
func compareCAPIMachines(capiMachine1, capiMachine2 *capiv1beta1.Machine) map[string]any {
	diff := make(map[string]any)

	if diffSpec := deep.Equal(capiMachine1.Spec, capiMachine2.Spec); len(diffSpec) > 0 {
		diff[".spec"] = diffSpec
	}

	if diffObjectMeta := util.ObjectMetaEqual(capiMachine1.ObjectMeta, capiMachine2.ObjectMeta); len(diffObjectMeta) > 0 {
		diff[".metadata"] = diffObjectMeta
	}

	return diff
}

// compareMAPIMachines compares MAPI machines a and b, and returns a list of differences, or none if there are none.
func compareMAPIMachines(a, b *machinev1beta1.Machine) (map[string]any, error) {
	diff := make(map[string]any)

	ps1, err := mapi2capi.AWSProviderSpecFromRawExtension(a.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("unable to parse first Machine API machine set providerSpec: %w", err)
	}

	ps2, err := mapi2capi.AWSProviderSpecFromRawExtension(a.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("unable to parse second Machine API machine set providerSpec: %w", err)
	}

	if diffProviderSpec := deep.Equal(ps1, ps2); len(diffProviderSpec) > 0 {
		diff[".providerSpec"] = diffProviderSpec
	}

	// Remove the providerSpec from the Spec as we've already compared them.
	aCopy := a.DeepCopy()
	aCopy.Spec.ProviderSpec.Value = nil

	bCopy := b.DeepCopy()
	bCopy.Spec.ProviderSpec.Value = nil

	if diffSpec := deep.Equal(aCopy.Spec, bCopy.Spec); len(diffSpec) > 0 {
		diff[".spec"] = diffSpec
	}

	if diffObjectMeta := util.ObjectMetaEqual(aCopy.ObjectMeta, bCopy.ObjectMeta); len(diffObjectMeta) > 0 {
		diff[".metadata"] = diffObjectMeta
	}

	return diff, nil
}

// compareCAPIInfraMachines compares CAPI infra machines a and b, and returns a list of differences, or none if there are none.
func compareCAPIInfraMachines(platform configv1.PlatformType, infraMachine1, infraMachine2 client.Object) (map[string]any, error) {
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

		diff := make(map[string]any)
		if diffSpec := deep.Equal(typedInfraMachine1.Spec, typedinfraMachine2.Spec); len(diffSpec) > 0 {
			diff[".spec"] = diffSpec
		}

		if diffMetadata := util.ObjectMetaEqual(typedInfraMachine1.ObjectMeta, typedinfraMachine2.ObjectMeta); len(diffMetadata) > 0 {
			diff[".metadata"] = diffMetadata
		}

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

		diff := make(map[string]any)
		if diffSpec := deep.Equal(typedInfraMachine1.Spec, typedinfraMachine2.Spec); len(diffSpec) > 0 {
			diff[".spec"] = diffSpec
		}

		if diffMetadata := util.ObjectMetaEqual(typedInfraMachine1.ObjectMeta, typedinfraMachine2.ObjectMeta); len(diffMetadata) > 0 {
			diff[".metadata"] = diffMetadata
		}

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
		return fmt.Errorf("failed to patch Machine API machine status with synchronized condition: %w", err)
	}

	return nil
}

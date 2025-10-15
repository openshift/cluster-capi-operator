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
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/synccommon"
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
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	conditionsv1beta2 "sigs.k8s.io/cluster-api/util/conditions/v1beta2"
	"sigs.k8s.io/cluster-api/util/labels/format"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
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

	capiNamespace                  string = "openshift-cluster-api"
	machineKind                    string = "Machine"
	machineSetKind                 string = "MachineSet"
	cpmsKind                       string = "ControlPlaneMachineSet"
	controllerName                 string = "MachineSyncController"
	mapiNamespace                  string = "openshift-machine-api"
	capiInfraCommonFinalizerSuffix string = ".cluster.x-k8s.io"

	messageSuccessfullySynchronizedCAPItoMAPI = "Successfully synchronized CAPI Machine to MAPI"
	messageSuccessfullySynchronizedMAPItoCAPI = "Successfully synchronized MAPI Machine to CAPI"
	progressingToSynchronizeMAPItoCAPI        = "Progressing to synchronize MAPI Machine to CAPI"

	// SyncFinalizer is the finalizer set to coordinate deletion of mirrored
	// resources.
	SyncFinalizer = "sync.machine.openshift.io/finalizer"
)

var (
	// errAssertingCAPIAWSMachine is returned when we encounter an issue asserting a client.Object into an AWSMachine.
	errAssertingCAPIAWSMachine = errors.New("error asserting the Cluster API AWSMachine object")

	// errAssertingCAPIOpenStackMachine is returned when we encounter an issue asserting a client.Object into an OpenStackVSMachine.
	errAssertingCAPIOpenStackMachine = errors.New("error asserting the Cluster API OpenStackMachine object")

	// errAssertingCAPIBMPowerVSMachine is returned when we encounter an issue asserting a client.Object into an IBMPowerVSMachine.
	errAssertingCAPIIBMPowerVSMachine = errors.New("error asserting the Cluster API IBMPowerVSMachine object")

	// errCAPIMachineNotFound is returned when the AuthoritativeAPI is set to CAPI on the MAPI machine,
	// but we can't find the CAPI machine.
	//lint:ignore ST1005 Cluster API is a name.
	//nolint:stylecheck
	errCAPIMachineNotFound = errors.New("Cluster API machine not found")

	// errPlatformNotSupported is returned when the platform is not supported.
	errPlatformNotSupported = errors.New("error determining InfraMachine type, platform not supported")

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
	infraMachine, _, err := controllers.InitInfraMachineAndInfraClusterFromProvider(r.Platform)
	if err != nil {
		return fmt.Errorf("failed to get infrastructure machine from Provider: %w", err)
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
	logger := logf.FromContext(ctx, "namespace", req.Namespace, "name", req.Name)

	logger.V(1).Info("Reconciling machine")
	defer logger.V(1).Info("Finished reconciling machine")

	var mapiMachineNotFound, capiMachineNotFound bool

	// Get the MAPI Machine.
	mapiMachine := &mapiv1beta1.Machine{}
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
	capiMachine := &clusterv1.Machine{}
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

	authoritativeAPI := mapiMachine.Status.AuthoritativeAPI

	switch {
	case authoritativeAPI == mapiv1beta1.MachineAuthorityMachineAPI:
		return r.reconcileMAPIMachinetoCAPIMachine(ctx, mapiMachine, capiMachine)
	case authoritativeAPI == mapiv1beta1.MachineAuthorityClusterAPI && !capiMachineNotFound:
		return r.reconcileCAPIMachinetoMAPIMachine(ctx, capiMachine, mapiMachine)
	case authoritativeAPI == mapiv1beta1.MachineAuthorityClusterAPI && capiMachineNotFound:
		return r.reconcileMAPIMachinetoCAPIMachine(ctx, mapiMachine, capiMachine)
	case authoritativeAPI == mapiv1beta1.MachineAuthorityMigrating:
		logger.Info("Machine currently migrating", "machine", mapiMachine.GetName())
		return ctrl.Result{}, nil
	case authoritativeAPI == "":
		logger.Info("Machine status.authoritativeAPI is empty, will check again later", "AuthoritativeAPI", mapiMachine.Status.AuthoritativeAPI)
		return ctrl.Result{}, nil
	default:
		logger.Info("Machine status.authoritativeAPI has unexpected value", "AuthoritativeAPI", mapiMachine.Status.AuthoritativeAPI)
		return ctrl.Result{}, nil
	}
}

// reconcileCAPIMachinetoMAPIMachine reconciles a CAPI Machine to a MAPI Machine.
//
//nolint:gocognit,funlen, cyclop
func (r *MachineSyncReconciler) reconcileCAPIMachinetoMAPIMachine(ctx context.Context, sourceCAPIMachine *clusterv1.Machine, existingMAPIMachine *mapiv1beta1.Machine) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	if sourceCAPIMachine == nil {
		logger.Error(errCAPIMachineNotFound, "machine", existingMAPIMachine.Name)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, existingMAPIMachine, corev1.ConditionFalse, reasonCAPIMachineNotFound, errCAPIMachineNotFound.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{errCAPIMachineNotFound, condErr})
		}

		return ctrl.Result{}, errCAPIMachineNotFound
	}

	infraCluster, infraMachine, err := r.fetchCAPIInfraResources(ctx, sourceCAPIMachine)
	if err != nil {
		fetchErr := fmt.Errorf("failed to fetch Cluster API infra resources: %w", err)

		if existingMAPIMachine == nil {
			r.Recorder.Event(sourceCAPIMachine, corev1.EventTypeWarning, "SynchronizationWarning", fetchErr.Error())
			return ctrl.Result{}, fetchErr
		}

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, existingMAPIMachine, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, fetchErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{fetchErr, condErr})
		}

		return ctrl.Result{}, fetchErr
	}

	if shouldRequeue, err := r.reconcileCAPItoMAPIMachineDeletion(ctx, sourceCAPIMachine, infraMachine, existingMAPIMachine); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile Cluster API to Machine API machine deletion: %w", err)
	} else if shouldRequeue {
		return ctrl.Result{}, nil
	}

	if shouldRequeue, err := r.ensureSyncFinalizer(ctx, existingMAPIMachine, sourceCAPIMachine, infraMachine); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure sync finalizer: %w", err)
	} else if shouldRequeue {
		return ctrl.Result{}, nil
	}

	newMAPIOwnerReferences, err := r.convertCAPIMachineOwnerReferencesToMAPI(ctx, sourceCAPIMachine)
	//nolint:nestif
	if err != nil {
		var fe *field.Error
		if errors.As(err, &fe) {
			if existingMAPIMachine != nil {
				if condErr := r.applySynchronizedConditionWithPatch(ctx, existingMAPIMachine, corev1.ConditionFalse, reasonFailedToConvertCAPIMachineToMAPI, fe.Detail, nil); condErr != nil {
					return ctrl.Result{}, utilerrors.NewAggregate([]error{err, condErr})
				}
			}

			logger.Error(err, "unable to convert Cluster API machine to Machine API, unsupported owner reference in conversion")

			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to convert Cluster API machine owner references to Machine API: %w", err)
	}

	convertedMAPIMachine, warns, err := r.convertCAPIToMAPIMachine(sourceCAPIMachine, infraMachine, infraCluster)
	if err != nil {
		conversionErr := fmt.Errorf("failed to convert Cluster API machine to Machine API machine: %w", err)

		if existingMAPIMachine == nil {
			r.Recorder.Event(sourceCAPIMachine, corev1.EventTypeWarning, "SynchronizationWarning", conversionErr.Error())
			return ctrl.Result{}, conversionErr
		}

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, existingMAPIMachine, corev1.ConditionFalse, reasonFailedToConvertCAPIMachineToMAPI, conversionErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{conversionErr, condErr})
		}

		return ctrl.Result{}, conversionErr
	}

	for _, warning := range warns {
		logger.Info("Warning during conversion", "warning", warning)
		r.Recorder.Event(existingMAPIMachine, corev1.EventTypeWarning, "ConversionWarning", warning)
	}

	convertedMAPIMachine.SetNamespace(r.MAPINamespace)
	convertedMAPIMachine.SetOwnerReferences(newMAPIOwnerReferences)

	if existingMAPIMachine != nil {
		convertedMAPIMachine.SetResourceVersion(util.GetResourceVersion(existingMAPIMachine))
		// Restore authoritativeness to the current one.
		convertedMAPIMachine.Spec.AuthoritativeAPI = existingMAPIMachine.Spec.AuthoritativeAPI
		// Restore finalizers to the current one.
		convertedMAPIMachine.ObjectMeta.Finalizers = existingMAPIMachine.Finalizers
	} else {
		// If there is no existing MAPI machine it means we are creating a MAPI machine
		// from scratch from CAPI one, hence set the authoritativeness for it to Cluster API.
		convertedMAPIMachine.Spec.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
	}

	if result, err := r.createOrUpdateMAPIMachine(ctx, existingMAPIMachine, convertedMAPIMachine); err != nil {
		createUpdateErr := fmt.Errorf("unable to ensure Machine API machine: %w", err)

		if existingMAPIMachine == nil {
			r.Recorder.Event(sourceCAPIMachine, corev1.EventTypeWarning, "SynchronizationWarning", createUpdateErr.Error())
			return ctrl.Result{}, createUpdateErr
		}

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, existingMAPIMachine, corev1.ConditionFalse, reasonFailedToConvertCAPIMachineToMAPI, createUpdateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{createUpdateErr, condErr})
		}

		return result, createUpdateErr
	}

	return ctrl.Result{}, r.applySynchronizedConditionWithPatch(ctx, convertedMAPIMachine, corev1.ConditionTrue,
		controllers.ReasonResourceSynchronized, messageSuccessfullySynchronizedCAPItoMAPI, &sourceCAPIMachine.Generation)
}

// reconcileMAPIMachinetoCAPIMachine a MAPI Machine to a CAPI Machine.
//
// it assumes the mapiMachine passed is not nil, as the switch above currently
// enforces this.
//
//nolint:funlen, cyclop, gocognit
func (r *MachineSyncReconciler) reconcileMAPIMachinetoCAPIMachine(ctx context.Context, sourceMAPIMachine *mapiv1beta1.Machine, existingCAPIMachine *clusterv1.Machine) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	authoritativeAPI := sourceMAPIMachine.Status.AuthoritativeAPI

	if authoritativeAPI == mapiv1beta1.MachineAuthorityClusterAPI {
		logger.Info("AuthoritativeAPI is set to Cluster API, but no Cluster API machine exists. Running an initial Machine API to Cluster API sync")
	}

	_, existingInfraMachine, err := r.fetchCAPIInfraResources(ctx, existingCAPIMachine)
	if err != nil {
		fetchErr := fmt.Errorf("failed to fetch Cluster API infra resources: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, sourceMAPIMachine, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, fetchErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{fetchErr, condErr})
		}

		return ctrl.Result{}, fetchErr
	}

	if shouldRequeue, err := r.reconcileMAPItoCAPIMachineDeletion(ctx, sourceMAPIMachine, existingCAPIMachine, existingInfraMachine); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile Machine API to Cluster API machine deletion: %w", err)
	} else if shouldRequeue {
		return ctrl.Result{}, nil
	}

	if shouldRequeue, err := r.ensureSyncFinalizer(ctx, sourceMAPIMachine, existingCAPIMachine, existingInfraMachine); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure sync finalizer: %w", err)
	} else if shouldRequeue {
		return ctrl.Result{}, nil
	}

	convertedCAPIOwnerReferences, err := r.convertMAPIMachineOwnerReferencesToCAPI(ctx, sourceMAPIMachine)
	//nolint:nestif
	if err != nil {
		var fe *field.Error
		if errors.As(err, &fe) {
			if condErr := r.applySynchronizedConditionWithPatch(ctx, sourceMAPIMachine, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineToCAPI, fe.Detail, nil); condErr != nil {
				return ctrl.Result{}, utilerrors.NewAggregate([]error{err, condErr})
			}

			if fe.Detail == errUnsupportedCPMSOwnedMachineConversion.Error() {
				logger.Info("Not converting control plane Machine. Conversion of Machine API machines owned by control plane machine set is currently not supported")
				return ctrl.Result{}, nil
			}

			logger.Error(err, "unable to convert Machine API machine to Cluster API, unsupported owner reference in conversion")

			return ctrl.Result{}, nil
		}

		if condErr := r.applySynchronizedConditionWithPatch(ctx, sourceMAPIMachine, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineToCAPI, fmt.Errorf("failed to convert Machine API machine owner references to Cluster API: %w", err).Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{err, condErr})
		}

		return ctrl.Result{}, fmt.Errorf("failed to convert Machine API machine owner references to Cluster API: %w", err)
	}

	if err := r.validateMAPIToCAPIPlatfromSpecifics(ctx, sourceMAPIMachine); err != nil {
		conversionErr := fmt.Errorf("failed to convert Machine API machine to Cluster API machine: %w", err)
		if condErr := r.applySynchronizedConditionWithPatch(ctx, sourceMAPIMachine, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineToCAPI, conversionErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{conversionErr, condErr})
		}

		return ctrl.Result{}, conversionErr
	}

	convertedCAPIMachine, convertedCAPIInfraMachine, warns, err := r.convertMAPIToCAPIMachine(sourceMAPIMachine)
	if err != nil {
		conversionErr := fmt.Errorf("failed to convert Machine API machine to Cluster API machine: %w", err)
		if condErr := r.applySynchronizedConditionWithPatch(ctx, sourceMAPIMachine, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineToCAPI, conversionErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{conversionErr, condErr})
		}

		return ctrl.Result{}, conversionErr
	}

	for _, warning := range warns {
		logger.Info("Warning during conversion", "warning", warning)
		r.Recorder.Event(sourceMAPIMachine, corev1.EventTypeWarning, "ConversionWarning", warning)
	}

	if existingCAPIMachine != nil {
		convertedCAPIMachine.SetGeneration(existingCAPIMachine.GetGeneration())
		convertedCAPIMachine.SetUID(existingCAPIMachine.GetUID())
		convertedCAPIMachine.SetCreationTimestamp(existingCAPIMachine.GetCreationTimestamp())
		convertedCAPIMachine.SetManagedFields(existingCAPIMachine.GetManagedFields())
		convertedCAPIMachine.SetResourceVersion(util.GetResourceVersion(client.Object(existingCAPIMachine)))
		// Needed to account for additional labels/annotations that might have been down-propagated in-place
		// from an authoritative CAPI MachineSet to its existing and non-authoritative child CAPI Machine.
		// ref: https://github.com/kubernetes-sigs/cluster-api/issues/7731
		convertedCAPIMachine.Labels = util.MergeMaps(existingCAPIMachine.Labels, convertedCAPIMachine.Labels)
		convertedCAPIMachine.Annotations = util.MergeMaps(existingCAPIMachine.Annotations, convertedCAPIMachine.Annotations)
		// Restore finalizers.
		convertedCAPIMachine.SetFinalizers(existingCAPIMachine.GetFinalizers())
	}

	convertedCAPIMachine.SetNamespace(r.CAPINamespace)
	convertedCAPIMachine.Spec.InfrastructureRef.Namespace = r.CAPINamespace
	convertedCAPIMachine.OwnerReferences = convertedCAPIOwnerReferences

	if len(convertedCAPIMachine.OwnerReferences) == 1 && convertedCAPIMachine.OwnerReferences[0].Kind == machineSetKind {
		// For CAPI Machine that is owned by a CAPI MachineSet we must set the clusterv1.MachineSetNameLabel
		// as this is what the CAPI machineset controller sets on the CAPI Machine when it creates it, an it is then later used
		// by other CAPI tooling for filtering purposes.
		// This check should be safe as in the above convertMAPIMachineOwnerReferencesToCAPI(), we make sure
		// there is only one owning MachineSet reference for a machine, if any.
		convertedCAPIMachine.Labels[clusterv1.MachineSetNameLabel] = format.MustFormatValue(convertedCAPIMachine.OwnerReferences[0].Name)
	}

	if authoritativeAPI == mapiv1beta1.MachineAuthorityMachineAPI {
		// Set the paused annotation on the new CAPI Machine, if the authoritativeAPI is Machine API,
		// as we want the new CAPI Machine to be initially paused when the MAPI Machine is the authoritative one.
		// For the other case instead (authoritativeAPI == mapiv1beta1.MachineAuthorityClusterAPI),
		// when the new CAPI Machine that is being created is also expected to be the authority
		// (i.e. in cases where the MAPI Machine is created as .spec.authoritativeAPI: ClusterAPI), we do not want to create it paused.
		annotations.AddAnnotations(convertedCAPIMachine, map[string]string{clusterv1.PausedAnnotation: ""})
	}

	if !util.IsNilObject(existingInfraMachine) {
		convertedCAPIInfraMachine.SetGeneration(existingInfraMachine.GetGeneration())
		convertedCAPIInfraMachine.SetUID(existingInfraMachine.GetUID())
		convertedCAPIInfraMachine.SetCreationTimestamp(existingInfraMachine.GetCreationTimestamp())
		convertedCAPIInfraMachine.SetManagedFields(existingInfraMachine.GetManagedFields())
		convertedCAPIInfraMachine.SetResourceVersion(util.GetResourceVersion(existingInfraMachine))
		// Needed to account for additional labels/annotations that might have been down-propagated in-place
		// from an authoritative CAPI MachineSet to its existing and non-authoritative child CAPI Machine.
		// ref: https://github.com/kubernetes-sigs/cluster-api/issues/7731
		convertedCAPIInfraMachine.SetLabels(util.MergeMaps(existingInfraMachine.GetLabels(), convertedCAPIInfraMachine.GetLabels()))
		convertedCAPIInfraMachine.SetAnnotations(util.MergeMaps(existingInfraMachine.GetAnnotations(), convertedCAPIInfraMachine.GetAnnotations()))
		// Restore finalizers.
		convertedCAPIInfraMachine.SetFinalizers(existingInfraMachine.GetFinalizers())
	}

	convertedCAPIInfraMachine.SetNamespace(r.CAPINamespace)

	if authoritativeAPI == mapiv1beta1.MachineAuthorityMachineAPI {
		// Set the paused annotation on the new CAPI Infra Machine, if the authoritativeAPI is Machine API,
		// as we want the new CAPI Infra Machine to be initially paused when the MAPI Machine is the authoritative one.
		// For the other case instead (authoritativeAPI == mapiv1beta1.MachineAuthorityClusterAPI),
		// when the new CAPI Infra Machine that is being created is also expected to be the authority
		// (i.e. in cases where the MAPI Machine is created as .spec.authoritativeAPI: ClusterAPI), we do not want to create it paused.
		annotations.AddAnnotations(convertedCAPIInfraMachine, map[string]string{clusterv1.PausedAnnotation: ""})
	}

	// Create or update the CAPI machine.
	existingCAPIMachine, err = r.createOrUpdateCAPIMachine(ctx, sourceMAPIMachine, existingCAPIMachine, convertedCAPIMachine)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure Cluster API machine: %w", err)
	}

	convertedCAPIInfraMachine.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion:         clusterv1.GroupVersion.String(),
		Kind:               machineKind,
		Name:               existingCAPIMachine.Name,
		UID:                existingCAPIMachine.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}})

	result, syncronizationIsProgressing, err := r.createOrUpdateCAPIInfraMachine(ctx, sourceMAPIMachine, existingInfraMachine, convertedCAPIInfraMachine)
	if err != nil {
		return result, fmt.Errorf("unable to ensure Cluster API infra machine: %w", err)
	}

	if syncronizationIsProgressing {
		return ctrl.Result{RequeueAfter: time.Second * 1}, r.applySynchronizedConditionWithPatch(ctx, sourceMAPIMachine, corev1.ConditionUnknown,
			reasonProgressingToCreateCAPIInfraMachine, progressingToSynchronizeMAPItoCAPI, nil)
	}

	return ctrl.Result{}, r.applySynchronizedConditionWithPatch(ctx, sourceMAPIMachine, corev1.ConditionTrue,
		controllers.ReasonResourceSynchronized, messageSuccessfullySynchronizedMAPItoCAPI, &sourceMAPIMachine.Generation)
}

// validateMAPIToCAPIPlatfromSpecifics verifies that shared CAPI resources are compatible before converting from MAPI -> CAPI.
func (r *MachineSyncReconciler) validateMAPIToCAPIPlatfromSpecifics(ctx context.Context, mapiMachine *mapiv1beta1.Machine) error {
	switch r.Platform {
	case configv1.AWSPlatformType:
		return r.validateMachineAWSLoadBalancers(ctx, mapiMachine)
	default:
		return nil
	}
}

// convertMAPIToCAPIMachine converts a MAPI Machine to a CAPI Machine, selecting the correct converter based on the platform.
func (r *MachineSyncReconciler) convertMAPIToCAPIMachine(mapiMachine *mapiv1beta1.Machine) (*clusterv1.Machine, client.Object, []string, error) {
	switch r.Platform {
	case configv1.AWSPlatformType:
		return mapi2capi.FromAWSMachineAndInfra(mapiMachine, r.Infra).ToMachineAndInfrastructureMachine() //nolint:wrapcheck
	case configv1.OpenStackPlatformType:
		return mapi2capi.FromOpenStackMachineAndInfra(mapiMachine, r.Infra).ToMachineAndInfrastructureMachine() //nolint:wrapcheck
	case configv1.PowerVSPlatformType:
		return mapi2capi.FromPowerVSMachineAndInfra(mapiMachine, r.Infra).ToMachineAndInfrastructureMachine() //nolint:wrapcheck
	default:
		return nil, nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, r.Platform)
	}
}

func (r *MachineSyncReconciler) convertCAPIToMAPIMachine(capiMachine *clusterv1.Machine, infraMachine client.Object, infraCluster client.Object) (*mapiv1beta1.Machine, []string, error) {
	switch r.Platform {
	case configv1.AWSPlatformType:
		awsMachine, ok := infraMachine.(*awsv1.AWSMachine)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected AWSMachine, got %T", errUnexpectedInfraMachineType, infraMachine)
		}

		awsCluster, ok := infraCluster.(*awsv1.AWSCluster)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected AWSCluster, got %T", errUnexpectedInfraClusterType, infraCluster)
		}

		return capi2mapi.FromMachineAndAWSMachineAndAWSCluster(capiMachine, awsMachine, awsCluster).ToMachine() //nolint:wrapcheck
	case configv1.OpenStackPlatformType:
		openStackMachine, ok := infraMachine.(*openstackv1.OpenStackMachine)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected OpenStackMachine, got %T", errUnexpectedInfraMachineType, infraMachine)
		}

		openStackCluster, ok := infraCluster.(*openstackv1.OpenStackCluster)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected OpenStackCluster, got %T", errUnexpectedInfraClusterType, infraCluster)
		}

		return capi2mapi.FromMachineAndOpenStackMachineAndOpenStackCluster(capiMachine, openStackMachine, openStackCluster).ToMachine() //nolint:wrapcheck
	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, r.Platform)
	}
}

// createOrUpdateCAPIInfraMachine creates a CAPI infra machine from a MAPI machine, or updates if it exists and it is out of date.
//
//nolint:funlen,unparam
func (r *MachineSyncReconciler) createOrUpdateCAPIInfraMachine(ctx context.Context, mapiMachine *mapiv1beta1.Machine, infraMachine client.Object, newCAPIInfraMachine client.Object) (ctrl.Result, bool, error) {
	logger := logf.FromContext(ctx)
	// This variable tracks whether or not we are still progressing
	// towards syncronizing the MAPI machine with the CAPI infra machine.
	// It is then passed up the stack so the syncronized condition can be set accordingly.
	syncronizationIsProgressing := false

	alreadyExists := false

	//nolint: nestif
	if util.IsNilObject(infraMachine) {
		if err := r.Create(ctx, newCAPIInfraMachine); err != nil && !apierrors.IsAlreadyExists(err) {
			logger.Error(err, "Failed to create Cluster API infra machine")
			createErr := fmt.Errorf("failed to create Cluster API infra machine: %w", err)

			if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToCreateCAPIInfraMachine, createErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, syncronizationIsProgressing, utilerrors.NewAggregate([]error{createErr, condErr})
			}

			return ctrl.Result{}, syncronizationIsProgressing, createErr
		} else if apierrors.IsAlreadyExists(err) {
			// this handles the case where the CAPI Machine is not present, so we can't resolve the
			// infraMachine ref from it - but the InfraMachine exists. (e.g a user deletes the CAPI machine manually).
			//  This would lead to the call to fetchCAPIInfraResources returning nil for the infraMachine.
			alreadyExists = true
		} else {
			logger.Info("Successfully created Cluster API infra machine")

			return ctrl.Result{}, syncronizationIsProgressing, nil
		}
	}

	if alreadyExists {
		if err := r.Get(ctx, client.ObjectKeyFromObject(newCAPIInfraMachine), infraMachine); err != nil {
			logger.Error(err, "Failed to get Cluster API infra machine")
			getErr := fmt.Errorf("failed to get Cluster API infra machine: %w", err)

			if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, getErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, syncronizationIsProgressing, utilerrors.NewAggregate([]error{getErr, condErr})
			}

			return ctrl.Result{}, syncronizationIsProgressing, getErr
		}
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

	// Remove finalizers from the deleting CAPI infraMachine, it is not authoritative.
	infraMachine.SetFinalizers(nil)

	if err := r.Update(ctx, infraMachine); err != nil {
		logger.Error(err, "Failed to remove finalizer for deleting Cluster API infra machine")

		deleteErr := fmt.Errorf("failed to remove finalizer for deleting Cluster API infra machine: %w", err)

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

// ensureCAPIMachine creates a new CAPI machine if one doesn't exist.
func (r *MachineSyncReconciler) ensureCAPIMachine(ctx context.Context, sourceMAPIMachine *mapiv1beta1.Machine, existingCAPIMachine, convertedCAPIMachine *clusterv1.Machine) (*clusterv1.Machine, error) {
	// If there is an existing CAPI machine, no need to create one.
	if existingCAPIMachine != nil {
		return existingCAPIMachine, nil
	}

	logger := logf.FromContext(ctx)

	// Set the existing CAPI machine to the converted CAPI machine.
	existingCAPIMachine = convertedCAPIMachine.DeepCopy()

	if err := r.Create(ctx, existingCAPIMachine); err != nil {
		logger.Error(err, "Failed to create Cluster API machine")

		createErr := fmt.Errorf("failed to create Cluster API machine: %w", err)
		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, sourceMAPIMachine, corev1.ConditionFalse, reasonFailedToCreateCAPIMachine, createErr.Error(), nil); condErr != nil {
			return existingCAPIMachine, utilerrors.NewAggregate([]error{createErr, condErr})
		}

		return nil, createErr
	}

	logger.Info("Successfully created Cluster API machine", "name", existingCAPIMachine.Name)

	return existingCAPIMachine, nil
}

// ensureCAPIMachineSpecUpdated updates the Cluster API machine if changes are detected to the spec, metadata or provider spec.
func (r *MachineSyncReconciler) ensureCAPIMachineSpecUpdated(ctx context.Context, mapiMachine *mapiv1beta1.Machine, capiMachinesDiff map[string]any, updatedCAPIMachine *clusterv1.Machine) (bool, error) {
	logger := logf.FromContext(ctx)

	// If there are no spec changes, return early.
	if !hasSpecOrMetadataOrProviderSpecChanges(capiMachinesDiff) {
		return false, nil
	}

	logger.Info("Changes detected for Cluster API machine. Updating it", "diff", fmt.Sprintf("%+v", capiMachinesDiff))

	if err := r.Update(ctx, updatedCAPIMachine); err != nil {
		logger.Error(err, "Failed to update Cluster API machine")

		updateErr := fmt.Errorf("failed to update Cluster API machine: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachine, updateErr.Error(), nil); condErr != nil {
			return false, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return false, updateErr
	}

	return true, nil
}

// createOrUpdateCAPIMachine creates or updates (if existing but out of date) a CAPI machine from a convertedCAPIMachine (CAPI machine object converted from MAPI).
// it returns the CAPI machine, existing or newly created.
func (r *MachineSyncReconciler) createOrUpdateCAPIMachine(ctx context.Context, sourceMAPIMachine *mapiv1beta1.Machine, existingCAPIMachine, convertedCAPIMachine *clusterv1.Machine) (*clusterv1.Machine, error) {
	logger := logf.FromContext(ctx)

	// If there is no existing CAPI machine, create a new one.
	existingCAPIMachine, err := r.ensureCAPIMachine(ctx, sourceMAPIMachine, existingCAPIMachine, convertedCAPIMachine)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure Cluster API machine: %w", err)
	}
	// There already is an existing CAPI machine, work out if it needs updating.

	// Compare the existing CAPI machine with the desired CAPI machine to check for changes.
	capiMachinesDiff := compareCAPIMachines(existingCAPIMachine, convertedCAPIMachine)

	// Update the CAPI machine spec/metadata/provider spec if needed.
	specUpdated, err := r.ensureCAPIMachineSpecUpdated(ctx, sourceMAPIMachine, capiMachinesDiff, convertedCAPIMachine)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure Cluster API machine spec updated: %w", err)
	}

	// Update the CAPI machine status if needed.
	statusUpdated, err := r.ensureCAPIMachineStatusUpdated(ctx, sourceMAPIMachine, existingCAPIMachine, convertedCAPIMachine, convertedCAPIMachine, capiMachinesDiff, specUpdated)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure Cluster API machine status updated: %w", err)
	}

	if specUpdated || statusUpdated {
		logger.Info("Successfully updated Cluster API machine")
	} else {
		logger.Info("No changes detected for Cluster API machine")
	}

	return existingCAPIMachine, nil
}

// createOrUpdateMAPIMachine creates or updates (if existing but out of date) a MAPI machine from a convertedMAPIMachine (MAPI machine object converted from CAPI).
//
//nolint:unparam
func (r *MachineSyncReconciler) createOrUpdateMAPIMachine(ctx context.Context, existingMAPIMachine *mapiv1beta1.Machine, convertedMAPIMachine *mapiv1beta1.Machine) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// If there is no existing MAPI machine, create a new one.
	existingMAPIMachine, err := r.ensureMAPIMachine(ctx, existingMAPIMachine, convertedMAPIMachine)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure Machine API machine: %w", err)
	}
	// There already is an existing MAPI machine, work out if it needs updating.

	// Compare the existing MAPI machine with the converted MAPI machine to check for changes.
	mapiMachinesDiff, err := compareMAPIMachines(existingMAPIMachine, convertedMAPIMachine)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compare Machine API machines: %w", err)
	}

	// Update the MAPI machine spec if needed.
	specUpdated, err := r.ensureMAPIMachineSpecUpdated(ctx, existingMAPIMachine, mapiMachinesDiff, convertedMAPIMachine)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure Machine API machine spec updated: %w", err)
	}

	// Update the MAPI machine status if needed.
	statusUpdated, err := r.ensureMAPIMachineStatusUpdated(ctx, existingMAPIMachine, convertedMAPIMachine, mapiMachinesDiff, specUpdated)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure Machine API machine status updated: %w", err)
	}

	if specUpdated || statusUpdated {
		logger.Info("Successfully updated Machine API machine")
	} else {
		logger.Info("No changes detected for Machine API machine")
	}

	return ctrl.Result{}, nil
}

// shouldMirrorCAPIMachineToMAPIMachine takes a CAPI machine and determines if there should
// be a MAPI mirror, it returns true only if:
//
// 1. The CAPI machine is owned by a CAPI machineset,
// 2. That owning CAPI machineset has a MAPI machineset Mirror.
func (r *MachineSyncReconciler) shouldMirrorCAPIMachineToMAPIMachine(ctx context.Context, logger logr.Logger, machine *clusterv1.Machine) (bool, error) {
	logger.V(4).WithName("shouldMirrorCAPIMachineToMAPIMachine").
		Info("Checking if Cluster API machine should be mirrored", "machine", machine.GetName())

	// Handles when the CAPI machine is deleting, and we don't have a MAPI
	// machine. See
	// (https://github.com/openshift/cluster-capi-operator/pull/281#discussion_r2029362674)
	if !machine.GetDeletionTimestamp().IsZero() {
		return false, nil
	}

	// Check if the CAPI machine has an ownerReference that points to a CAPI machineset.
	for _, ref := range machine.ObjectMeta.OwnerReferences {
		if ref.Kind != machineSetKind || ref.APIVersion != clusterv1.GroupVersion.String() {
			continue
		}

		logger.V(4).Info("Cluster API machine is owned by a Cluster API machineset", "machine", machine.GetName(), "machineset", ref.Name)

		// Checks if the CAPI machineset has a MAPI machineset mirror (same name) in MAPI namespace.
		key := client.ObjectKey{
			Namespace: r.MAPINamespace,
			Name:      ref.Name, // same name as the CAPI machineset.
		}
		mapiMachineSet := &mapiv1beta1.MachineSet{}

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
func (r *MachineSyncReconciler) convertMAPIMachineOwnerReferencesToCAPI(ctx context.Context, mapiMachine *mapiv1beta1.Machine) ([]metav1.OwnerReference, error) {
	capiOwnerReferences := []metav1.OwnerReference{}

	if len(mapiMachine.OwnerReferences) == 0 {
		clusterObject := &clusterv1.Cluster{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: r.Infra.Status.InfrastructureName}, clusterObject); err != nil {
			return nil, fmt.Errorf("failed to get Cluster API cluster: %w", err)
		}

		capiOwnerReferences = append(capiOwnerReferences, metav1.OwnerReference{
			Kind:               clusterObject.Kind,
			APIVersion:         clusterObject.APIVersion,
			Name:               clusterObject.Name,
			UID:                clusterObject.UID,
			Controller:         ptr.To(false),
			BlockOwnerDeletion: ptr.To(true),
		})

		return capiOwnerReferences, nil
	}

	if len(mapiMachine.OwnerReferences) > 1 {
		return nil, field.TooMany(field.NewPath("metadata", "ownerReferences"), len(mapiMachine.OwnerReferences), 1)
	}

	mapiOwnerReference := mapiMachine.OwnerReferences[0]
	if mapiOwnerReference.Kind == cpmsKind {
		return nil, field.Invalid(field.NewPath("metadata", "ownerReferences"), mapiMachine.OwnerReferences, errUnsupportedCPMSOwnedMachineConversion.Error())
	}

	if mapiOwnerReference.Kind != machineSetKind || mapiOwnerReference.APIVersion != mapiv1beta1.GroupVersion.String() {
		return nil, field.Invalid(field.NewPath("metadata", "ownerReferences"), mapiMachine.OwnerReferences, errUnsuportedOwnerKindForConversion.Error())
	}

	key := types.NamespacedName{
		Namespace: r.CAPINamespace,
		Name:      mapiOwnerReference.Name,
	}

	capiMachineSet := clusterv1.MachineSet{}
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
func (r *MachineSyncReconciler) convertCAPIMachineOwnerReferencesToMAPI(ctx context.Context, capiMachine *clusterv1.Machine) ([]metav1.OwnerReference, error) {
	mapiOwnerReferences := []metav1.OwnerReference{}

	if capiMachine.OwnerReferences == nil {
		return nil, nil
	}

	if len(capiMachine.OwnerReferences) == 0 {
		return mapiOwnerReferences, nil
	}

	if len(capiMachine.OwnerReferences) > 1 {
		return nil, field.TooMany(field.NewPath("metadata", "ownerReferences"), len(capiMachine.OwnerReferences), 1)
	}

	capiOwnerReference := capiMachine.OwnerReferences[0]
	switch capiOwnerReference.Kind {
	case machineSetKind:
		if capiOwnerReference.APIVersion != clusterv1.GroupVersion.String() {
			return nil, field.Invalid(field.NewPath("metadata", "ownerReferences"), capiMachine.OwnerReferences, errUnsuportedOwnerKindForConversion.Error())
		}

		key := types.NamespacedName{
			Namespace: r.MAPINamespace,
			Name:      capiOwnerReference.Name,
		}

		mapiMachineSet := mapiv1beta1.MachineSet{}
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

	case clusterv1.ClusterKind:
		if capiOwnerReference.APIVersion != clusterv1.GroupVersion.String() {
			return nil, field.Invalid(field.NewPath("metadata", "ownerReferences"), capiMachine.OwnerReferences, errUnsuportedOwnerKindForConversion.Error())
		}

		// MAPI doesn't have a cluster, so we don't need to add an owner reference.
		return nil, nil
	default:
		return nil, field.Invalid(field.NewPath("metadata", "ownerReferences"), capiMachine.OwnerReferences, errUnsuportedOwnerKindForConversion.Error())
	}

	return mapiOwnerReferences, nil
}

// fetchCAPIInfraResources fetches the provider specific infrastructure resources depending on which provider is set.
// If the InfraMachine is not found, we will just return nil. If the InfraCluster is not found, we error.
func (r *MachineSyncReconciler) fetchCAPIInfraResources(ctx context.Context, capiMachine *clusterv1.Machine) (client.Object, client.Object, error) {
	if capiMachine == nil {
		return nil, nil, nil
	}

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

	infraMachine, infraCluster, err := controllers.InitInfraMachineAndInfraClusterFromProvider(r.Platform)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to devise Cluster API infra resources: %w", err)
	}

	if err := r.Get(ctx, infraClusterKey, infraCluster); err != nil {
		return nil, nil, fmt.Errorf("failed to get Cluster API infrastructure cluster: %w", err)
	}

	if err := r.Get(ctx, infraMachineKey, infraMachine); err != nil && !apierrors.IsNotFound(err) {
		return nil, nil, fmt.Errorf("failed to get Cluster API infrastructure machine: %w", err)
	} else if apierrors.IsNotFound(err) {
		infraMachine = nil
	}

	return infraCluster, infraMachine, nil
}

//nolint:funlen,gocognit,cyclop
func (r *MachineSyncReconciler) reconcileMAPItoCAPIMachineDeletion(ctx context.Context, mapiMachine *mapiv1beta1.Machine, capiMachine *clusterv1.Machine, infraMachine client.Object) (bool, error) {
	if mapiMachine.DeletionTimestamp.IsZero() {
		if capiMachine == nil || capiMachine.DeletionTimestamp.IsZero() {
			// Neither MAPI authoritative machine nor its CAPI non-authoritative machine mirror
			// are being deleted, nothing to reconcile for deletion.
			return false, nil
		}

		// The MAPI authoritative machine is not being deleted, but the CAPI non-authoritative one is.
		// Issue a deletion also to the MAPI authoritative machine.
		if err := r.Client.Delete(ctx, mapiMachine); err != nil {
			return false, fmt.Errorf("failed to delete Machine API machine: %w", err)
		}

		// Return true to force a requeue, to allow the deletion propagation.
		return true, nil
	}

	logger := logf.FromContext(ctx)

	if capiMachine == nil && util.IsNilObject(infraMachine) {
		logger.Info("Cluster API machine and infra machine do not exist, removing corresponding Machine API machine sync finalizer")
		// We don't have  a capi machine or infra resouorces to clean up we can
		// just let the MAPI operators function as normal, and remove our own sync
		// finalizer.
		_, err := util.RemoveFinalizer(ctx, r.Client, mapiMachine, SyncFinalizer)

		return false, fmt.Errorf("failed to remove finalizer: %w", err)
	}

	if capiMachine.DeletionTimestamp.IsZero() {
		logger.Info("Machine API machine is being deleted, issuing deletion to corresponding Cluster API machine")

		if err := r.Client.Delete(ctx, capiMachine); err != nil {
			return false, fmt.Errorf("failed delete Cluster API machine: %w", err)
		}
	}

	if !util.IsNilObject(infraMachine) {
		if infraMachine.GetDeletionTimestamp().IsZero() {
			logger.Info("Machine API machine is being deleted, issuing deletion to corresponding Cluster API infra machine")

			if err := r.Client.Delete(ctx, infraMachine); err != nil {
				return false, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
	}

	// Wait until the machinev1.MachineFinalizer is removed before removing the
	// CAPI finalizer we've set above, as well as our own. This ensures the CAPI
	// mirror resource doesn't disappear before the MAPI controller is done
	// deleting the infra resource.
	if slices.Contains(mapiMachine.Finalizers, mapiv1beta1.MachineFinalizer) {
		logger.Info("Waiting on Machine API machine specific finalizer to be removed")

		return true, nil
	}

	// MAPI finalizer removed, we can clean up the finalizers on the capi machine
	// & infra machine.
	//nolint:nestif
	if !util.IsNilObject(infraMachine) {
		finalizers := infraMachine.GetFinalizers()
		hasChanged := false

		for _, finalizer := range finalizers {
			if strings.HasSuffix(finalizer, capiInfraCommonFinalizerSuffix) {
				logger.Info("Removing Cluster API infra machine specific finalizer")

				if changed, err := util.RemoveFinalizer(ctx, r.Client, infraMachine, finalizer); err != nil {
					return false, fmt.Errorf("failed to remove finalizer: %w", err)
				} else if changed {
					hasChanged = true
				}
			}
		}

		if hasChanged {
			return true, nil
		}
	}

	if changed, err := util.RemoveFinalizer(ctx, r.Client, capiMachine, clusterv1.MachineFinalizer); err != nil {
		return false, fmt.Errorf("failed to remove finalizer: %w", err)
	} else if changed {
		logger.Info("Removing Cluster API machine specific finalizer")

		return true, nil
	}

	// We want to remove the SyncFinalizer in one reconcile
	hasChanged := false

	if changed, err := util.RemoveFinalizer(ctx, r.Client, capiMachine, SyncFinalizer); err != nil {
		return false, fmt.Errorf("failed to remove finalizer: %w", err)
	} else if changed {
		logger.Info("Removing Cluster API machine sync finalizer")

		hasChanged = true
	}

	if changed, err := util.RemoveFinalizer(ctx, r.Client, infraMachine, SyncFinalizer); err != nil {
		return false, fmt.Errorf("failed to remove finalizer: %w", err)
	} else if changed {
		logger.Info("Removing Cluster API infra machine sync finalizer")

		hasChanged = true
	}

	if changed, err := util.RemoveFinalizer(ctx, r.Client, mapiMachine, SyncFinalizer); err != nil {
		return false, fmt.Errorf("failed to remove finalizer: %w", err)
	} else if changed {
		logger.Info("Removing Machine API machine sync finalizer")

		hasChanged = true
	}

	return hasChanged, nil
}

//nolint:funlen
func (r *MachineSyncReconciler) reconcileCAPItoMAPIMachineDeletion(ctx context.Context, capiMachine *clusterv1.Machine, infraMachine client.Object, mapiMachine *mapiv1beta1.Machine) (bool, error) {
	logger := logf.FromContext(ctx)

	if capiMachine.DeletionTimestamp.IsZero() {
		if mapiMachine == nil || mapiMachine.DeletionTimestamp.IsZero() {
			// Neither CAPI authoritative machine nor its MAPI non-authoritative machine mirror
			// are being deleted, nothing to reconcile for deletion.
			return false, nil
		}

		// The CAPI authoritative machine is not being deleted, but the MAPI non-authoritative one is
		// Issue a deletion also to the CAPI authoritative machine.
		if err := r.Client.Delete(ctx, capiMachine); err != nil {
			return false, fmt.Errorf("failed to delete Cluster API machine: %w", err)
		}

		// Return true to force a requeue, to allow the deletion propagation.
		return true, nil
	}

	if mapiMachine == nil {
		logger.Info("Machine API machine does not exist, removing corresponding Cluster API machine sync finalizer")
		// We don't have  a MAPI machine just let the CAPI operators function as
		// normal, and remove our own sync finalizer.
		_, err := util.RemoveFinalizer(ctx, r.Client, capiMachine, SyncFinalizer)

		return false, fmt.Errorf("failed to remove finalizer: %w", err)
	}

	if mapiMachine.DeletionTimestamp.IsZero() {
		logger.Info("Cluster API machine is being deleted, issuing deletion to corresponding Machine API machine")

		if err := r.Client.Delete(ctx, mapiMachine); err != nil {
			return false, fmt.Errorf("failed to delete Machine API machine: %w", err)
		}
	}

	// The CAPI InfraMachine must go away completely in order for the CAPI
	// Machine Finalizer to be removed by the CAPI Controllers.
	if !util.IsNilObject(infraMachine) {
		if slices.ContainsFunc(infraMachine.GetFinalizers(),
			func(s string) bool { return strings.HasSuffix(s, capiInfraCommonFinalizerSuffix) }) {
			logger.Info("Waiting on Cluster API infra machine specific finalizer to be removed")

			return true, nil
		}
	}

	logger.Info("Removing Cluster API infra machine sync finalizer")

	if _, err := util.RemoveFinalizer(ctx, r.Client, infraMachine, SyncFinalizer); err != nil {
		return false, fmt.Errorf("failed to remove finalizer: %w", err)
	}

	// Wait until the clusterv1.MachineFinalizer is removed before removing the
	// MAPI finalizer we've set above, as well as our own. This ensures the MAPI
	// mirror resource doesn't disappear before the CAPI controller is done
	// deleting the infra resources.
	if slices.Contains(capiMachine.Finalizers, clusterv1.MachineFinalizer) {
		logger.Info("Waiting on Cluster API machine specific finalizer to be removed")

		return true, nil
	}

	logger.Info("Removing Machine API machine specific finalizer")

	if changed, err := util.RemoveFinalizer(ctx, r.Client, mapiMachine, mapiv1beta1.MachineFinalizer); err != nil {
		return false, fmt.Errorf("failed to remove finalizer: %w", err)
	} else if changed {
		return true, nil
	}

	logger.Info("Removing Machine API machine sync finalizer")

	// We want to remove the SyncFinalizer in one reconcile
	hasChanged := false

	if changed, err := util.RemoveFinalizer(ctx, r.Client, mapiMachine, SyncFinalizer); err != nil {
		return false, fmt.Errorf("failed to remove finalizer: %w", err)
	} else if changed {
		hasChanged = true
	}

	logger.Info("Removing Cluster API machine sync finalizer")

	if changed, err := util.RemoveFinalizer(ctx, r.Client, capiMachine, SyncFinalizer); err != nil {
		return false, fmt.Errorf("failed to remove finalizer: %w", err)
	} else if changed {
		hasChanged = true
	}

	return hasChanged, nil
}

// ensureSyncFinalizer ensures the sync finalizer is present across the mapi
// machine, capi machine and capi infra machine.
func (r *MachineSyncReconciler) ensureSyncFinalizer(ctx context.Context, mapiMachine *mapiv1beta1.Machine, capiMachine *clusterv1.Machine, infraMachine client.Object) (bool, error) {
	var shouldRequeue bool

	var errors []error

	if mapiMachine != nil {
		if mapiMachine.DeletionTimestamp.IsZero() {
			didSet, err := util.EnsureFinalizer(ctx, r.Client, mapiMachine, SyncFinalizer)
			if err != nil {
				errors = append(errors, err)
			} else if didSet {
				shouldRequeue = true
			}
		}
	}

	// This will add the finalizer in the scenario where the capiMachine does not
	// exist yet too, as the creation of the machine triggers a reconcile where
	// this code path will run.
	if capiMachine != nil {
		if capiMachine.DeletionTimestamp.IsZero() {
			didSet, err := util.EnsureFinalizer(ctx, r.Client, capiMachine, SyncFinalizer)
			if err != nil {
				errors = append(errors, err)
			} else if didSet {
				shouldRequeue = true
			}
		}
	}

	if !util.IsNilObject(infraMachine) {
		if infraMachine.GetDeletionTimestamp().IsZero() {
			didSet, err := util.EnsureFinalizer(ctx, r.Client, infraMachine, SyncFinalizer)
			if err != nil {
				errors = append(errors, err)
			} else if didSet {
				shouldRequeue = true
			}
		}
	}

	return shouldRequeue, utilerrors.NewAggregate(errors)
}

// compareCAPIMachines compares CAPI machines a and b, and returns a list of differences, or none if there are none.
func compareCAPIMachines(capiMachine1, capiMachine2 *clusterv1.Machine) map[string]any {
	diff := make(map[string]any)

	if diffSpec := deep.Equal(capiMachine1.Spec, capiMachine2.Spec); len(diffSpec) > 0 {
		diff[".spec"] = diffSpec
	}

	if diffObjectMeta := util.ObjectMetaEqual(capiMachine1.ObjectMeta, capiMachine2.ObjectMeta); len(diffObjectMeta) > 0 {
		diff[".metadata"] = diffObjectMeta
	}

	if diffStatus := util.CAPIMachineStatusEqual(capiMachine1.Status, capiMachine2.Status); len(diffStatus) > 0 {
		diff[".status"] = diffStatus
	}

	return diff
}

// compareMAPIMachines compares MAPI machines a and b, and returns a list of differences, or none if there are none.
func compareMAPIMachines(a, b *mapiv1beta1.Machine) (map[string]any, error) {
	diff := make(map[string]any)

	ps1, err := mapi2capi.AWSProviderSpecFromRawExtension(a.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("unable to parse first Machine API machine set providerSpec: %w", err)
	}

	ps2, err := mapi2capi.AWSProviderSpecFromRawExtension(b.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("unable to parse second Machine API machine set providerSpec: %w", err)
	}

	// Sort the tags by name to ensure consistent ordering.
	// On the CAPI side these tags are in a map,
	// so the order is not guaranteed when converting back from a CAPI map to a MAPI slice.
	sort.Slice(ps1.Tags, func(i, j int) bool {
		return ps1.Tags[i].Name < ps1.Tags[j].Name
	})

	// Sort the tags by name to ensure consistent ordering.
	// On the CAPI side these tags are in a map,
	// so the order is not guaranteed when converting back from a CAPI map to a MAPI slice.
	sort.Slice(ps2.Tags, func(i, j int) bool {
		return ps2.Tags[i].Name < ps2.Tags[j].Name
	})

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

	if diffStatus := util.MAPIMachineStatusEqual(a.Status, b.Status); len(diffStatus) > 0 {
		diff[".status"] = diffStatus
	}

	return diff, nil
}

// compareCAPIInfraMachines compares CAPI infra machines a and b, and returns a list of differences, or none if there are none.
//
//nolint:funlen
func compareCAPIInfraMachines(platform configv1.PlatformType, infraMachine1, infraMachine2 client.Object) (map[string]any, error) {
	switch platform {
	case configv1.AWSPlatformType:
		typedInfraMachine1, ok := infraMachine1.(*awsv1.AWSMachine)
		if !ok {
			return nil, errAssertingCAPIAWSMachine
		}

		typedinfraMachine2, ok := infraMachine2.(*awsv1.AWSMachine)
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
	case configv1.OpenStackPlatformType:
		typedInfraMachine1, ok := infraMachine1.(*openstackv1.OpenStackMachine)
		if !ok {
			return nil, errAssertingCAPIOpenStackMachine
		}

		typedinfraMachine2, ok := infraMachine2.(*openstackv1.OpenStackMachine)
		if !ok {
			return nil, errAssertingCAPIOpenStackMachine
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
		typedInfraMachine1, ok := infraMachine1.(*ibmpowervsv1.IBMPowerVSMachine)
		if !ok {
			return nil, errAssertingCAPIIBMPowerVSMachine
		}

		typedinfraMachine2, ok := infraMachine2.(*ibmpowervsv1.IBMPowerVSMachine)
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

// ensureCAPIMachineStatusUpdated updates the CAPI machine status if changes are detected and conditions are met.
func (r *MachineSyncReconciler) ensureCAPIMachineStatusUpdated(ctx context.Context, mapiMachine *mapiv1beta1.Machine, existingCAPIMachine, convertedCAPIMachine, updatedOrCreatedCAPIMachine *clusterv1.Machine, capiMachinesDiff map[string]any, specUpdated bool) (bool, error) {
	logger := logf.FromContext(ctx)

	// If there are spec changes we always want to update the status to sync the spec generation.
	// If there are status changes we always want to update the status.
	// If both the above are false, we can skip updating the status and return early.
	if !specUpdated && !hasStatusChanges(capiMachinesDiff) {
		return false, nil
	}

	// If the source API object (MAPI Machine) status.synchronizedGeneration does not match the objectmeta.generation
	// it means the source API object status has not yet caught up with the desired spec,
	// so we don't want to update the CAPI machine status until that has happened.
	if mapiMachine.Status.SynchronizedGeneration != mapiMachine.ObjectMeta.Generation {
		logger.Info("Changes detected for Cluster API machine status, but the MAPI machine spec has not been observed yet, skipping status update")

		return false, nil
	}

	patchBase := client.MergeFrom(existingCAPIMachine.DeepCopy())
	setChangedCAPIMachineStatusFields(existingCAPIMachine, convertedCAPIMachine)

	// Update the observed generation to match the updated source API object generation.
	existingCAPIMachine.Status.ObservedGeneration = updatedOrCreatedCAPIMachine.ObjectMeta.Generation

	isPatchRequired, err := util.IsPatchRequired(existingCAPIMachine, patchBase)
	if err != nil {
		return false, fmt.Errorf("failed to check if patch is required: %w", err)
	}

	if !isPatchRequired {
		// If the patch is not required, return early.
		return false, nil
	}

	logger.Info("Changes detected for Cluster API machine status. Updating it")

	if err := r.Status().Patch(ctx, existingCAPIMachine, patchBase); err != nil {
		logger.Error(err, "Failed to update Cluster API machine status")
		updateErr := fmt.Errorf("failed to update status: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachine, updateErr.Error(), nil); condErr != nil {
			return false, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return false, updateErr
	}

	return true, nil
}

// ensureMAPIMachine creates a new MAPI machine if one doesn't exist.
func (r *MachineSyncReconciler) ensureMAPIMachine(ctx context.Context, existingMAPIMachine *mapiv1beta1.Machine, convertedMAPIMachine *mapiv1beta1.Machine) (*mapiv1beta1.Machine, error) {
	// If there is an existing MAPI machine, no need to create one.
	if existingMAPIMachine != nil {
		return existingMAPIMachine, nil
	}

	logger := logf.FromContext(ctx)

	// Set the existing MAPI machine to the converted MAPI machine.
	existingMAPIMachine = convertedMAPIMachine.DeepCopy()

	if err := r.Create(ctx, convertedMAPIMachine); err != nil {
		logger.Error(err, "Failed to create Machine API machine")

		createErr := fmt.Errorf("failed to create Machine API machine: %w", err)
		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, existingMAPIMachine, corev1.ConditionFalse, reasonFailedToCreateMAPIMachine, createErr.Error(), nil); condErr != nil {
			return nil, utilerrors.NewAggregate([]error{createErr, condErr})
		}

		return nil, createErr
	}

	logger.Info("Successfully created Machine API machine", "name", existingMAPIMachine.Name)

	return existingMAPIMachine, nil
}

// ensureMAPIMachineStatusUpdated updates the MAPI machine status if changes are detected.
func (r *MachineSyncReconciler) ensureMAPIMachineStatusUpdated(ctx context.Context, existingMAPIMachine *mapiv1beta1.Machine, convertedMAPIMachine *mapiv1beta1.Machine, mapiMachinesDiff map[string]any, specUpdated bool) (bool, error) {
	logger := logf.FromContext(ctx)

	// If there are no status changes and the spec has not been updated, return early.
	if !hasStatusChanges(mapiMachinesDiff) && !specUpdated {
		return false, nil
	}

	logger.Info("Changes detected for Machine API machine status. Updating it", "diff", fmt.Sprintf("%+v", mapiMachinesDiff))

	patchBase := client.MergeFrom(existingMAPIMachine.DeepCopy())

	// Set the changed MAPI machine status fields from the converted MAPI machine.
	setChangedMAPIMachineStatusFields(existingMAPIMachine, convertedMAPIMachine)

	// Here we would've updated the observed generation to match the updated source API object generation.
	// MAPI Machine does not have the observed generation field.

	isPatchRequired, err := util.IsPatchRequired(existingMAPIMachine, patchBase)
	if err != nil {
		return false, fmt.Errorf("failed to check if patch is required: %w", err)
	}

	if !isPatchRequired {
		// If the patch is not required, return early.
		return false, nil
	}

	if err := r.Status().Patch(ctx, existingMAPIMachine, patchBase); err != nil {
		logger.Error(err, "Failed to update Machine API machine status")
		updateErr := fmt.Errorf("failed to update status: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, existingMAPIMachine, corev1.ConditionFalse, reasonFailedToUpdateMAPIMachine, updateErr.Error(), nil); condErr != nil {
			return false, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return false, updateErr
	}

	return true, nil
}

// ensureMAPIMachineSpecUpdated updates the MAPI machine if changes are detected.
func (r *MachineSyncReconciler) ensureMAPIMachineSpecUpdated(ctx context.Context, existingMAPIMachine *mapiv1beta1.Machine, mapiMachinesDiff map[string]any, updatedMAPIMachine *mapiv1beta1.Machine) (bool, error) {
	logger := logf.FromContext(ctx)

	// If there are no spec changes, return early.
	if !hasSpecOrMetadataOrProviderSpecChanges(mapiMachinesDiff) {
		return false, nil
	}

	logger.Info("Changes detected for Machine API machine. Updating it", "diff", fmt.Sprintf("%+v", mapiMachinesDiff))

	if err := r.Update(ctx, updatedMAPIMachine); err != nil {
		logger.Error(err, "Failed to update Machine API machine")

		updateErr := fmt.Errorf("failed to update Machine API machine: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(ctx, existingMAPIMachine, corev1.ConditionFalse, reasonFailedToUpdateMAPIMachine, updateErr.Error(), nil); condErr != nil {
			return false, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return false, updateErr
	}

	return true, nil
}

// setChangedCAPIMachineStatusFields sets the updated fields in the CAPI machine status.
func setChangedCAPIMachineStatusFields(existingCAPIMachine, convertedCAPIMachine *clusterv1.Machine) {
	// convertedCAPIMachine holds the computed and desired status changes converted from the source MAPI machine, so apply them to the existing existingCAPIMachine.
	// Merge the v1beta1 conditions.
	for i := range convertedCAPIMachine.Status.Conditions {
		conditions.Set(existingCAPIMachine, &convertedCAPIMachine.Status.Conditions[i])
	}

	// Copy them back to the convertedCAPIMachine.
	convertedCAPIMachine.Status.Conditions = existingCAPIMachine.Status.Conditions

	// Merge the v1beta2 conditions.
	if convertedCAPIMachine.Status.V1Beta2 != nil {
		if existingCAPIMachine.Status.V1Beta2 == nil {
			existingCAPIMachine.Status.V1Beta2 = &clusterv1.MachineV1Beta2Status{}
		}

		for i := range convertedCAPIMachine.Status.V1Beta2.Conditions {
			conditionsv1beta2.Set(existingCAPIMachine, convertedCAPIMachine.Status.V1Beta2.Conditions[i])
		}

		// Copy them back to the convertedCAPIMachine.
		convertedCAPIMachine.Status.V1Beta2.Conditions = existingCAPIMachine.Status.V1Beta2.Conditions
	}

	// Finally overwrite the entire existingCAPIMachine status with the convertedCAPIMachine status.
	existingCAPIMachine.Status = convertedCAPIMachine.Status
}

// setChangedMAPIMachineStatusFields sets the updated fields in the MAPI machine status.
func setChangedMAPIMachineStatusFields(existingMAPIMachine, convertedMAPIMachine *mapiv1beta1.Machine) {
	// convertedMAPIMachine holds the computed and desired status changes, so apply them to the existing existingMAPIMachine.
	for i := range convertedMAPIMachine.Status.Conditions {
		existingMAPIMachine.Status.Conditions = util.SetMAPICondition(existingMAPIMachine.Status.Conditions, &convertedMAPIMachine.Status.Conditions[i])
	}

	// Copy them back to the convertedMAPIMachine.
	convertedMAPIMachine.Status.Conditions = existingMAPIMachine.Status.Conditions

	// Copy the other fields that are not present in the convertedMAPIMachine from the existingMAPIMachine.
	convertedMAPIMachine.Status.AuthoritativeAPI = existingMAPIMachine.Status.AuthoritativeAPI
	convertedMAPIMachine.Status.SynchronizedGeneration = existingMAPIMachine.Status.SynchronizedGeneration
	convertedMAPIMachine.Status.LastOperation = existingMAPIMachine.Status.LastOperation
	convertedMAPIMachine.Status.ProviderStatus = existingMAPIMachine.Status.ProviderStatus

	// Finally overwrite the entire existingMAPIMachine status with the convertedMAPIMachine status.
	existingMAPIMachine.Status = convertedMAPIMachine.Status
}

// hasStatusChanges checks if there are status-related changes in the diff.
func hasStatusChanges(diff map[string]any) bool {
	_, hasStatus := diff[".status"]
	return hasStatus
}

// applySynchronizedConditionWithPatch updates the synchronized condition
// using a server side apply patch. We do this to force ownership of the
// 'Synchronized' condition and 'SynchronizedGeneration'.
func (r *MachineSyncReconciler) applySynchronizedConditionWithPatch(ctx context.Context, mapiMachine *mapiv1beta1.Machine, status corev1.ConditionStatus, reason, message string, generation *int64) error {
	return synccommon.ApplySyncStatus[*machinev1applyconfigs.MachineStatusApplyConfiguration](
		ctx, r.Client, controllerName,
		machinev1applyconfigs.Machine, mapiMachine,
		status, reason, message, generation)
}

// hasSpecOrMetadataOrProviderSpecChanges checks if there are spec, metadata, or provider spec changes in the diff.
func hasSpecOrMetadataOrProviderSpecChanges(diff map[string]any) bool {
	_, hasSpec := diff[".spec"]
	_, hasMetadata := diff[".metadata"]
	_, hasProviderSpec := diff[".providerSpec"]

	return hasSpec || hasMetadata || hasProviderSpec
}

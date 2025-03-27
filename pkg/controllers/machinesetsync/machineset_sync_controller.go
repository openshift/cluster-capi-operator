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

package machinesetsync

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/go-test/deep"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"k8s.io/client-go/tools/record"
	awscapiv1beta1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
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
	// errPlatformNotSupported is returned when the platform is not supported.
	errPlatformNotSupported = errors.New("error determining InfraMachineTemplate type, platform not supported")

	// errUnexpectedInfraMachineTemplateType is returned when we receive an unexpected InfraMachineTemplate type.
	errUnexpectedInfraMachineTemplateType = errors.New("unexpected InfraMachineTemplate type")

	// errUnexpectedInfraClusterType is returned when we receive an unexpected InfraCluster type.
	errUnexpectedInfraClusterType = errors.New("unexpected InfraCluster type")

	// errAssertingCAPIAWSMachineTemplate is returned when we encounter an issue asserting a client.Object into a AWSMachineTemplate.
	errAssertingCAPIAWSMachineTemplate = errors.New("error asserting the Cluster API AWSMachineTemplate object")

	// errAssertingCAPIPowerVSMachineTemplate is returned when we encounter an issue asserting a client.Object into a IBMPowerVSMachineTemplate.
	errAssertingCAPIIBMPowerVSMachineTemplate = errors.New("error asserting the Cluster API IBMPowerVSMachineTemplate object")

	// errUnrecognizedConditionStatus is returned when the condition status is not recognized.
	errUnrecognizedConditionStatus = errors.New("error unrecognized condition status")
)

const (
	reasonFailedToGetCAPIInfraResources          = "FailedToGetCAPIInfraResources"
	reasonFailedToConvertCAPIMachineSetToMAPI    = "FailedToConvertCAPIMachineSetToMAPI"
	reasonFailedToConvertMAPIMachineSetToCAPI    = "FailedToConvertMAPIMachineSetToCAPI"
	reasonFailedToUpdateMAPIMachineSet           = "FailedToUpdateMAPIMachineSet"
	reasonFailedToUpdateCAPIMachineSet           = "FailedToUpdateCAPIMachineSet"
	reasonFailedToUpdateCAPIInfraMachineTemplate = "FailedToUpdateCAPIInfraMachineTemplate"
	reasonFailedToCreateCAPIMachineSet           = "FailedToCreateCAPIMachineSet"
	reasonFailedToCreateCAPIInfraMachineTemplate = "FailedToCreateCAPIInfraMachineTemplate"
	reasonFailedToGetCAPIMachineSet              = "FailedToGetCAPIMachineSet"
	reasonResourceSynchronized                   = "ResourceSynchronized"

	messageSuccessfullySynchronizedCAPItoMAPI = "Successfully synchronized Cluster API MachineSet to Machine API"
	messageSuccessfullySynchronizedMAPItoCAPI = "Successfully synchronized Machine API MachineSet to Cluster API"

	controllerName string = "MachineSetSyncController"
)

// MachineSetSyncReconciler reconciles CAPI and MAPI MachineSets.
type MachineSetSyncReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Infra         *configv1.Infrastructure
	Platform      configv1.PlatformType
	CAPINamespace string
	MAPINamespace string
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineSetSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	infraMachineTemplate, _, err := initInfraMachineTemplateAndInfraClusterFromProvider(r.Platform)
	if err != nil {
		return fmt.Errorf("failed to get infrastructure machine template from Provider: %w", err)
	}

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
		Watches(
			infraMachineTemplate,
			handler.EnqueueRequestsFromMapFunc(util.ResolveCAPIMachineSetFromObject(r.MAPINamespace)),
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
func (r *MachineSetSyncReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	logger.V(1).Info("Reconciling machine set")
	defer logger.V(1).Info("Finished reconciling machine set")

	mapiMachineSet, capiMachineSet, err := r.fetchMachineSets(ctx, req.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to fetch machine sets: %w", err)
	}

	if mapiMachineSet == nil && capiMachineSet == nil {
		logger.Info("Both Machine API and Cluster API machine sets not found, nothing to do")
		return ctrl.Result{}, nil
	}

	if mapiMachineSet == nil {
		logger.Info("Only Cluster API machine set found, nothing to do")
		return ctrl.Result{}, nil
	}

	return r.syncMachineSets(ctx, mapiMachineSet, capiMachineSet)
}

// fetchMachineSets fetches both MAPI and CAPI MachineSets.
func (r *MachineSetSyncReconciler) fetchMachineSets(ctx context.Context, name string) (*machinev1beta1.MachineSet, *capiv1beta1.MachineSet, error) {
	logger := log.FromContext(ctx)

	mapiMachineSet := &machinev1beta1.MachineSet{}

	capiMachineSet := &capiv1beta1.MachineSet{}

	if err := r.Get(ctx, client.ObjectKey{Namespace: r.MAPINamespace, Name: name}, mapiMachineSet); apierrors.IsNotFound(err) {
		logger.Info("Machine API machine set not found")

		mapiMachineSet = nil
	} else if err != nil {
		return nil, nil, fmt.Errorf("failed to get Machine API machine set: %w", err)
	}

	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: name}, capiMachineSet); apierrors.IsNotFound(err) {
		logger.Info("Cluster API machine set not found")

		capiMachineSet = nil
	} else if err != nil {
		return nil, nil, fmt.Errorf("failed to get Cluster API machine set: %w", err)
	}

	return mapiMachineSet, capiMachineSet, nil
}

// fetchCAPIInfraResources fetches the provider specific infrastructure resources depending on which provider is set.
func (r *MachineSetSyncReconciler) fetchCAPIInfraResources(ctx context.Context, capiMachineSet *capiv1beta1.MachineSet) (client.Object, client.Object, error) {
	var infraCluster, infraMachineTemplate client.Object

	infraClusterKey := client.ObjectKey{
		Namespace: capiMachineSet.Namespace,
		Name:      capiMachineSet.Spec.ClusterName,
	}

	infraMachineTemplateRef := capiMachineSet.Spec.Template.Spec.InfrastructureRef
	infraMachineTemplateKey := client.ObjectKey{
		Namespace: infraMachineTemplateRef.Namespace,
		Name:      infraMachineTemplateRef.Name,
	}

	infraMachineTemplate, infraCluster, err := initInfraMachineTemplateAndInfraClusterFromProvider(r.Platform)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to devise Cluster API infra resources: %w", err)
	}

	if err := r.Get(ctx, infraClusterKey, infraCluster); err != nil {
		return nil, nil, fmt.Errorf("failed to get Cluster API infrastructure cluster: %w", err)
	}

	if err := r.Get(ctx, infraMachineTemplateKey, infraMachineTemplate); err != nil {
		return nil, nil, fmt.Errorf("failed to get Cluster API infrastructure machine template: %w", err)
	}

	return infraCluster, infraMachineTemplate, nil
}

// syncMachineSets synchronizes MachineSets based on the authoritative API.
func (r *MachineSetSyncReconciler) syncMachineSets(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	authoritativeAPI := mapiMachineSet.Status.AuthoritativeAPI

	switch {
	case authoritativeAPI == machinev1beta1.MachineAuthorityMachineAPI:
		return r.reconcileMAPIMachineSetToCAPIMachineSet(ctx, mapiMachineSet, capiMachineSet)
	case authoritativeAPI == machinev1beta1.MachineAuthorityClusterAPI && capiMachineSet == nil:
		return r.reconcileMAPIMachineSetToCAPIMachineSet(ctx, mapiMachineSet, capiMachineSet)
	case authoritativeAPI == machinev1beta1.MachineAuthorityClusterAPI && capiMachineSet != nil:
		return r.reconcileCAPIMachineSetToMAPIMachineSet(ctx, capiMachineSet, mapiMachineSet)
	case authoritativeAPI == machinev1beta1.MachineAuthorityMigrating:
		logger.Info("machine set is currently being migrated")
		return ctrl.Result{}, nil

	default:
		logger.Info("unexpected value for authoritativeAPI", "AuthoritativeAPI", mapiMachineSet.Status.AuthoritativeAPI)

		return ctrl.Result{}, nil
	}
}

// reconcileMAPIMachineSetToCAPIMachineSet reconciles a MAPI MachineSet to a CAPI MachineSet.
func (r *MachineSetSyncReconciler) reconcileMAPIMachineSetToCAPIMachineSet(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	newCAPIMachineSet, newCAPIInfraMachineTemplate, warns, err := r.convertMAPIToCAPIMachineSet(mapiMachineSet)
	if err != nil {
		conversionErr := fmt.Errorf("failed to convert Machine API machine set to Cluster API machine set: %w", err)
		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineSetToCAPI, conversionErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{conversionErr, condErr})
		}

		return ctrl.Result{}, conversionErr
	}

	for _, warning := range warns {
		logger.Info("Warning during conversion", "warning", warning)
		r.Recorder.Event(mapiMachineSet, corev1.EventTypeWarning, "ConversionWarning", warning)
	}

	newCAPIMachineSet.SetResourceVersion(util.GetResourceVersion(client.Object(capiMachineSet)))
	newCAPIMachineSet.SetNamespace(r.CAPINamespace)
	newCAPIMachineSet.Spec.Template.Spec.InfrastructureRef.Namespace = r.CAPINamespace

	_, infraMachineTemplate, err := r.fetchCAPIInfraResources(ctx, newCAPIMachineSet)
	if err != nil && !apierrors.IsNotFound(err) {
		fetchErr := fmt.Errorf("failed to fetch Cluster API infra resources: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, fetchErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{fetchErr, condErr})
		}

		return ctrl.Result{}, fetchErr
	}

	newCAPIInfraMachineTemplate.SetResourceVersion(util.GetResourceVersion(infraMachineTemplate))
	newCAPIInfraMachineTemplate.SetNamespace(r.CAPINamespace)

	if result, err := r.createOrUpdateCAPIInfraMachineTemplate(ctx, mapiMachineSet, infraMachineTemplate, newCAPIInfraMachineTemplate); err != nil {
		return result, fmt.Errorf("unable to ensure Cluster API infra machine template: %w", err)
	}

	if result, err := r.createOrUpdateCAPIMachineSet(ctx, mapiMachineSet, capiMachineSet, newCAPIMachineSet); err != nil {
		return result, fmt.Errorf("unable to ensure Cluster API machine set: %w", err)
	}

	return ctrl.Result{}, r.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionTrue,
		consts.ReasonResourceSynchronized, messageSuccessfullySynchronizedMAPItoCAPI, &mapiMachineSet.Generation)
}

// reconcileCAPIMachineSetToMAPIMachineSet reconciles a CAPI MachineSet to a
// MAPI MachineSet.
func (r *MachineSetSyncReconciler) reconcileCAPIMachineSetToMAPIMachineSet(ctx context.Context, capiMachineSet *capiv1beta1.MachineSet, mapiMachineSet *machinev1beta1.MachineSet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	infraCluster, infraMachineTemplate, err := r.fetchCAPIInfraResources(ctx, capiMachineSet)
	if err != nil {
		fetchErr := fmt.Errorf("failed to fetch Cluster API infra resources: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, fetchErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{fetchErr, condErr})
		}

		return ctrl.Result{}, fetchErr
	}

	newMapiMachineSet, warns, err := r.convertCAPIToMAPIMachineSet(capiMachineSet, infraMachineTemplate, infraCluster)
	if err != nil {
		conversionErr := fmt.Errorf("failed to convert Cluster API machine set to Machine API machine set: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToConvertCAPIMachineSetToMAPI, conversionErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{conversionErr, condErr})
		}

		return ctrl.Result{}, conversionErr
	}

	for _, warning := range warns {
		logger.Info("Warning during conversion", "warning", warning)
		r.Recorder.Event(mapiMachineSet, corev1.EventTypeWarning, "ConversionWarning", warning)
	}

	newMapiMachineSet.Spec.Template.Labels = util.MergeMaps(mapiMachineSet.Spec.Template.Labels, newMapiMachineSet.Spec.Template.Labels)
	newMapiMachineSet.SetNamespace(mapiMachineSet.GetNamespace())
	// The conversion does not set a resource version, so we must copy it over
	newMapiMachineSet.SetResourceVersion(util.GetResourceVersion(mapiMachineSet))

	mapiMachineSetsDiff := compareMAPIMachineSets(mapiMachineSet, newMapiMachineSet)
	if len(mapiMachineSetsDiff) > 0 {
		logger.Info("Changes detected, updating Machine API machine set", "diff", mapiMachineSetsDiff)

		if err := r.Update(ctx, newMapiMachineSet); err != nil {
			logger.Error(err, "Failed to update Machine API machine set")

			updateErr := fmt.Errorf("failed to update Machine API machine set: %w", err)

			if condErr := r.applySynchronizedConditionWithPatch(
				ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateMAPIMachineSet, updateErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, utilerrors.NewAggregate([]error{updateErr, condErr})
			}

			return ctrl.Result{}, updateErr
		}

		logger.Info("Successfully updated Machine API machine set")
	} else {
		logger.Info("No changes detected in Machine API machine set")
	}

	return ctrl.Result{}, r.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionTrue,
		consts.ReasonResourceSynchronized, messageSuccessfullySynchronizedCAPItoMAPI, &capiMachineSet.Generation)
}

// convertCAPIToMAPIMachineSet converts a CAPI MachineSet to a MAPI MachineSet, selecting the correct converter based on the platform.
func (r *MachineSetSyncReconciler) convertCAPIToMAPIMachineSet(capiMachineSet *capiv1beta1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) (*machinev1beta1.MachineSet, []string, error) {
	switch r.Platform {
	case configv1.AWSPlatformType:
		awsMachineTemplate, ok := infraMachineTemplate.(*awscapiv1beta1.AWSMachineTemplate)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected AWSMachineTemplate, got %T", errUnexpectedInfraMachineTemplateType, infraMachineTemplate)
		}

		awsCluster, ok := infraCluster.(*awscapiv1beta1.AWSCluster)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected AWSCluster, got %T", errUnexpectedInfraClusterType, infraCluster)
		}

		return capi2mapi.FromMachineSetAndAWSMachineTemplateAndAWSCluster( //nolint: wrapcheck
			capiMachineSet, awsMachineTemplate, awsCluster,
		).ToMachineSet()
	case configv1.PowerVSPlatformType:
		powerVSMachineTemplate, ok := infraMachineTemplate.(*capibmv1.IBMPowerVSMachineTemplate)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected IBMPowerVSMachineTemplate, got %T", errUnexpectedInfraMachineTemplateType, infraMachineTemplate)
		}

		powerVSCluster, ok := infraCluster.(*capibmv1.IBMPowerVSCluster)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected IBMPowerVSCluster, got %T", errUnexpectedInfraClusterType, infraCluster)
		}

		return capi2mapi.FromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster( //nolint: wrapcheck
			capiMachineSet, powerVSMachineTemplate, powerVSCluster,
		).ToMachineSet()
	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, r.Platform)
	}
}

// convertMAPIToCAPIMachineSet converts a MAPI MachineSet to a CAPI MachineSet, selecting the correct converter based on the platform.
func (r *MachineSetSyncReconciler) convertMAPIToCAPIMachineSet(mapiMachineSet *machinev1beta1.MachineSet) (*capiv1beta1.MachineSet, client.Object, []string, error) {
	switch r.Platform {
	case configv1.AWSPlatformType:
		return mapi2capi.FromAWSMachineSetAndInfra(mapiMachineSet, r.Infra).ToMachineSetAndMachineTemplate() //nolint:wrapcheck
	case configv1.PowerVSPlatformType:
		return mapi2capi.FromPowerVSMachineSetAndInfra(mapiMachineSet, r.Infra).ToMachineSetAndMachineTemplate() //nolint:wrapcheck
	default:
		return nil, nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, r.Platform)
	}
}

// applySynchronizedConditionWithPatch updates the synchronized condition
// using a server side apply patch. We do this to force ownership of the
// 'Synchronized' condition and 'SynchronizedGeneration'.
func (r *MachineSetSyncReconciler) applySynchronizedConditionWithPatch(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, status corev1.ConditionStatus, reason, message string, generation *int64) error {
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
		synchronizedGeneration = mapiMachineSet.Status.SynchronizedGeneration
	case corev1.ConditionUnknown:
		severity = machinev1beta1.ConditionSeverityInfo
		// Restore the old SynchronizedGeneration, otherwise if that's not set the existing one will be cleared.
		synchronizedGeneration = mapiMachineSet.Status.SynchronizedGeneration
	default:
		return fmt.Errorf("%w: %s", errUnrecognizedConditionStatus, status)
	}

	conditionAc := machinev1applyconfigs.Condition().
		WithType(consts.SynchronizedCondition).
		WithStatus(status).
		WithReason(reason).
		WithMessage(message).
		WithSeverity(severity)

	util.SetLastTransitionTime(consts.SynchronizedCondition, mapiMachineSet.Status.Conditions, conditionAc)

	statusAc := machinev1applyconfigs.MachineSetStatus().
		WithConditions(conditionAc).
		WithSynchronizedGeneration(synchronizedGeneration)

	msAc := machinev1applyconfigs.MachineSet(mapiMachineSet.GetName(), mapiMachineSet.GetNamespace()).
		WithStatus(statusAc)

	if err := r.Status().Patch(ctx, mapiMachineSet, util.ApplyConfigPatch(msAc), client.ForceOwnership, client.FieldOwner(controllerName+"-SynchronizedCondition")); err != nil {
		return fmt.Errorf("failed to patch Machine API machine set status with synchronized condition: %w", err)
	}

	return nil
}

// createOrUpdateCAPIInfraMachineTemplate creates a CAPI infra machine template from a MAPI machine set, or updates if it exists and it is out of date.
func (r *MachineSetSyncReconciler) createOrUpdateCAPIInfraMachineTemplate(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, infraMachineTemplate client.Object, newCAPIInfraMachineTemplate client.Object) (ctrl.Result, error) { //nolint:unparam
	logger := log.FromContext(ctx)

	if infraMachineTemplate == nil {
		if err := r.Create(ctx, newCAPIInfraMachineTemplate); err != nil {
			logger.Error(err, "Failed to create Cluster API infra machine template")
			createErr := fmt.Errorf("failed to create Cluster API infra machine template: %w", err)

			if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToCreateCAPIInfraMachineTemplate, createErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, utilerrors.NewAggregate([]error{createErr, condErr})
			}

			return ctrl.Result{}, createErr
		}

		logger.Info("Successfully created Cluster API infra machine template")

		return ctrl.Result{}, nil
	}

	capiInfraMachineTemplatesDiff, err := compareCAPIInfraMachineTemplates(r.Platform, infraMachineTemplate, newCAPIInfraMachineTemplate)
	if err != nil {
		logger.Error(err, "Failed to check Cluster API infra machine template diff")
		updateErr := fmt.Errorf("failed to check Cluster API infra machine template diff: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachineTemplate, updateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return ctrl.Result{}, updateErr
	}

	if len(capiInfraMachineTemplatesDiff) == 0 {
		logger.Info("No changes detected in Cluster API infra machine template")
		return ctrl.Result{}, nil
	}

	logger.Info("Changes detected, updating Cluster API infra machine template", "diff", capiInfraMachineTemplatesDiff)

	if err := r.Update(ctx, newCAPIInfraMachineTemplate); err != nil {
		logger.Error(err, "Failed to update Cluster API infra machine template")

		updateErr := fmt.Errorf("failed to update Cluster API infra machine template: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachineTemplate, updateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return ctrl.Result{}, updateErr
	}

	logger.Info("Successfully updated Cluster API infra machine template")

	return ctrl.Result{}, nil
}

// createOrUpdateCAPIMachineSet creates a CAPI machine set from a MAPI one, or updates if it exists and it is out of date.
func (r *MachineSetSyncReconciler) createOrUpdateCAPIMachineSet(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet, newCAPIMachineSet *capiv1beta1.MachineSet) (ctrl.Result, error) { //nolint:unparam
	logger := log.FromContext(ctx)

	if capiMachineSet == nil {
		if err := r.Create(ctx, newCAPIMachineSet); err != nil {
			logger.Error(err, "Failed to create Cluster API machine set")

			createErr := fmt.Errorf("failed to create Cluster API machine set: %w", err)
			if condErr := r.applySynchronizedConditionWithPatch(
				ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToCreateCAPIMachineSet, createErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, utilerrors.NewAggregate([]error{createErr, condErr})
			}

			return ctrl.Result{}, createErr
		}

		logger.Info("Successfully created Cluster API machine set")

		return ctrl.Result{}, nil
	}

	capiMachineSetsDiff := compareCAPIMachineSets(capiMachineSet, newCAPIMachineSet)

	if len(capiMachineSetsDiff) == 0 {
		logger.Info("No changes detected in Cluster API machine set")
		return ctrl.Result{}, nil
	}

	logger.Info("Changes detected, updating Cluster API machine set", "diff", capiMachineSetsDiff)

	if err := r.Update(ctx, newCAPIMachineSet); err != nil {
		logger.Error(err, "Failed to update Cluster API machine set")

		updateErr := fmt.Errorf("failed to update Cluster API machine set: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachineSet, updateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return ctrl.Result{}, updateErr
	}

	logger.Info("Successfully updated Cluster API machine set")

	return ctrl.Result{}, nil
}

// initInfraMachineTemplateAndInfraClusterFromProvider returns the correct InfraMachineTemplate and InfraCluster implementation
// for a given provider.
//
// As we implement other cloud providers, we'll need to update this list.
func initInfraMachineTemplateAndInfraClusterFromProvider(platform configv1.PlatformType) (client.Object, client.Object, error) {
	switch platform {
	case configv1.AWSPlatformType:
		return &awscapiv1beta1.AWSMachineTemplate{}, &awscapiv1beta1.AWSCluster{}, nil
	case configv1.PowerVSPlatformType:
		return &capibmv1.IBMPowerVSMachineTemplate{}, &capibmv1.IBMPowerVSCluster{}, nil
	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

// compareCAPIInfraMachineTemplates compares CAPI infra machine templates a and b, and returns a list of differences, or none if there are none.
func compareCAPIInfraMachineTemplates(platform configv1.PlatformType, infraMachineTemplate1, infraMachineTemplate2 client.Object) ([]string, error) {
	switch platform {
	case configv1.AWSPlatformType:
		typedInfraMachineTemplate1, ok := infraMachineTemplate1.(*awscapiv1beta1.AWSMachineTemplate)
		if !ok {
			return nil, errAssertingCAPIAWSMachineTemplate
		}

		typedinfraMachineTemplate2, ok := infraMachineTemplate2.(*awscapiv1beta1.AWSMachineTemplate)
		if !ok {
			return nil, errAssertingCAPIAWSMachineTemplate
		}

		var diff []string
		diff = append(diff, deep.Equal(typedInfraMachineTemplate1.Spec, typedinfraMachineTemplate2.Spec)...)
		diff = append(diff, util.ObjectMetaEqual(typedInfraMachineTemplate1.ObjectMeta, typedinfraMachineTemplate2.ObjectMeta)...)

		return diff, nil
	case configv1.PowerVSPlatformType:
		typedInfraMachineTemplate1, ok := infraMachineTemplate1.(*capibmv1.IBMPowerVSMachineTemplate)
		if !ok {
			return nil, errAssertingCAPIIBMPowerVSMachineTemplate
		}

		typedinfraMachineTemplate2, ok := infraMachineTemplate2.(*capibmv1.IBMPowerVSMachineTemplate)
		if !ok {
			return nil, errAssertingCAPIIBMPowerVSMachineTemplate
		}

		var diff []string
		diff = append(diff, deep.Equal(typedInfraMachineTemplate1.Spec, typedinfraMachineTemplate2.Spec)...)
		diff = append(diff, util.ObjectMetaEqual(typedInfraMachineTemplate1.ObjectMeta, typedinfraMachineTemplate2.ObjectMeta)...)

		return diff, nil
	default:
		return nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

// compareCAPIMachineSets compares CAPI machineSets a and b, and returns a list of differences, or none if there are none.
func compareCAPIMachineSets(capiMachineSet1, capiMachineSet2 *capiv1beta1.MachineSet) []string {
	var diff []string
	diff = append(diff, deep.Equal(capiMachineSet1.Spec, capiMachineSet2.Spec)...)
	diff = append(diff, util.ObjectMetaEqual(capiMachineSet1.ObjectMeta, capiMachineSet2.ObjectMeta)...)

	return diff
}

// compareMAPIMachineSets compares MAPI machineSets a and b, and returns a list of differences, or none if there are none.
func compareMAPIMachineSets(mapiMachineSet1, mapiMachineSet2 *machinev1beta1.MachineSet) []string {
	var diff []string
	diff = append(diff, deep.Equal(mapiMachineSet1.Spec, mapiMachineSet2.Spec)...)
	diff = append(diff, util.ObjectMetaEqual(mapiMachineSet1.ObjectMeta, mapiMachineSet2.ObjectMeta)...)

	return diff
}

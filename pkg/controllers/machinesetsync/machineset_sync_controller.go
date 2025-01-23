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
	"reflect"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

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
	errAssertingCAPIAWSMachineTemplate = errors.New("error asserting the CAPI AWSMachineTemplate object")

	// errAssertingCAPIPowerVSMachineTemplate is returned when we encounter an issue asserting a client.Object into a IBMPowerVSMachineTemplate.
	errAssertingCAPIIBMPowerVSMachineTemplate = errors.New("error asserting the CAPI IBMPowerVSMachineTemplate object")
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

	messageSuccessfullySynchronized = "Successfully synchronized CAPI MachineSet to MAPI"
)

// MachineSetSyncController reconciles CAPI and MAPI MachineSets.
type MachineSetSyncController struct {
	operatorstatus.ClusterOperatorStatusClient
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Infra         *configv1.Infrastructure
	Platform      configv1.PlatformType
	CAPINamespace string
	MAPINamespace string
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineSetSyncController) SetupWithManager(mgr ctrl.Manager) error {
	infraMachineTemplate, err := getInfraMachineTemplateFromProvider(r.Platform)
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
	r.Recorder = mgr.GetEventRecorderFor("machineset-sync-controller")

	return nil
}

// Reconcile reconciles CAPI and MAPI MachineSets for their respective namespaces.
func (r *MachineSetSyncController) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("namespace", req.Namespace, "name", req.Name)
	ctx = logr.NewContext(ctx, logger)

	logger.V(1).Info("Reconciling machine set")
	defer logger.V(1).Info("Finished reconciling machine set")

	mapiMachineSet, capiMachineSet, err := r.fetchMachineSets(ctx, req.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to fetch machine sets: %w", err)
	}

	if mapiMachineSet == nil && capiMachineSet == nil {
		logger.Info("Both MAPI and CAPI machine sets not found, nothing to do")

		if err := r.setControllerConditionsToNormal(ctx, logger); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set conditions for machine set sync controller: %w", err)
		}

		return ctrl.Result{}, nil
	}

	if mapiMachineSet == nil {
		logger.Info("Only CAPI machine set found, nothing to do")

		if err := r.setControllerConditionsToNormal(ctx, logger); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set conditions for machine set sync controller: %w", err)
		}

		return ctrl.Result{}, nil
	}

	result, err := r.syncMachineSets(ctx, mapiMachineSet, capiMachineSet)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to sync machine sets: %w", err)
	}

	if err := r.setControllerConditionsToNormal(ctx, logger); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set conditions for machine set sync controller: %w", err)
	}

	return result, nil
}

// fetchMachineSets fetches both MAPI and CAPI MachineSets.
func (r *MachineSetSyncController) fetchMachineSets(ctx context.Context, name string) (*machinev1beta1.MachineSet, *capiv1beta1.MachineSet, error) {
	logger := log.FromContext(ctx)

	mapiMachineSet := &machinev1beta1.MachineSet{}

	capiMachineSet := &capiv1beta1.MachineSet{}

	if err := r.Get(ctx, client.ObjectKey{Namespace: r.MAPINamespace, Name: name}, mapiMachineSet); apierrors.IsNotFound(err) {
		logger.Info("MAPI machine set not found")

		mapiMachineSet = nil
	} else if err != nil {
		return nil, nil, fmt.Errorf("failed to get MAPI machine set: %w", err)
	}

	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: name}, capiMachineSet); apierrors.IsNotFound(err) {
		logger.Info("CAPI machine set not found")

		capiMachineSet = nil
	} else if err != nil {
		return nil, nil, fmt.Errorf("failed to get CAPI machine set: %w", err)
	}

	return mapiMachineSet, capiMachineSet, nil
}

// fetchCAPIInfraResources fetches the provider specific infrastructure resources depending on which provider is set.
func (r *MachineSetSyncController) fetchCAPIInfraResources(ctx context.Context, capiMachineSet *capiv1beta1.MachineSet) (client.Object, client.Object, error) {
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

	switch r.Platform {
	case configv1.AWSPlatformType:
		infraCluster = &awscapiv1beta1.AWSCluster{}
		infraMachineTemplate = &awscapiv1beta1.AWSMachineTemplate{}
	case configv1.PowerVSPlatformType:
		infraCluster = &capibmv1.IBMPowerVSCluster{}
		infraMachineTemplate = &capibmv1.IBMPowerVSMachineTemplate{}
	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, r.Platform)
	}

	if err := r.Get(ctx, infraClusterKey, infraCluster); err != nil {
		return nil, nil, fmt.Errorf("failed to get CAPI infrastructure cluster: %w", err)
	}

	if err := r.Get(ctx, infraMachineTemplateKey, infraMachineTemplate); err != nil {
		return nil, nil, fmt.Errorf("failed to get CAPI infrastructure machine template: %w", err)
	}

	return infraCluster, infraMachineTemplate, nil
}

// syncMachineSets synchronizes MachineSets based on the authoritative API.
func (r *MachineSetSyncController) syncMachineSets(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet) (ctrl.Result, error) {
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
func (r *MachineSetSyncController) reconcileMAPIMachineSetToCAPIMachineSet(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	newCAPIMachineSet, newCAPIInfraMachineTemplate, warns, err := r.convertMAPIToCAPIMachineSet(mapiMachineSet)
	if err != nil {
		conversionErr := fmt.Errorf("failed to convert MAPI machine set to CAPI machine set: %w", err)
		if condErr := r.updateSynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineSetToCAPI, conversionErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{conversionErr, condErr})
		}

		return ctrl.Result{}, conversionErr
	}

	for _, warning := range warns {
		logger.Info("Warning during conversion", "warning", warning)
		r.Recorder.Event(mapiMachineSet, corev1.EventTypeWarning, "ConversionWarning", warning)
	}

	newCAPIMachineSet.SetResourceVersion(getResourceVersion(client.Object(capiMachineSet)))
	newCAPIMachineSet.SetNamespace(r.CAPINamespace)
	newCAPIMachineSet.Spec.Template.Spec.InfrastructureRef.Namespace = r.CAPINamespace

	_, infraMachineTemplate, err := r.fetchCAPIInfraResources(ctx, newCAPIMachineSet)
	if err != nil && !apierrors.IsNotFound(err) {
		fetchErr := fmt.Errorf("failed to fetch CAPI infra resources: %w", err)

		if condErr := r.updateSynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, fetchErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{fetchErr, condErr})
		}

		return ctrl.Result{}, fetchErr
	}

	newCAPIInfraMachineTemplate.SetResourceVersion(getResourceVersion(infraMachineTemplate))
	newCAPIInfraMachineTemplate.SetNamespace(r.CAPINamespace)

	if result, err := r.createOrUpdateCAPIInfraMachineTemplate(ctx, mapiMachineSet, infraMachineTemplate, newCAPIInfraMachineTemplate); err != nil {
		return result, fmt.Errorf("unable to ensure CAPI infra machine template: %w", err)
	}

	if result, err := r.createOrUpdateCAPIMachineSet(ctx, mapiMachineSet, capiMachineSet, newCAPIMachineSet); err != nil {
		return result, fmt.Errorf("unable to ensure CAPI machine set: %w", err)
	}

	return ctrl.Result{}, r.updateSynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionTrue,
		consts.ReasonResourceSynchronized, messageSuccessfullySynchronized, &mapiMachineSet.Generation)
}

// reconcileCAPIMachineSetToMAPIMachineSet reconciles a CAPI MachineSet to a
// MAPI MachineSet.
func (r *MachineSetSyncController) reconcileCAPIMachineSetToMAPIMachineSet(ctx context.Context, capiMachineSet *capiv1beta1.MachineSet, mapiMachineSet *machinev1beta1.MachineSet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	infraCluster, infraMachineTemplate, err := r.fetchCAPIInfraResources(ctx, capiMachineSet)
	if err != nil {
		fetchErr := fmt.Errorf("failed to fetch CAPI infra resources: %w", err)

		if condErr := r.updateSynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, fetchErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{fetchErr, condErr})
		}

		return ctrl.Result{}, fetchErr
	}

	newMapiMachineSet, warns, err := r.convertCAPIToMAPIMachineSet(capiMachineSet, infraMachineTemplate, infraCluster)
	if err != nil {
		conversionErr := fmt.Errorf("failed to convert CAPI machine set to MAPI machine set: %w", err)

		if condErr := r.updateSynchronizedConditionWithPatch(
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
	newMapiMachineSet.SetResourceVersion(getResourceVersion(mapiMachineSet))

	if !reflect.DeepEqual(newMapiMachineSet.Spec, mapiMachineSet.Spec) || !objectMetaIsEqual(newMapiMachineSet.ObjectMeta, mapiMachineSet.ObjectMeta) {
		logger.Info("Updating MAPI machine set")

		if err := r.Update(ctx, newMapiMachineSet); err != nil {
			logger.Error(err, "Failed to update MAPI machine set")

			updateErr := fmt.Errorf("failed to update MAPI machine set: %w", err)

			if condErr := r.updateSynchronizedConditionWithPatch(
				ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateMAPIMachineSet, updateErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, utilerrors.NewAggregate([]error{updateErr, condErr})
			}

			return ctrl.Result{}, updateErr
		}

		logger.Info("Successfully updated MAPI machine set")
	} else {
		logger.Info("No changes detected in MAPI machine set")
	}

	return ctrl.Result{}, r.updateSynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionTrue,
		consts.ReasonResourceSynchronized, messageSuccessfullySynchronized, &capiMachineSet.Generation)
}

// convertCAPIToMAPIMachineSet converts a CAPI MachineSet to a MAPI MachineSet, selecting the correct converter based on the platform.
func (r *MachineSetSyncController) convertCAPIToMAPIMachineSet(capiMachineSet *capiv1beta1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) (*machinev1beta1.MachineSet, []string, error) {
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
func (r *MachineSetSyncController) convertMAPIToCAPIMachineSet(mapiMachineSet *machinev1beta1.MachineSet) (*capiv1beta1.MachineSet, client.Object, []string, error) {
	switch r.Platform {
	case configv1.AWSPlatformType:
		return mapi2capi.FromAWSMachineSetAndInfra(mapiMachineSet, r.Infra).ToMachineSetAndMachineTemplate() //nolint:wrapcheck
	case configv1.PowerVSPlatformType:
		return mapi2capi.FromPowerVSMachineSetAndInfra(mapiMachineSet, r.Infra).ToMachineSetAndMachineTemplate() //nolint:wrapcheck
	default:
		return nil, nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, r.Platform)
	}
}

// updateSynchronizedConditionWithPatch updates the synchronized condition
// using a server side apply patch. We do this to force ownership of the
// 'Synchronized' condition and 'SynchronizedGeneration'.
func (r *MachineSetSyncController) updateSynchronizedConditionWithPatch(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, status corev1.ConditionStatus, reason, message string, generation *int64) error {
	var severity machinev1beta1.ConditionSeverity
	if status == corev1.ConditionTrue {
		severity = machinev1beta1.ConditionSeverityNone
	} else {
		severity = machinev1beta1.ConditionSeverityError
	}

	conditionAc := machinev1applyconfigs.Condition().
		WithType(consts.SynchronizedCondition).
		WithStatus(status).
		WithReason(reason).
		WithMessage(message).
		WithSeverity(severity)

	setLastTransitionTime(consts.SynchronizedCondition, mapiMachineSet.Status.Conditions, conditionAc)

	statusAc := machinev1applyconfigs.MachineSetStatus().
		WithConditions(conditionAc)

	if status == corev1.ConditionTrue && generation != nil {
		statusAc = statusAc.WithSynchronizedGeneration(*generation)
	}

	msAc := machinev1applyconfigs.MachineSet(mapiMachineSet.GetName(), mapiMachineSet.GetNamespace()).
		WithStatus(statusAc)

	if err := r.Status().Patch(ctx, mapiMachineSet, util.ApplyConfigPatch(msAc), client.ForceOwnership, client.FieldOwner("machineset-sync-controller")); err != nil {
		return fmt.Errorf("failed to patch MAPI machine set status with synchronized condition: %w", err)
	}

	return nil
}

// createOrUpdateCAPIInfraMachineTemplate creates a CAPI infra machine template from a MAPI machine set, or updates if it exists and it is out of date.
func (r *MachineSetSyncController) createOrUpdateCAPIInfraMachineTemplate(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, infraMachineTemplate client.Object, newCAPIInfraMachineTemplate client.Object) (ctrl.Result, error) { //nolint:unparam
	logger := log.FromContext(ctx)

	if infraMachineTemplate == nil {
		if err := r.Create(ctx, newCAPIInfraMachineTemplate); err != nil {
			logger.Error(err, "Failed to create CAPI infra machine template")
			createErr := fmt.Errorf("failed to create CAPI infra machine template: %w", err)

			if condErr := r.updateSynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToCreateCAPIInfraMachineTemplate, createErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, utilerrors.NewAggregate([]error{createErr, condErr})
			}

			return ctrl.Result{}, createErr
		}

		logger.Info("Successfully created CAPI infra machine template")

		return ctrl.Result{}, nil
	}

	isEqualCAPIInfraMachineTemplate, err := capiInfraMachineTemplateIsEqual(r.Platform, infraMachineTemplate, newCAPIInfraMachineTemplate)
	if err != nil {
		logger.Error(err, "Failed to check CAPI infra machine template diff")
		updateErr := fmt.Errorf("failed to check CAPI infra machine template diff: %w", err)

		if condErr := r.updateSynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachineTemplate, updateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return ctrl.Result{}, updateErr
	}

	if isEqualCAPIInfraMachineTemplate {
		logger.Info("No changes detected in CAPI infra machine template")
		return ctrl.Result{}, nil
	}

	logger.Info("Updating CAPI infra machine template")

	if err := r.Update(ctx, newCAPIInfraMachineTemplate); err != nil {
		logger.Error(err, "Failed to update CAPI infra machine template")

		updateErr := fmt.Errorf("failed to update CAPI infra machine template: %w", err)

		if condErr := r.updateSynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachineTemplate, updateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return ctrl.Result{}, updateErr
	}

	logger.Info("Successfully updated CAPI infra machine template")

	return ctrl.Result{}, nil
}

// createOrUpdateCAPIMachineSet creates a CAPI machine set from a MAPI one, or updates if it exists and it is out of date.
func (r *MachineSetSyncController) createOrUpdateCAPIMachineSet(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet, newCAPIMachineSet *capiv1beta1.MachineSet) (ctrl.Result, error) { //nolint:unparam
	logger := log.FromContext(ctx)

	if capiMachineSet == nil {
		if err := r.Create(ctx, newCAPIMachineSet); err != nil {
			logger.Error(err, "Failed to create CAPI machine set")

			createErr := fmt.Errorf("failed to create CAPI machine set: %w", err)
			if condErr := r.updateSynchronizedConditionWithPatch(
				ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToCreateCAPIMachineSet, createErr.Error(), nil); condErr != nil {
				return ctrl.Result{}, utilerrors.NewAggregate([]error{createErr, condErr})
			}

			return ctrl.Result{}, createErr
		}

		logger.Info("Successfully created CAPI machine set")

		return ctrl.Result{}, nil
	}

	if reflect.DeepEqual(newCAPIMachineSet.Spec, capiMachineSet.Spec) && objectMetaIsEqual(newCAPIMachineSet.ObjectMeta, capiMachineSet.ObjectMeta) {
		logger.Info("No changes detected in CAPI machine set")
		return ctrl.Result{}, nil
	}

	logger.Info("Updating CAPI machine set")

	if err := r.Update(ctx, newCAPIMachineSet); err != nil {
		logger.Error(err, "Failed to update CAPI machine set")

		updateErr := fmt.Errorf("failed to update CAPI machine set: %w", err)

		if condErr := r.updateSynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachineSet, updateErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return ctrl.Result{}, updateErr
	}

	logger.Info("Successfully updated CAPI machine set")

	return ctrl.Result{}, nil
}

// getInfraMachineTemplateFromProvider returns the correct InfraMachineTemplate implementation
// for a given provider.
func getInfraMachineTemplateFromProvider(platform configv1.PlatformType) (client.Object, error) {
	switch platform {
	case configv1.AWSPlatformType:
		return &awscapiv1beta1.AWSMachineTemplate{}, nil
	case configv1.PowerVSPlatformType:
		return &capibmv1.IBMPowerVSMachineTemplate{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

// setLastTransitionTime determines if the last transition time should be set or updated for a given condition type.
func setLastTransitionTime(condType machinev1beta1.ConditionType, conditions []machinev1beta1.Condition, conditionAc *machinev1applyconfigs.ConditionApplyConfiguration) {
	for _, condition := range conditions {
		if condition.Type == condType {
			if !hasSameState(&condition, conditionAc) {
				conditionAc.WithLastTransitionTime(metav1.Now())

				return
			}

			conditionAc.WithLastTransitionTime(condition.LastTransitionTime)

			return
		}
	}
	// Condition does not exist; set the transition time
	conditionAc.WithLastTransitionTime(metav1.Now())
}

// hasSameState returns true if a condition has the same state as a condition
// apply config; state is defined by the union of following fields: Type,
// Status.
func hasSameState(i *machinev1beta1.Condition, j *machinev1applyconfigs.ConditionApplyConfiguration) bool {
	return i.Type == *j.Type &&
		i.Status == *j.Status
}

// objectMetaIsEqual determines if the two ObjectMeta are equal for the fields we care about
// when synchronising MAPI and CAPI MachineSets.
func objectMetaIsEqual(a, b metav1.ObjectMeta) bool {
	return reflect.DeepEqual(a.Labels, b.Labels) &&
		reflect.DeepEqual(a.Annotations, b.Annotations) &&
		reflect.DeepEqual(a.Finalizers, b.Finalizers) &&
		reflect.DeepEqual(a.OwnerReferences, b.OwnerReferences)
}

// capiInfraMachineTemplateIsEqual checks whether the provided CAPI infra machine templates are equal.
func capiInfraMachineTemplateIsEqual(platform configv1.PlatformType, infraMachineTemplate1, infraMachineTemplate2 client.Object) (bool, error) {
	switch platform {
	case configv1.AWSPlatformType:
		typedInfraMachineTemplate1, ok := infraMachineTemplate1.(*awscapiv1beta1.AWSMachineTemplate)
		if !ok {
			return false, errAssertingCAPIAWSMachineTemplate
		}

		typedinfraMachineTemplate2, ok := infraMachineTemplate2.(*awscapiv1beta1.AWSMachineTemplate)
		if !ok {
			return false, errAssertingCAPIAWSMachineTemplate
		}

		return reflect.DeepEqual(typedInfraMachineTemplate1.Spec, typedinfraMachineTemplate2.Spec) && objectMetaIsEqual(typedInfraMachineTemplate1.ObjectMeta, typedinfraMachineTemplate2.ObjectMeta), nil
	case configv1.PowerVSPlatformType:
		typedInfraMachineTemplate1, ok := infraMachineTemplate1.(*capibmv1.IBMPowerVSMachineTemplate)
		if !ok {
			return false, errAssertingCAPIIBMPowerVSMachineTemplate
		}

		typedinfraMachineTemplate2, ok := infraMachineTemplate2.(*capibmv1.IBMPowerVSMachineTemplate)
		if !ok {
			return false, errAssertingCAPIIBMPowerVSMachineTemplate
		}

		return reflect.DeepEqual(typedInfraMachineTemplate1.Spec, typedinfraMachineTemplate2.Spec) && objectMetaIsEqual(typedInfraMachineTemplate1.ObjectMeta, typedinfraMachineTemplate2.ObjectMeta), nil
	default:
		return false, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

// getResourceVersion returns the object ResourceVersion or the zero value for it.
func getResourceVersion(obj client.Object) string {
	if obj == nil || reflect.ValueOf(obj).IsNil() {
		return "0"
	}

	return obj.GetResourceVersion()
}

// setControllerConditionsToNormal sets the MachineSetSyncController conditions to the normal state.
func (r *MachineSetSyncController) setControllerConditionsToNormal(ctx context.Context, log logr.Logger) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster operator: %w", err)
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.MachineSetSyncControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"MachineSet Sync Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.MachineSetSyncControllerDegradedCondition, configv1.ConditionFalse, operatorstatus.ReasonAsExpected,
			"MachineSet Sync Controller works as expected"),
	}

	log.V(2).Info("MachineSet Sync Controller is Available")

	if err := r.SyncStatus(ctx, co, conds); err != nil {
		return fmt.Errorf("failed to sync cluster operator status: %w", err)
	}

	return nil
}

// setControllerConditionDegraded sets the MachineSetSyncController conditions to a degraded state.
//
//nolint:unused
func (r *MachineSetSyncController) setControllerConditionDegraded(ctx context.Context, log logr.Logger, reconcileErr error) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster operator: %w", err)
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.MachineSetSyncControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"MachineSet Sync Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(operatorstatus.MachineSetSyncControllerDegradedCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			fmt.Sprintf("MachineSet Sync Controller is degraded: %s", reconcileErr.Error())),
	}

	log.Info("MachineSet Sync Controller is Degraded", reconcileErr.Error())

	if err := r.SyncStatus(ctx, co, conds); err != nil {
		return fmt.Errorf("failed to sync cluster operator status: %w", err)
	}

	return nil
}

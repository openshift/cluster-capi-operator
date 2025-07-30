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
	"slices"
	"sort"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinesync"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/synccommon"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/go-test/deep"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/annotations"
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

	// errUnexpectedInfraMachineTemplateListType is returned when we receive an unexpected InfraStructureMachineTemplateList type.
	errUnexpectedInfraMachineTemplateListType = errors.New("unexpected InfraMachineTemplateList type")

	// errUnexpectedInfraClusterType is returned when we receive an unexpected InfraCluster type.
	errUnexpectedInfraClusterType = errors.New("unexpected InfraCluster type")

	// errAssertingCAPIAWSMachineTemplate is returned when we encounter an issue asserting a client.Object into a AWSMachineTemplate.
	errAssertingCAPIAWSMachineTemplate = errors.New("error asserting the CAPI AWSMachineTemplate object")

	// errAssertingCAPIPowerVSMachineTemplate is returned when we encounter an issue asserting a client.Object into a IBMPowerVSMachineTemplate.
	errAssertingCAPIIBMPowerVSMachineTemplate = errors.New("error asserting the CAPI IBMPowerVSMachineTemplate object")

	// errAssertingCAPIOpenStackMachineTemplate is returned when we encounter an issue asserting a client.Object into a OpenStackMachineTemplate.
	errAssertingCAPIOpenStackMachineTemplate = errors.New("error asserting the CAPI OpenStackMachineTemplate object")

	// errUnsuportedOwnerKindForConversion is returned when the owner kind is not supported for conversion.
	errUnsuportedOwnerKindForConversion = errors.New("unsupported owner kind for conversion")

	// errMachineAPIMachineSetOwnerReferenceConversionUnsupported.
	errMachineAPIMachineSetOwnerReferenceConversionUnsupported = errors.New("could not convert Machine API machine set owner references to Cluster API")
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

	messageSuccessfullySynchronizedCAPItoMAPI = "Successfully synchronized CAPI MachineSet to MAPI"
	messageSuccessfullySynchronizedMAPItoCAPI = "Successfully synchronized MAPI MachineSet to CAPI"

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
			handler.EnqueueRequestsFromMapFunc(util.ResolveCAPIMachineSetFromInfraMachineTemplate(r.MAPINamespace)),
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
		logger.Info("Both MAPI and CAPI machine sets not found, nothing to do")
		return ctrl.Result{}, nil
	}

	if mapiMachineSet == nil {
		logger.Info("Only CAPI machine set found, nothing to do")
		return ctrl.Result{}, nil
	}

	return r.syncMachineSets(ctx, mapiMachineSet, capiMachineSet)
}

// fetchMachineSets fetches both MAPI and CAPI MachineSets.
func (r *MachineSetSyncReconciler) fetchMachineSets(ctx context.Context, name string) (*machinev1beta1.MachineSet, *clusterv1.MachineSet, error) {
	logger := log.FromContext(ctx)

	mapiMachineSet := &machinev1beta1.MachineSet{}

	capiMachineSet := &clusterv1.MachineSet{}

	if err := r.Get(ctx, client.ObjectKey{Namespace: r.MAPINamespace, Name: name}, mapiMachineSet); apierrors.IsNotFound(err) {
		logger.Info("MAPI machine set not found")

		mapiMachineSet = nil
	} else if err != nil {
		return nil, nil, fmt.Errorf("failed to get MAPI machine set: %w", err)
	} else {
		logger.V(4).Info("MAPI machine set found")
	}

	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: name}, capiMachineSet); apierrors.IsNotFound(err) {
		logger.Info("CAPI machine set not found")

		capiMachineSet = nil
	} else if err != nil {
		return nil, nil, fmt.Errorf("failed to get CAPI machine set: %w", err)
	} else {
		logger.V(4).Info("CAPI machine set found")
	}

	return mapiMachineSet, capiMachineSet, nil
}

// fetchCAPIInfraResources fetches the provider specific infrastructure resources depending on which provider is set.
func (r *MachineSetSyncReconciler) fetchCAPIInfraResources(ctx context.Context, capiMachineSet *clusterv1.MachineSet) (client.Object, client.Object, error) {
	var infraCluster, infraMachineTemplate client.Object

	logger := log.FromContext(ctx)

	infraClusterKey := client.ObjectKey{
		Namespace: capiMachineSet.Namespace,
		Name:      capiMachineSet.Spec.ClusterName,
	}

	infraMachineTemplateRef := capiMachineSet.Spec.Template.Spec.InfrastructureRef
	infraMachineTemplateKey := client.ObjectKey{
		Namespace: infraMachineTemplateRef.Namespace,
		Name:      infraMachineTemplateRef.Name,
	}

	infraMachineTemplate, infraCluster, err := controllers.InitInfraMachineTemplateAndInfraClusterFromProvider(r.Platform)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to devise CAPI infra resources: %w", err)
	}

	if err := r.Get(ctx, infraClusterKey, infraCluster); err != nil {
		return nil, nil, fmt.Errorf("failed to get CAPI infrastructure cluster: %w", err)
	} else {
		logger.V(4).Info("CAPI infrastructure cluster found")
	}

	if err := r.Get(ctx, infraMachineTemplateKey, infraMachineTemplate); err != nil {
		return nil, nil, fmt.Errorf("failed to get CAPI infrastructure machine template: %w", err)
	} else {
		logger.V(4).Info("CAPI infrastructure machine template found")
	}

	return infraCluster, infraMachineTemplate, nil
}

// syncMachineSets synchronizes MachineSets based on the authoritative API.
func (r *MachineSetSyncReconciler) syncMachineSets(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *clusterv1.MachineSet) (ctrl.Result, error) {
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
		logger.Info("Machine set is currently being migrated")
		return ctrl.Result{}, nil
	case authoritativeAPI == "":
		logger.Info("Machine set status.authoritativeAPI is empty, will check again later", "AuthoritativeAPI", mapiMachineSet.Status.AuthoritativeAPI)
		return ctrl.Result{}, nil
	default:
		logger.Info("Unexpected value for authoritativeAPI", "AuthoritativeAPI", mapiMachineSet.Status.AuthoritativeAPI)

		return ctrl.Result{}, nil
	}
}

// reconcileMAPIMachineSetToCAPIMachineSet reconciles a MAPI MachineSet to a CAPI MachineSet.
//
//nolint:funlen
func (r *MachineSetSyncReconciler) reconcileMAPIMachineSetToCAPIMachineSet(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *clusterv1.MachineSet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	authoritativeAPI := mapiMachineSet.Status.AuthoritativeAPI
	if authoritativeAPI == machinev1beta1.MachineAuthorityClusterAPI {
		logger.Info("AuthoritativeAPI is set to Cluster API, but no Cluster API machine set exists. Running an initial Machine API to Cluster API sync")
	}

	if shouldRequeue, err := r.reconcileMAPItoCAPIMachineSetDeletion(ctx, mapiMachineSet, capiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile Machine API to Cluster API machine set deletion: %w", err)
	} else if shouldRequeue {
		return ctrl.Result{}, nil
	}

	if shouldRequeue, err := r.ensureSyncFinalizer(ctx, mapiMachineSet, capiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure sync finalizer: %w", err)
	} else if shouldRequeue {
		return ctrl.Result{}, nil
	}

	if err := r.validateMAPIMachineSetOwnerReferences(mapiMachineSet); err != nil {
		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineSetToCAPI, err.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{err, condErr})
		}

		if errors.Is(err, errMachineAPIMachineSetOwnerReferenceConversionUnsupported) {
			logger.Error(err, "unable to convert Machine API machine set to Cluster API, owner references conversion is not supported for machine set")
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, fmt.Errorf("failed to convert Machine API machine set owner references to Cluster API: %w", err)
		}
	}

	clusterOwnerRefence, err := r.fetchCAPIClusterOwnerReference(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Cluster API cluster owner reference: %w", err)
	}

	newCAPIMachineSet, newCAPIInfraMachineTemplate, warns, err := r.convertMAPIToCAPIMachineSet(mapiMachineSet)
	if err != nil {
		conversionErr := fmt.Errorf("failed to convert MAPI machine set to CAPI machine set: %w", err)
		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToConvertMAPIMachineSetToCAPI, conversionErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{conversionErr, condErr})
		}

		return ctrl.Result{}, conversionErr
	}

	for _, warning := range warns {
		logger.Info("Warning during conversion", "warning", warning)
		r.Recorder.Event(mapiMachineSet, corev1.EventTypeWarning, "ConversionWarning", warning)
	}

	restoreCAPIFields(capiMachineSet, newCAPIMachineSet, r.CAPINamespace, authoritativeAPI, clusterOwnerRefence)

	if err := r.ensureCAPIInfraMachineTemplate(ctx, mapiMachineSet, newCAPIMachineSet, newCAPIInfraMachineTemplate, clusterOwnerRefence); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure CAPI infra machine template: %w", err)
	}

	if err := r.createOrUpdateCAPIMachineSet(ctx, mapiMachineSet, capiMachineSet, newCAPIMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure CAPI machine set: %w", err)
	}

	shouldRequeue, err := r.deleteOutdatedCAPIInfraMachineTemplates(ctx, mapiMachineSet, newCAPIInfraMachineTemplate.GetName())
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to delete outdated Cluster API infrastructure machine templates: %w", err)
	} else if shouldRequeue {
		logger.Info("Waiting for Cluster API infrastructure machine templates to be deleted")
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, r.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionTrue,
		controllers.ReasonResourceSynchronized, messageSuccessfullySynchronizedMAPItoCAPI, &mapiMachineSet.Generation)
}

// filterOutdatedInfraMachineTemplates takes infraMachineTemplatesList and constructs a slice of InfraMachineTemplates without newInfraMachineTemplate.
func filterOutdatedInfraMachineTemplates(infraMachineTemplateList client.ObjectList, newInfraMachineTemplateName string) ([]client.Object, error) {
	outdatedTemplates := []client.Object{}

	switch list := infraMachineTemplateList.(type) {
	case *awsv1.AWSMachineTemplateList:
		for _, template := range list.Items {
			if template.GetName() != newInfraMachineTemplateName {
				outdatedTemplates = append(outdatedTemplates, &template)
			}
		}
	case *ibmpowervsv1.IBMPowerVSMachineTemplateList:
		for _, template := range list.Items {
			if template.GetName() != newInfraMachineTemplateName {
				outdatedTemplates = append(outdatedTemplates, &template)
			}
		}
	case *openstackv1.OpenStackMachineTemplateList:
		for _, template := range list.Items {
			if template.GetName() != newInfraMachineTemplateName {
				outdatedTemplates = append(outdatedTemplates, &template)
			}
		}
	default:
		return nil, fmt.Errorf("%w: got unknown type %T", errUnexpectedInfraMachineTemplateListType, list)
	}

	return outdatedTemplates, nil
}

// deleteOutdatedCAPIInfraMachineTemplates deletes infra machine templates that have MAPI machine set label and are not newCAPIInfraMachineTemplateName.
func (r *MachineSetSyncReconciler) deleteOutdatedCAPIInfraMachineTemplates(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, newCAPIInfraMachineTemplateName string) (bool, error) {
	logger := log.FromContext(ctx)

	outdatedTemplates, err := r.listOutdatedInfraMachineTemplates(ctx, mapiMachineSet, newCAPIInfraMachineTemplateName)
	if err != nil {
		return false, err
	}

	if len(outdatedTemplates) == 0 {
		// There is nothing to delete. We are done.
		return false, nil
	}

	templatesToDelete, deletingTemplates := categorizeInfraMachineTemplates(outdatedTemplates)

	if len(templatesToDelete) > 0 {
		infraMachineTemplateNames := make([]string, 0, len(templatesToDelete))
		for _, template := range templatesToDelete {
			infraMachineTemplateNames = append(infraMachineTemplateNames, template.GetName())
		}

		logger.Info("Found Cluster API infrastructure machine templates without deletion timestamp. Proceeding to delete", "infraMachineTemplateNames", infraMachineTemplateNames)

		if err := r.deleteAllOutdatedCAPIInfraMachineTemplates(ctx, mapiMachineSet, newCAPIInfraMachineTemplateName); err != nil {
			return true, fmt.Errorf("unable to delete outdated Cluster API infrastructure machine templates: %w", err)
		}

		return true, nil
	}

	// If we reach here, all outdated templates are being deleted.
	logger.Info("Still waiting for Cluster API infrastructure machine templates to be deleted", "remaining", len(deletingTemplates))

	return true, nil
}

// listOutdatedInfraMachineTemplates lists and filters outdated infrastructure machine templates.
func (r *MachineSetSyncReconciler) listOutdatedInfraMachineTemplates(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, newCAPIInfraMachineTemplateName string) ([]client.Object, error) {
	logger := log.FromContext(ctx)

	machineSetMAPILabelSelector := labels.SelectorFromSet(map[string]string{controllers.MachineSetOpenshiftLabelKey: mapiMachineSet.Name})
	listOptions := []client.ListOption{
		client.InNamespace(r.CAPINamespace),
		client.MatchingLabelsSelector{Selector: machineSetMAPILabelSelector},
	}

	infraTemplateList, _, err := initInfraMachineTemplateListAndInfraClusterListFromProvider(r.Platform)
	if err != nil {
		return nil, fmt.Errorf("failed to get infrastructure machine template list from platform: %w", err)
	}

	if err := r.List(ctx, infraTemplateList, listOptions...); err != nil {
		logger.Error(err, "Failed to list Cluster API infrastructure machine templates")
		return nil, fmt.Errorf("failed to list Cluster API infrastructure machine templates: %w", err)
	}

	outdatedTemplates, err := filterOutdatedInfraMachineTemplates(infraTemplateList, newCAPIInfraMachineTemplateName)
	if err != nil {
		return nil, fmt.Errorf("failed to filter outdated Cluster API infrastructure machine templates: %w", err)
	}

	return outdatedTemplates, nil
}

// categorizeInfraMachineTemplates separates templates into those that need deletion and those already being deleted.
func categorizeInfraMachineTemplates(outdatedTemplates []client.Object) (templatesToDelete []client.Object, deletingTemplates []client.Object) {
	for _, template := range outdatedTemplates {
		if template.GetDeletionTimestamp().IsZero() {
			templatesToDelete = append(templatesToDelete, template)
		} else {
			deletingTemplates = append(deletingTemplates, template)
		}
	}

	return templatesToDelete, deletingTemplates
}

// deleteAllOutdatedCAPIInfraMachineTemplates deletes infra machine templates that have MAPI machine set label that of the current machine set and are not newCAPIInfraMachineTemplateName.
func (r *MachineSetSyncReconciler) deleteAllOutdatedCAPIInfraMachineTemplates(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, newCAPIInfraMachineTemplateName string) error {
	logger := log.FromContext(ctx)

	notNewCAPIInfraMachineTemplateNameFieldSelector := fields.OneTermNotEqualSelector("metadata.name", newCAPIInfraMachineTemplateName)
	machineSetMAPILabelSelector := labels.SelectorFromSet(map[string]string{controllers.MachineSetOpenshiftLabelKey: mapiMachineSet.Name})

	deleteAllOption := []client.DeleteAllOfOption{
		client.InNamespace(r.CAPINamespace),
		client.MatchingFieldsSelector{Selector: notNewCAPIInfraMachineTemplateNameFieldSelector},
		client.MatchingLabelsSelector{Selector: machineSetMAPILabelSelector},
	}

	infraMachineTemplate, _, err := controllers.InitInfraMachineTemplateAndInfraClusterFromProvider(r.Platform)
	if err != nil {
		return fmt.Errorf("failed to get infrastructure machine template from Platform: %w", err)
	}

	if err := r.DeleteAllOf(ctx, infraMachineTemplate, deleteAllOption...); err != nil {
		logger.Error(err, "Failed to delete outdated Cluster API infrastructure machine templates")

		updateErr := fmt.Errorf("failed to delete outdated Cluster API infrastructure machine templates: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachineTemplate, updateErr.Error(), nil); condErr != nil {
			return utilerrors.NewAggregate([]error{updateErr, condErr})
		}
	}

	return nil
}

// ensureCAPIInfraMachineTemplate ensures the CAPI InfraMachineTemplate is created or updated from the MAPI MachineSet.
func (r *MachineSetSyncReconciler) ensureCAPIInfraMachineTemplate(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, newCAPIMachineSet *clusterv1.MachineSet, newCAPIInfraMachineTemplate client.Object, clusterOwnerRefence metav1.OwnerReference) error {
	_, infraMachineTemplate, err := r.fetchCAPIInfraResources(ctx, newCAPIMachineSet)
	if err != nil && !apierrors.IsNotFound(err) {
		fetchErr := fmt.Errorf("failed to fetch CAPI infra resources: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, fetchErr.Error(), nil); condErr != nil {
			return utilerrors.NewAggregate([]error{fetchErr, condErr})
		}

		return fetchErr
	}

	if !util.IsNilObject(infraMachineTemplate) {
		newCAPIInfraMachineTemplate.SetResourceVersion(util.GetResourceVersion(infraMachineTemplate))
		newCAPIInfraMachineTemplate.SetFinalizers(infraMachineTemplate.GetFinalizers())
	}

	newCAPIInfraMachineTemplate.SetNamespace(r.CAPINamespace)
	newCAPIInfraMachineTemplate.SetOwnerReferences([]metav1.OwnerReference{clusterOwnerRefence})

	if mapiMachineSet.Status.AuthoritativeAPI == machinev1beta1.MachineAuthorityMachineAPI {
		// Set the paused annotation on the new CAPI InfraMachineTemplate, if the authoritativeAPI is Machine API,
		// as we want the new CAPI InfraMachineTemplate to be initially paused when the MAPI MachineSet is the authoritative one.
		// For the other case instead, when the new CAPI InfraMachineTemplate that is being created, is also expected to be the authority
		// (i.e. in cases where the MAPI MachineSet is created as .spec.authoritativeAPI: ClusterAPI), we do not want to create it paused.
		annotations.AddAnnotations(newCAPIInfraMachineTemplate, map[string]string{clusterv1.PausedAnnotation: ""})
	}

	newCAPIInfraMachineTemplate.SetLabels(map[string]string{controllers.MachineSetOpenshiftLabelKey: mapiMachineSet.Name})

	if err := r.createOrUpdateCAPIInfraMachineTemplate(ctx, mapiMachineSet, infraMachineTemplate, newCAPIInfraMachineTemplate); err != nil {
		return fmt.Errorf("unable to ensure Cluster API infrastructure machine template: %w", err)
	}

	return nil
}

// reconcileCAPIMachineSetToMAPIMachineSet reconciles a CAPI MachineSet to a
// MAPI MachineSet.
//
//nolint:funlen
func (r *MachineSetSyncReconciler) reconcileCAPIMachineSetToMAPIMachineSet(ctx context.Context, capiMachineSet *clusterv1.MachineSet, mapiMachineSet *machinev1beta1.MachineSet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if shouldRequeue, err := r.reconcileCAPItoMAPIMachineSetDeletion(ctx, mapiMachineSet, capiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile Machine API to Cluster API machine set deletion: %w", err)
	} else if shouldRequeue {
		return ctrl.Result{}, nil
	}

	if shouldRequeue, err := r.ensureSyncFinalizer(ctx, mapiMachineSet, capiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure sync finalizer: %w", err)
	} else if shouldRequeue {
		return ctrl.Result{}, nil
	}

	if err := r.validateCAPIMachineSetOwnerReferences(capiMachineSet); err != nil {
		logger.Error(err, "unable to convert Cluster API machine set to Machine API. Cluster API machine set has non-convertible owner references")

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToConvertCAPIMachineSetToMAPI, err.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{err, condErr})
		}

		return ctrl.Result{}, nil
	}

	infraCluster, infraMachineTemplate, err := r.fetchCAPIInfraResources(ctx, capiMachineSet)
	if err != nil {
		fetchErr := fmt.Errorf("failed to fetch CAPI infra resources: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToGetCAPIInfraResources, fetchErr.Error(), nil); condErr != nil {
			return ctrl.Result{}, utilerrors.NewAggregate([]error{fetchErr, condErr})
		}

		return ctrl.Result{}, fetchErr
	}

	newMapiMachineSet, warns, err := r.convertCAPIToMAPIMachineSet(capiMachineSet, infraMachineTemplate, infraCluster)
	if err != nil {
		conversionErr := fmt.Errorf("failed to convert CAPI machine set to MAPI machine set: %w", err)

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

	restoreMAPIFields(mapiMachineSet, newMapiMachineSet)

	if err := r.createOrUpdateMAPIMachineSet(ctx, mapiMachineSet, newMapiMachineSet); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure MAPI machine set: %w", err)
	}

	return ctrl.Result{}, r.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionTrue,
		controllers.ReasonResourceSynchronized, messageSuccessfullySynchronizedCAPItoMAPI, &capiMachineSet.Generation)
}

// fetchCAPIClusterOwnerReference fetches the OpenShift cluster object instance and returns owner reference to it.
// The OwnerReference has Controller set to false and BlockOwnerDeletion set to true.
func (r *MachineSetSyncReconciler) fetchCAPIClusterOwnerReference(ctx context.Context) (metav1.OwnerReference, error) {
	cluster := &clusterv1.Cluster{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: r.Infra.Status.InfrastructureName}, cluster); err != nil {
		return metav1.OwnerReference{}, fmt.Errorf("failed to get CAPI cluster: %w", err)
	}

	return metav1.OwnerReference{
		APIVersion:         cluster.APIVersion,
		Kind:               cluster.Kind,
		Name:               cluster.Name,
		UID:                cluster.UID,
		Controller:         ptr.To(false),
		BlockOwnerDeletion: ptr.To(true),
	}, nil
}

// validateMAPIMachineSetOwnerReferences validates the owner references are allowed for conversion.
func (r *MachineSetSyncReconciler) validateMAPIMachineSetOwnerReferences(mapiMachineSet *machinev1beta1.MachineSet) error {
	if len(mapiMachineSet.OwnerReferences) > 0 {
		return field.Invalid(field.NewPath("metadata", "ownerReferences"), mapiMachineSet.OwnerReferences, errMachineAPIMachineSetOwnerReferenceConversionUnsupported.Error())
	}

	return nil
}

// validateCAPIMachineSetOwnerReferences validates the owner references are allowed for conversion.
func (r *MachineSetSyncReconciler) validateCAPIMachineSetOwnerReferences(capiMachineSet *clusterv1.MachineSet) error {
	if len(capiMachineSet.OwnerReferences) > 1 {
		return field.TooMany(field.NewPath("metadata", "ownerReferences"), len(capiMachineSet.OwnerReferences), 1)
	} else if len(capiMachineSet.OwnerReferences) == 1 {
		// Only reference to the Cluster is allowed.
		ownerRef := capiMachineSet.OwnerReferences[0]
		if ownerRef.Kind != clusterv1.ClusterKind || ownerRef.APIVersion != clusterv1.GroupVersion.String() {
			return field.Invalid(field.NewPath("metadata", "ownerReferences"), capiMachineSet.OwnerReferences, errUnsuportedOwnerKindForConversion.Error())
		}
	}

	return nil
}

// convertCAPIToMAPIMachineSet converts a CAPI MachineSet to a MAPI MachineSet, selecting the correct converter based on the platform.
func (r *MachineSetSyncReconciler) convertCAPIToMAPIMachineSet(capiMachineSet *clusterv1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) (*machinev1beta1.MachineSet, []string, error) {
	switch r.Platform {
	case configv1.AWSPlatformType:
		machineTemplate, ok := infraMachineTemplate.(*awsv1.AWSMachineTemplate)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected AWSMachineTemplate, got %T", errUnexpectedInfraMachineTemplateType, infraMachineTemplate)
		}

		cluster, ok := infraCluster.(*awsv1.AWSCluster)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected AWSCluster, got %T", errUnexpectedInfraClusterType, infraCluster)
		}

		return capi2mapi.FromMachineSetAndAWSMachineTemplateAndAWSCluster( //nolint: wrapcheck
			capiMachineSet, machineTemplate, cluster,
		).ToMachineSet()
	case configv1.OpenStackPlatformType:
		machineTemplate, ok := infraMachineTemplate.(*openstackv1.OpenStackMachineTemplate)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected OpenStackMachineTemplate, got %T", errUnexpectedInfraMachineTemplateType, infraMachineTemplate)
		}

		cluster, ok := infraCluster.(*openstackv1.OpenStackCluster)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected OpenStackCluster, got %T", errUnexpectedInfraClusterType, infraCluster)
		}

		return capi2mapi.FromMachineSetAndOpenStackMachineTemplateAndOpenStackCluster( //nolint: wrapcheck
			capiMachineSet, machineTemplate, cluster,
		).ToMachineSet()
	case configv1.PowerVSPlatformType:
		machineTemplate, ok := infraMachineTemplate.(*ibmpowervsv1.IBMPowerVSMachineTemplate)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected IBMPowerVSMachineTemplate, got %T", errUnexpectedInfraMachineTemplateType, infraMachineTemplate)
		}

		cluster, ok := infraCluster.(*ibmpowervsv1.IBMPowerVSCluster)
		if !ok {
			return nil, nil, fmt.Errorf("%w, expected IBMPowerVSCluster, got %T", errUnexpectedInfraClusterType, infraCluster)
		}

		return capi2mapi.FromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster( //nolint: wrapcheck
			capiMachineSet, machineTemplate, cluster,
		).ToMachineSet()
	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, r.Platform)
	}
}

// convertMAPIToCAPIMachineSet converts a MAPI MachineSet to a CAPI MachineSet, selecting the correct converter based on the platform.
func (r *MachineSetSyncReconciler) convertMAPIToCAPIMachineSet(mapiMachineSet *machinev1beta1.MachineSet) (*clusterv1.MachineSet, client.Object, []string, error) {
	switch r.Platform {
	case configv1.AWSPlatformType:
		return mapi2capi.FromAWSMachineSetAndInfra(mapiMachineSet, r.Infra).ToMachineSetAndMachineTemplate() //nolint:wrapcheck
	case configv1.OpenStackPlatformType:
		return mapi2capi.FromOpenStackMachineSetAndInfra(mapiMachineSet, r.Infra).ToMachineSetAndMachineTemplate() //nolint:wrapcheck
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
	return synccommon.ApplySyncStatus[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](
		ctx, r.Client, controllerName,
		machinev1applyconfigs.MachineSet, mapiMachineSet,
		status, reason, message, generation)
}

// createOrUpdateCAPIInfraMachineTemplate creates a CAPI infra machine template from a MAPI machine set, or updates if it exists and it is out of date.
func (r *MachineSetSyncReconciler) createOrUpdateCAPIInfraMachineTemplate(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, infraMachineTemplate client.Object, newCAPIInfraMachineTemplate client.Object) error {
	logger := log.FromContext(ctx)

	if infraMachineTemplate != nil {
		capiInfraMachineTemplatesDiff, err := compareCAPIInfraMachineTemplates(r.Platform, infraMachineTemplate, newCAPIInfraMachineTemplate)
		if err != nil {
			logger.Error(err, "Failed to check CAPI infra machine template diff")
			updateErr := fmt.Errorf("failed to check CAPI infra machine template diff: %w", err)

			if condErr := r.applySynchronizedConditionWithPatch(
				ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachineTemplate, updateErr.Error(), nil); condErr != nil {
				return utilerrors.NewAggregate([]error{updateErr, condErr})
			}

			return updateErr
		}

		if len(capiInfraMachineTemplatesDiff) == 0 {
			logger.Info("No changes detected for CAPI infra machine template")
			return nil
		}

		logger.Info("Changes detected for CAPI infra machine template. Updating it", "diff", fmt.Sprintf("%+v", capiInfraMachineTemplatesDiff))
	}

	if err := r.Patch(ctx, newCAPIInfraMachineTemplate, client.Apply, &client.PatchOptions{
		FieldManager: controllerName,
		Force:        ptr.To(true),
	}); err != nil {
		logger.Error(err, "Failed to apply CAPI infrastructure machine template")

		updateErr := fmt.Errorf("failed to apply CAPI infrastructure machine template: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachineTemplate, updateErr.Error(), nil); condErr != nil {
			return utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return updateErr
	}

	logger.Info("Successfully created Cluster API infrastructure machine template", "name", newCAPIInfraMachineTemplate.GetName())

	return nil
}

// createOrUpdateCAPIMachineSet creates a CAPI machine set from a MAPI one, or updates if it exists and it is out of date.
func (r *MachineSetSyncReconciler) createOrUpdateCAPIMachineSet(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *clusterv1.MachineSet, newCAPIMachineSet *clusterv1.MachineSet) error {
	logger := log.FromContext(ctx)

	if capiMachineSet == nil {
		if err := r.Create(ctx, newCAPIMachineSet); err != nil {
			logger.Error(err, "Failed to create CAPI machine set")

			createErr := fmt.Errorf("failed to create CAPI machine set: %w", err)
			if condErr := r.applySynchronizedConditionWithPatch(
				ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToCreateCAPIMachineSet, createErr.Error(), nil); condErr != nil {
				return utilerrors.NewAggregate([]error{createErr, condErr})
			}

			return createErr
		}

		logger.Info("Successfully created CAPI machine set", "name", newCAPIMachineSet.Name, "infraMachineTemplate", newCAPIMachineSet.Spec.Template.Spec.InfrastructureRef.Name)

		return nil
	}

	capiMachineSetsDiff := compareCAPIMachineSets(capiMachineSet, newCAPIMachineSet)

	updated := false

	if hasSpecOrMetadataOrProviderSpecChanges(capiMachineSetsDiff) {
		logger.Info("Changes detected for CAPI machine set. Updating it", "diff", fmt.Sprintf("%+v", capiMachineSetsDiff))

		if err := r.Update(ctx, newCAPIMachineSet.DeepCopy()); err != nil {
			logger.Error(err, "Failed to update CAPI machine set")

			updateErr := fmt.Errorf("failed to update CAPI machine set: %w", err)

			if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachineSet, updateErr.Error(), nil); condErr != nil {
				return utilerrors.NewAggregate([]error{updateErr, condErr})
			}

			return updateErr
		}

		updated = true
	}

	if hasStatusChanges(capiMachineSetsDiff) {
		logger.Info("Changes detected for CAPI machine set status. Updating it", "diff", fmt.Sprintf("%+v", capiMachineSetsDiff))

		patchBase := client.MergeFrom(capiMachineSet.DeepCopy())

		// Apply status changes from the new CAPI machine set.
		capiMachineSet.Status.Replicas = newCAPIMachineSet.Status.Replicas
		capiMachineSet.Status.ReadyReplicas = newCAPIMachineSet.Status.ReadyReplicas
		capiMachineSet.Status.AvailableReplicas = newCAPIMachineSet.Status.AvailableReplicas
		capiMachineSet.Status.FullyLabeledReplicas = newCAPIMachineSet.Status.FullyLabeledReplicas
		capiMachineSet.Status.FailureReason = newCAPIMachineSet.Status.FailureReason
		capiMachineSet.Status.FailureMessage = newCAPIMachineSet.Status.FailureMessage

		// TODO(damdo): Restore the Conditions.
		// Conditions: newCAPIMachineSet.Status.Conditions, // These will need to be appended?

		if isPatchRequired, err := util.IsPatchRequired(capiMachineSet, patchBase); err != nil {
			return fmt.Errorf("failed to check if patch is required: %w", err)
		} else if isPatchRequired {
			if err := r.Status().Patch(ctx, capiMachineSet, patchBase); err != nil {
				logger.Error(err, "Failed to update CAPI machine set status")

				updateErr := fmt.Errorf("failed to update CAPI machine set status: %w", err)

				if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachineSet, updateErr.Error(), nil); condErr != nil {
					return utilerrors.NewAggregate([]error{updateErr, condErr})
				}

				return updateErr
			}

			updated = true
		}
	}

	if updated {
		logger.Info("Successfully updated CAPI machine set")
	} else {
		logger.Info("No changes detected for CAPI machine set")
	}

	return nil
}

// createOrUpdateMAPIMachineSet creates a MAPI machine set from a CAPI one, or updates if it exists and it is out of date.
func (r *MachineSetSyncReconciler) createOrUpdateMAPIMachineSet(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, newMAPIMachineSet *machinev1beta1.MachineSet) error {
	logger := log.FromContext(ctx)

	mapiMachineSetsDiff, err := compareMAPIMachineSets(mapiMachineSet, newMAPIMachineSet)
	if err != nil {
		return fmt.Errorf("unable to compare MAPI machine sets: %w", err)
	}

	updated := false

	if hasSpecOrMetadataOrProviderSpecChanges(mapiMachineSetsDiff) {
		logger.Info("Changes detected for MAPI machine set. Updating it", "diff", fmt.Sprintf("%+v", mapiMachineSetsDiff))

		if err := r.Update(ctx, newMAPIMachineSet.DeepCopy()); err != nil {
			logger.Error(err, "Failed to update MAPI machine set")

			updateErr := fmt.Errorf("failed to update MAPI machine set: %w", err)

			if condErr := r.applySynchronizedConditionWithPatch(
				ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateMAPIMachineSet, updateErr.Error(), nil); condErr != nil {
				return utilerrors.NewAggregate([]error{updateErr, condErr})
			}

			return updateErr
		}

		updated = true
	}

	if hasStatusChanges(mapiMachineSetsDiff) {
		logger.Info("Changes detected for MAPI machine set status. Updating it", "diff", fmt.Sprintf("%+v", mapiMachineSetsDiff))

		patchBase := client.MergeFrom(mapiMachineSet.DeepCopy())

		// Apply status changes from the new MAPI machine set.
		mapiMachineSet.Status.Replicas = newMAPIMachineSet.Status.Replicas
		mapiMachineSet.Status.ReadyReplicas = newMAPIMachineSet.Status.ReadyReplicas
		mapiMachineSet.Status.AvailableReplicas = newMAPIMachineSet.Status.AvailableReplicas
		mapiMachineSet.Status.FullyLabeledReplicas = newMAPIMachineSet.Status.FullyLabeledReplicas
		mapiMachineSet.Status.ErrorReason = newMAPIMachineSet.Status.ErrorReason
		mapiMachineSet.Status.ErrorMessage = newMAPIMachineSet.Status.ErrorMessage

		// TODO(damdo): Restore the Conditions.
		// Conditions: newMapiMachineSet.Status.Conditions, // These will need to be appended?

		if isPatchRequired, err := util.IsPatchRequired(mapiMachineSet, patchBase); err != nil {
			return fmt.Errorf("failed to check if patch is required: %w", err)
		} else if isPatchRequired {
			if err := r.Status().Patch(ctx, mapiMachineSet, patchBase); err != nil {
				logger.Error(err, "Failed to update MAPI machine set status")

				updateErr := fmt.Errorf("failed to update MAPI machine set status: %w", err)

				if condErr := r.applySynchronizedConditionWithPatch(
					ctx, mapiMachineSet, corev1.ConditionFalse, reasonFailedToUpdateMAPIMachineSet, updateErr.Error(), nil); condErr != nil {
					return utilerrors.NewAggregate([]error{updateErr, condErr})
				}

				return updateErr
			}

			updated = true
		}
	}

	if updated {
		logger.Info("Successfully updated MAPI machine set")
	} else {
		logger.Info("No changes detected for MAPI machine set")
	}

	return nil
}

// ensureSyncFinalizer ensures the sync finalizer is present across mapi and capi machine sets.
// It attempts to set both in one call, aggregating errors.
func (r *MachineSetSyncReconciler) ensureSyncFinalizer(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	var shouldRequeue bool

	var errors []error

	if mapiMachineSet != nil {
		if mapiMachineSet.DeletionTimestamp.IsZero() {
			didSet, err := util.EnsureFinalizer(ctx, r.Client, mapiMachineSet, machinesync.SyncFinalizer)
			if err != nil {
				errors = append(errors, err)
			} else if didSet {
				shouldRequeue = true
			}
		}
	}

	if capiMachineSet != nil {
		if capiMachineSet.DeletionTimestamp.IsZero() {
			didSet, err := util.EnsureFinalizer(ctx, r.Client, capiMachineSet, machinesync.SyncFinalizer)
			if err != nil {
				errors = append(errors, err)
			} else if didSet {
				shouldRequeue = true
			}
		}
	}

	return shouldRequeue, utilerrors.NewAggregate(errors)
}

func (r *MachineSetSyncReconciler) reconcileMAPItoCAPIMachineSetDeletion(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	if mapiMachineSet.DeletionTimestamp.IsZero() {
		return r.reconcileMAPItoCAPIMachineSetDeletionMAPINotDeleting(ctx, mapiMachineSet, capiMachineSet)
	}

	if capiMachineSet == nil {
		return r.reconcileMAPItoCAPIMachineSetDeletionNoCAPI(ctx, mapiMachineSet)
	}

	return r.reconcileMAPItoCAPIMachineSetDeletionNormal(ctx, mapiMachineSet, capiMachineSet)
}

// reconcileMAPItoCAPIMachineSetDeletionMAPINotDeleting handles deletion when the MAPI machine set is not being deleted.
// It checks if the CAPI machine set is being deleted, and if so, deletes the MAPI machine set.
func (r *MachineSetSyncReconciler) reconcileMAPItoCAPIMachineSetDeletionMAPINotDeleting(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	logger := log.FromContext(ctx)

	if capiMachineSet == nil || capiMachineSet.DeletionTimestamp.IsZero() {
		// Neither MAPI authoritative machine set nor its CAPI non-authoritative mirror
		// are being deleted, nothing to reconcile for deletion.
		return false, nil
	}

	// The MAPI authoritative machine set is not being deleted, but the CAPI non-authoritative one is.
	// Issue a deletion also to the MAPI authoritative machine set.
	logger.Info("The non-authoritative Cluster API machine set is being deleted, issuing deletion to the corresponding Machine API machine set")

	if err := r.Client.Delete(ctx, mapiMachineSet); err != nil {
		return false, fmt.Errorf("failed to delete Machine API machine set: %w", err)
	}

	// Return true to force a requeue, to allow the deletion propagation.
	return true, nil
}

// reconcileMAPItoCAPIMachineSetDeletionNoCAPI handles deletion when the CAPI machine set does not exist.
// It cleans up the MAPI machine set resources and finalizers.
func (r *MachineSetSyncReconciler) reconcileMAPItoCAPIMachineSetDeletionNoCAPI(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet) (bool, error) {
	logger := log.FromContext(ctx)

	logger.Info("Cluster API machine set does not exist, removing corresponding Machine API machine set sync finalizer")

	// Clean up any CAPI infrastructure machine templates that may still exist
	shouldRequeue, err := r.deleteOutdatedCAPIInfraMachineTemplates(ctx, mapiMachineSet, "")
	if err != nil {
		logger.Error(err, "Failed to clean up Cluster API infrastructure machine templates during deletion")
		return true, fmt.Errorf("unable to clean up Cluster API infrastructure machine templates during deletion: %w", err)
	} else if shouldRequeue {
		logger.Info("Waiting for Cluster API infrastructure machine templates to be deleted")
		return true, nil
	}

	// We don't have a capi machine set to clean up. Just let the MAPI operators
	// function as normal, and remove the MAPI sync finalizer.
	if _, err := util.RemoveFinalizer(ctx, r.Client, mapiMachineSet, machinesync.SyncFinalizer); err != nil {
		return true, fmt.Errorf("failed to remove finalizer from Machine API machine set: %w", err)
	}

	return true, nil
}

// reconcileMAPItoCAPIMachineSetDeletionNormal handles deletion when both MAPI and CAPI machine sets exist,
// and the MAPI machine set is being deleted.
func (r *MachineSetSyncReconciler) reconcileMAPItoCAPIMachineSetDeletionNormal(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	logger := log.FromContext(ctx)

	if capiMachineSet.DeletionTimestamp.IsZero() {
		logger.Info("Machine API machine set is being deleted, issuing deletion to corresponding Cluster API machine set")

		if err := r.Client.Delete(ctx, capiMachineSet); err != nil {
			return true, fmt.Errorf("failed delete Cluster API machine set: %w", err)
		}
	}

	// Clean up CAPI infrastructure machine templates that have the machine set label
	shouldRequeue, err := r.deleteOutdatedCAPIInfraMachineTemplates(ctx, mapiMachineSet, "")
	if err != nil {
		logger.Error(err, "Failed to clean up Cluster API infrastructure machine templates during deletion")
		return true, fmt.Errorf("failed to clean up Cluster API infrastructure machine templates during deletion: %w", err)
	} else if shouldRequeue {
		logger.Info("Waiting for Cluster API infrastructure machine templates to be deleted")
		return true, nil
	}

	// Because the CAPI machineset is paused we must remove the CAPI finalizer manually.
	if _, err := util.RemoveFinalizer(ctx, r.Client, capiMachineSet, clusterv1.MachineSetFinalizer); err != nil {
		return true, fmt.Errorf("failed to remove finalizer from Cluster API machine set: %w", err)
	}

	// We'll re-reconcile and remove the MAPI machineset once the CAPI machine set is not present
	if _, err := util.RemoveFinalizer(ctx, r.Client, capiMachineSet, machinesync.SyncFinalizer); err != nil {
		return true, fmt.Errorf("failed to remove finalizer from Cluster API machine set: %w", err)
	}

	return true, nil
}

func (r *MachineSetSyncReconciler) reconcileCAPItoMAPIMachineSetDeletion(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	if capiMachineSet.DeletionTimestamp.IsZero() {
		return r.reconcileCAPItoMAPIMachineSetDeletionCAPINotDeleting(ctx, mapiMachineSet, capiMachineSet)
	}

	if mapiMachineSet == nil {
		return r.reconcileCAPItoMAPIMachineSetDeletionNoMAPI(ctx, capiMachineSet)
	}

	return r.reconcileCAPItoMAPIMachineSetDeletionNormal(ctx, mapiMachineSet, capiMachineSet)
}

// reconcileCAPItoMAPIMachineSetDeletionCAPINotDeleting handles deletion when the CAPI machine set is not being deleted.
// It checks if the MAPI machine set is being deleted, and if so, removes the sync finalizers.
func (r *MachineSetSyncReconciler) reconcileCAPItoMAPIMachineSetDeletionCAPINotDeleting(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	logger := log.FromContext(ctx)

	if mapiMachineSet == nil || mapiMachineSet.DeletionTimestamp.IsZero() {
		// Neither CAPI authoritative machine set nor its MAPI non-authoritative mirror are being deleted, nothing to reconcile for deletion.
		return false, nil
	}
	// The CAPI authoritative machine set is not being deleted, but the MAPI non-authoritative one is remove our sync finalizer
	// on the cluster api resources, and allow deletion of the MAPI machineset
	logger.Info("The non-authoritative Machine API machine set is being deleted, removing our sync finalizer from the corresponding Cluster API machine set")

	if _, err := util.RemoveFinalizer(ctx, r.Client, capiMachineSet, machinesync.SyncFinalizer); err != nil {
		return true, fmt.Errorf("failed to remove finalizer from Cluster API machine set: %w", err)
	}

	if _, err := util.RemoveFinalizer(ctx, r.Client, mapiMachineSet, machinesync.SyncFinalizer); err != nil {
		return true, fmt.Errorf("failed to remove finalizer from Machine API machine set: %w", err)
	}

	return true, nil
}

// reconcileCAPItoMAPIMachineSetDeletionNoMAPI handles deletion when the MAPI machine set does not exist.
// It cleans up the CAPI machine set resources and finalizers.
func (r *MachineSetSyncReconciler) reconcileCAPItoMAPIMachineSetDeletionNoMAPI(ctx context.Context, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	logger := log.FromContext(ctx)

	logger.Info("Machine API machine set does not exist, removing corresponding Cluster API machine set sync finalizer")
	// We don't have  a mapi machine set to clean up. Just let the CAPI operators function as normal, and remove the CAPI sync finalizer.
	if _, err := util.RemoveFinalizer(ctx, r.Client, capiMachineSet, machinesync.SyncFinalizer); err != nil {
		return true, fmt.Errorf("failed to remove finalizer from Cluster API machine set: %w", err)
	}

	return true, nil
}

// reconcileCAPItoMAPIMachineSetDeletionNormal handles deletion when both CAPI and MAPI machine sets exist,
// and the CAPI machine set is being deleted.
func (r *MachineSetSyncReconciler) reconcileCAPItoMAPIMachineSetDeletionNormal(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *clusterv1.MachineSet) (bool, error) {
	logger := log.FromContext(ctx)

	if mapiMachineSet.DeletionTimestamp.IsZero() {
		logger.Info("Cluster API machine set is being deleted, issuing deletion to corresponding Machine API machine set")

		if err := r.Client.Delete(ctx, mapiMachineSet); err != nil {
			return true, fmt.Errorf("failed delete Machine API machine set: %w", err)
		}

		return true, nil
	}

	if slices.Contains(capiMachineSet.Finalizers, clusterv1.MachineSetFinalizer) {
		logger.Info("Waiting on Cluster API machine set specific finalizer to be removed")
		return true, nil
	}

	// Delete infraMachineTemplates that have the MAPI machineSet label
	shouldRequeue, err := r.deleteOutdatedCAPIInfraMachineTemplates(ctx, mapiMachineSet, "")
	if err != nil {
		logger.Error(err, "Failed to clean up Cluster API infrastructure machine templates during deletion")
		return true, fmt.Errorf("unable to clean up Cluster API infrastructure machine templates during deletion: %w", err)
	} else if shouldRequeue {
		logger.Info("Waiting for Cluster API infrastructure machine templates to be deleted")
		return true, nil
	}

	// Once the CAPI machine set finalizer is gone, we can remove our sync finalizer
	if _, err := util.RemoveFinalizer(ctx, r.Client, capiMachineSet, machinesync.SyncFinalizer); err != nil {
		return true, fmt.Errorf("failed to remove finalizer from Cluster API machine set: %w", err)
	}

	// Remove the MAPI finalizer last, once the MAPI machine set goes away we won't re-reconcile
	// so can end up leaving the CAPI machine set behind if we remove it first.
	if _, err := util.RemoveFinalizer(ctx, r.Client, mapiMachineSet, machinesync.SyncFinalizer); err != nil {
		return true, fmt.Errorf("failed to remove finalizer from Cluster API machine set: %w", err)
	}

	return true, nil
}

// initInfraMachineTemplateListAndInfraClusterListFromProvider returns the correct InfraMachineTemplateList and InfraClusterList implementation
// for a given provider.
func initInfraMachineTemplateListAndInfraClusterListFromProvider(platform configv1.PlatformType) (client.ObjectList, client.ObjectList, error) {
	switch platform {
	case configv1.AWSPlatformType:
		return &awsv1.AWSMachineTemplateList{}, &awsv1.AWSClusterList{}, nil
	case configv1.OpenStackPlatformType:
		return &openstackv1.OpenStackMachineTemplateList{}, &openstackv1.OpenStackClusterList{}, nil
	case configv1.PowerVSPlatformType:
		return &ibmpowervsv1.IBMPowerVSMachineTemplateList{}, &ibmpowervsv1.IBMPowerVSClusterList{}, nil
	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

// compareCAPIInfraMachineTemplates compares CAPI infra machine templates a and b, and returns a list of differences, or none if there are none.
//
//nolint:funlen
func compareCAPIInfraMachineTemplates(platform configv1.PlatformType, infraMachineTemplate1, infraMachineTemplate2 client.Object) (map[string]any, error) {
	switch platform {
	case configv1.AWSPlatformType:
		typedInfraMachineTemplate1, ok := infraMachineTemplate1.(*awsv1.AWSMachineTemplate)
		if !ok {
			return nil, errAssertingCAPIAWSMachineTemplate
		}

		typedinfraMachineTemplate2, ok := infraMachineTemplate2.(*awsv1.AWSMachineTemplate)
		if !ok {
			return nil, errAssertingCAPIAWSMachineTemplate
		}

		diff := make(map[string]any)

		if diffSpec := deep.Equal(typedInfraMachineTemplate1.Spec, typedinfraMachineTemplate2.Spec); len(diffSpec) > 0 {
			diff[".spec"] = diffSpec
		}

		if diffObjectMeta := util.ObjectMetaEqual(typedInfraMachineTemplate1.ObjectMeta, typedinfraMachineTemplate2.ObjectMeta); len(diffObjectMeta) > 0 {
			diff[".metadata"] = diffObjectMeta
		}

		// TODO: Evaluate if we want to add status comparison if needed in the future (e.g. for scale from zero capacity).

		return diff, nil
	case configv1.OpenStackPlatformType:
		typedInfraMachineTemplate1, ok := infraMachineTemplate1.(*openstackv1.OpenStackMachineTemplate)
		if !ok {
			return nil, errAssertingCAPIIBMPowerVSMachineTemplate
		}

		typedinfraMachineTemplate2, ok := infraMachineTemplate2.(*openstackv1.OpenStackMachineTemplate)
		if !ok {
			return nil, errAssertingCAPIOpenStackMachineTemplate
		}

		diff := make(map[string]any)

		if diffSpec := deep.Equal(typedInfraMachineTemplate1.Spec, typedinfraMachineTemplate2.Spec); len(diffSpec) > 0 {
			diff[".spec"] = diffSpec
		}

		if diffObjectMeta := deep.Equal(typedInfraMachineTemplate1.ObjectMeta, typedinfraMachineTemplate2.ObjectMeta); len(diffObjectMeta) > 0 {
			diff[".metadata"] = diffObjectMeta
		}

		return diff, nil
	case configv1.PowerVSPlatformType:
		typedInfraMachineTemplate1, ok := infraMachineTemplate1.(*ibmpowervsv1.IBMPowerVSMachineTemplate)
		if !ok {
			return nil, errAssertingCAPIIBMPowerVSMachineTemplate
		}

		typedinfraMachineTemplate2, ok := infraMachineTemplate2.(*ibmpowervsv1.IBMPowerVSMachineTemplate)
		if !ok {
			return nil, errAssertingCAPIIBMPowerVSMachineTemplate
		}

		diff := make(map[string]any)

		if diffSpec := deep.Equal(typedInfraMachineTemplate1.Spec, typedinfraMachineTemplate2.Spec); len(diffSpec) > 0 {
			diff[".spec"] = diffSpec
		}

		if diffObjectMeta := deep.Equal(typedInfraMachineTemplate1.ObjectMeta, typedinfraMachineTemplate2.ObjectMeta); len(diffObjectMeta) > 0 {
			diff[".metadata"] = diffObjectMeta
		}

		// TODO: Evaluate if we want to add status comparison if needed in the future (e.g. for scale from zero capacity).

		return diff, nil
	default:
		return nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

// compareCAPIMachineSets compares CAPI machineSets a and b, and returns a list of differences, or none if there are none.
func compareCAPIMachineSets(capiMachineSet1, capiMachineSet2 *clusterv1.MachineSet) map[string]any {
	diff := make(map[string]any)

	if diffSpec := deep.Equal(capiMachineSet1.Spec, capiMachineSet2.Spec); len(diffSpec) > 0 {
		diff[".spec"] = diffSpec
	}

	if diffObjectMeta := util.ObjectMetaEqual(capiMachineSet1.ObjectMeta, capiMachineSet2.ObjectMeta); len(diffObjectMeta) > 0 {
		diff[".metadata"] = diffObjectMeta
	}

	if diffStatus := util.CAPIMachineSetStatusEqual(capiMachineSet1.Status, capiMachineSet2.Status); len(diffStatus) > 0 {
		diff[".status"] = diffStatus
	}

	return diff
}

// compareMAPIMachineSets compares MAPI machineSets a and b, and returns a list of differences, or none if there are none.
func compareMAPIMachineSets(a, b *machinev1beta1.MachineSet) (map[string]any, error) {
	diff := make(map[string]any)

	ps1, err := mapi2capi.AWSProviderSpecFromRawExtension(a.Spec.Template.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("unable to parse first MAPI machine set providerSpec: %w", err)
	}

	ps2, err := mapi2capi.AWSProviderSpecFromRawExtension(b.Spec.Template.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("unable to parse second MAPI machine set providerSpec: %w", err)
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
	aCopy.Spec.Template.Spec.ProviderSpec.Value = nil

	bCopy := b.DeepCopy()
	bCopy.Spec.Template.Spec.ProviderSpec.Value = nil

	if diffSpec := deep.Equal(aCopy.Spec, bCopy.Spec); len(diffSpec) > 0 {
		diff[".spec"] = diffSpec
	}

	if diffMetadata := util.ObjectMetaEqual(aCopy.ObjectMeta, bCopy.ObjectMeta); len(diffMetadata) > 0 {
		diff[".metadata"] = diffMetadata
	}

	if diffStatus := util.MAPIMachineSetStatusEqual(a.Status, b.Status); len(diffStatus) > 0 {
		diff[".status"] = diffStatus
	}

	return diff, nil
}

// restoreCAPIFields restores the CAPI machine set fields to the new CAPI machine set.
func restoreCAPIFields(capiMachineSet, newCAPIMachineSet *clusterv1.MachineSet, capiNamespace string, authoritativeAPI machinev1beta1.MachineAuthority, clusterOwnerRefence metav1.OwnerReference) {
	// Restore the CAPI object fields if a CAPI machine set already existed.
	if capiMachineSet != nil {
		newCAPIMachineSet.SetGeneration(capiMachineSet.GetGeneration())
		newCAPIMachineSet.SetUID(capiMachineSet.GetUID())
		newCAPIMachineSet.SetCreationTimestamp(capiMachineSet.GetCreationTimestamp())
		newCAPIMachineSet.SetManagedFields(capiMachineSet.GetManagedFields())
		newCAPIMachineSet.SetResourceVersion(util.GetResourceVersion(client.Object(capiMachineSet)))
		// Restore finalizers.
		newCAPIMachineSet.SetFinalizers(capiMachineSet.GetFinalizers())
	}

	// Restore the CAPI machine set namespace and template infrastructure ref namespace.
	newCAPIMachineSet.SetNamespace(capiNamespace)
	newCAPIMachineSet.Spec.Template.Spec.InfrastructureRef.Namespace = capiNamespace

	// Restore the Cluster object owner reference.
	newCAPIMachineSet.OwnerReferences = []metav1.OwnerReference{clusterOwnerRefence}

	if authoritativeAPI == machinev1beta1.MachineAuthorityMachineAPI {
		// Set the paused annotation on the new CAPI MachineSet, if the authoritativeAPI is Machine API,
		// as we want the new CAPI MachineSet to be initially paused when the MAPI Machine is the authoritative one.
		// For the other case instead (authoritativeAPI == machinev1beta1.MachineAuthorityClusterAPI),
		// when the new CAPI MachineSet that is being created is also expected to be the authority
		// (i.e. in cases where the MAPI MachineSet is created as .spec.authoritativeAPI: ClusterAPI), we do not want to create it paused.
		annotations.AddAnnotations(newCAPIMachineSet, map[string]string{clusterv1.PausedAnnotation: ""})
	}
}

// restoreMAPIFields restores the MAPI machine set fields to the new MAPI machine set.
func restoreMAPIFields(mapiMachineSet, newMapiMachineSet *machinev1beta1.MachineSet) {
	// Restore the MAPI object metadata fields.
	newMapiMachineSet.SetGeneration(mapiMachineSet.GetGeneration())
	newMapiMachineSet.SetUID(mapiMachineSet.GetUID())
	newMapiMachineSet.SetCreationTimestamp(mapiMachineSet.GetCreationTimestamp())
	newMapiMachineSet.SetManagedFields(mapiMachineSet.GetManagedFields())
	newMapiMachineSet.SetResourceVersion(util.GetResourceVersion(client.Object(mapiMachineSet)))
	newMapiMachineSet.SetNamespace(mapiMachineSet.GetNamespace())
	// Restore the MAPI machine set template labels.
	newMapiMachineSet.Spec.Template.ObjectMeta.Labels = util.MergeMaps(mapiMachineSet.Spec.Template.ObjectMeta.Labels, newMapiMachineSet.Spec.Template.ObjectMeta.Labels)
	newMapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels = util.MergeMaps(mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels, newMapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels)
	// Restore API authoritativeness, as it gets lost in MAPI->CAPI->MAPI translation.
	newMapiMachineSet.Spec.AuthoritativeAPI = mapiMachineSet.Spec.AuthoritativeAPI
	newMapiMachineSet.Spec.Template.Spec.AuthoritativeAPI = mapiMachineSet.Spec.Template.Spec.AuthoritativeAPI
	// Restore the original MAPI selector as it is immutable.
	newMapiMachineSet.Spec.Selector = mapiMachineSet.Spec.Selector
	newMapiMachineSet.OwnerReferences = nil // No CAPI machine set owner references are converted to MAPI machine set.
	// Restore finalizers.
	newMapiMachineSet.SetFinalizers(mapiMachineSet.GetFinalizers())
}

// hasStatusChanges returns true if there are changes to the status.
func hasStatusChanges(diff map[string]any) bool {
	_, ok := diff[".status"]

	return ok
}

// hasSpecOrMetadataOrProviderSpecChanges returns true if there are changes to the spec, metadata, or providerSpec.
func hasSpecOrMetadataOrProviderSpecChanges(diff map[string]any) bool {
	_, ok1 := diff[".spec"]
	_, ok2 := diff[".metadata"]
	_, ok3 := diff[".providerSpec"]

	return ok1 || ok2 || ok3
}

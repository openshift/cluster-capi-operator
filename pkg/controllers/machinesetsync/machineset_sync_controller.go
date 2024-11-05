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
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"k8s.io/client-go/tools/record"
	awscapiv1beta1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
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
)

const (
	reasonFailedToGetCAPIInfraResources       = "FailedToGetCAPIInfraResources"
	reasonFailedToConvertCAPIMachineSetToMAPI = "FailedToConvertCAPIMachineSetToMAPI"
	reasonFailedToUpdateMAPIMachineSet        = "FailedToUpdateMAPIMachineSet"
	reasonFailedToGetCAPIMachineSet           = "FailedToGetCAPIMachineSet"
	reasonResourceSynchronized                = "ResourceSynchronized"

	messageSuccessfullySynchronized = "Successfully synchronized CAPI MachineSet to MAPI"
)

// MachineSetSyncReconciler reconciles CAPI and MAPI MachineSets.
type MachineSetSyncReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Platform      configv1.PlatformType
	CAPINamespace string
	MAPINamespace string
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineSetSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
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
func (r *MachineSetSyncReconciler) fetchMachineSets(ctx context.Context, name string) (*machinev1beta1.MachineSet, *capiv1beta1.MachineSet, error) {
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
func (r *MachineSetSyncReconciler) fetchCAPIInfraResources(ctx context.Context, capiMachineSet *capiv1beta1.MachineSet) (client.Object, client.Object, error) {
	var infraCluster, infraMachineTemplate client.Object

	clusterKey := client.ObjectKey{
		Namespace: capiMachineSet.Namespace,
		Name:      capiMachineSet.Spec.ClusterName,
	}

	templateRef := capiMachineSet.Spec.Template.Spec.InfrastructureRef
	templateKey := client.ObjectKey{
		Namespace: templateRef.Namespace,
		Name:      templateRef.Name,
	}

	switch r.Platform {
	case configv1.AWSPlatformType:
		infraCluster = &awscapiv1beta1.AWSCluster{}
		infraMachineTemplate = &awscapiv1beta1.AWSMachineTemplate{}
	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, r.Platform)
	}

	if err := r.Get(ctx, clusterKey, infraCluster); err != nil {
		return nil, nil, fmt.Errorf("failed to get CAPI infrastructure cluster: %w", err)
	}

	if err := r.Get(ctx, templateKey, infraMachineTemplate); err != nil {
		return nil, nil, fmt.Errorf("failed to get CAPI infrastructure machine template: %w", err)
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
		// We want to create a new CAPI MachineSet from the MAPI one.
		// I think we may want to call a lot of the same logic we'll need for reconciling MAPI -> CAPI,
		// as such I think we should hold off on implementing this until that logic is worked out
		// (and hopefully it's just calling some of the same helper funcs)
		// TODO: Implementation
	case authoritativeAPI == machinev1beta1.MachineAuthorityClusterAPI && capiMachineSet != nil:
		return r.reconcileCAPIMachineSetToMAPIMachineSet(ctx, capiMachineSet, mapiMachineSet)

	case authoritativeAPI == machinev1beta1.MachineAuthorityMigrating:
		logger.Info("machine set is currently being migrated")
		return ctrl.Result{}, nil

	default:
		logger.Info("unexpected value for authoritativeAPI", "AuthoritativeAPI", mapiMachineSet.Status.AuthoritativeAPI)

		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileMAPIMachineSetToCAPIMachineSet reconciles a MAPI MachineSet to a CAPI MachineSet.
func (r *MachineSetSyncReconciler) reconcileMAPIMachineSetToCAPIMachineSet(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet) (ctrl.Result, error) {
	// This function is currently a placeholder.
	return ctrl.Result{}, nil
}

// reconcileCAPIMachineSetToMAPIMachineSet reconciles a CAPI MachineSet to a
// MAPI MachineSet.
func (r *MachineSetSyncReconciler) reconcileCAPIMachineSetToMAPIMachineSet(ctx context.Context, capiMachineSet *capiv1beta1.MachineSet, mapiMachineSet *machinev1beta1.MachineSet) (ctrl.Result, error) {
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
	newMapiMachineSet.SetResourceVersion(mapiMachineSet.GetResourceVersion())

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

	default:
		return nil, nil, fmt.Errorf("%w: %s", errPlatformNotSupported, r.Platform)
	}
}

// updateSynchronizedConditionWithPatch updates the synchronized condition
// using a server side apply patch. We do this to force ownership of the
// 'Synchronized' condition and 'SynchronizedGeneration'.
func (r *MachineSetSyncReconciler) updateSynchronizedConditionWithPatch(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, status corev1.ConditionStatus, reason, message string, generation *int64) error {
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

// getInfraMachineTemplateFromProvider returns the correct InfraMachineTemplate implementation
// for a given provider.
func getInfraMachineTemplateFromProvider(platform configv1.PlatformType) (client.Object, error) {
	switch platform {
	case configv1.AWSPlatformType:
		return &awscapiv1beta1.AWSMachineTemplate{}, nil
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

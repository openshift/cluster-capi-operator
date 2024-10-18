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
	"github.com/openshift/machine-api-operator/pkg/util/conditions"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
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

const (
	capiNamespace string = "openshift-cluster-api"
	mapiNamespace string = "openshift-machine-api"
)

var (
	// errPlatformNotSupported is returned when the platform is not supported.
	errPlatformNotSupported = errors.New("error determining InfraMachineTemplate type, platform not supported")
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
		return fmt.Errorf("failed to get InfraMachineTemplate from Provider: %w", err)
	}

	// Allow the namespaces to be set externally for test purposes, when not set,
	// default to the production namespaces.
	if r.CAPINamespace == "" {
		r.CAPINamespace = capiNamespace
	}

	if r.MAPINamespace == "" {
		r.MAPINamespace = mapiNamespace
	}

	err = ctrl.NewControllerManagedBy(mgr).
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
		Complete(r)
	if err != nil {
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

	logger.V(1).Info("Reconciling MachineSet")
	defer logger.V(1).Info("Finished reconciling MachineSet")

	mapiMachineSet, capiMachineSet, err := r.fetchMachineSets(ctx, req.Name)
	if err != nil {
		return ctrl.Result{}, err
	}

	if mapiMachineSet == nil && capiMachineSet == nil {
		logger.Info("Both MAPI and CAPI MachineSets not found, nothing to do")
		return ctrl.Result{}, nil
	}

	if mapiMachineSet == nil {
		logger.Info("Only CAPI MachineSet found, nothing to do")
		return ctrl.Result{}, nil
	}

	return r.syncMachineSets(ctx, mapiMachineSet, capiMachineSet)
}

// fetchMachineSets fetches both MAPI and CAPI MachineSets.
func (r *MachineSetSyncReconciler) fetchMachineSets(ctx context.Context, name string) (*machinev1beta1.MachineSet, *capiv1beta1.MachineSet, error) {
	logger := log.FromContext(ctx)

	var mapiMachineSet *machinev1beta1.MachineSet
	var capiMachineSet *capiv1beta1.MachineSet

	// Fetch MAPI MachineSet
	mapiMachineSet = &machinev1beta1.MachineSet{}
	err := r.Get(ctx, client.ObjectKey{Namespace: r.MAPINamespace, Name: name}, mapiMachineSet)
	if apierrors.IsNotFound(err) {
		logger.Info("MAPI MachineSet not found", "name", name)
		mapiMachineSet = nil
	} else if err != nil {
		logger.Error(err, "Failed to get MAPI MachineSet", "name", name)
		return nil, nil, fmt.Errorf("failed to get MAPI MachineSet: %w", err)
	}

	// Fetch CAPI MachineSet
	capiMachineSet = &capiv1beta1.MachineSet{}
	err = r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: name}, capiMachineSet)
	if apierrors.IsNotFound(err) {
		logger.Info("CAPI MachineSet not found", "name", name)
		capiMachineSet = nil
	} else if err != nil {
		logger.Error(err, "Failed to get CAPI MachineSet", "name", name)
		return nil, nil, fmt.Errorf("failed to get CAPI MachineSet: %w", err)
	}

	return mapiMachineSet, capiMachineSet, nil
}

// syncMachineSets synchronizes MachineSets based on the authoritative API.
func (r *MachineSetSyncReconciler) syncMachineSets(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	switch mapiMachineSet.Status.AuthoritativeAPI {
	case machinev1beta1.MachineAuthorityMachineAPI:
		return r.reconcileMAPIMachineSetToCAPIMachineSet(ctx, mapiMachineSet, capiMachineSet)
	case machinev1beta1.MachineAuthorityClusterAPI:
		return r.reconcileCAPIMachineSetToMAPIMachineSet(ctx, capiMachineSet, mapiMachineSet)
	case machinev1beta1.MachineAuthorityMigrating:
		logger.Info("MachineSet is currently migrating", "MachineSet", mapiMachineSet.GetName())
		return ctrl.Result{}, nil
	default:
		logger.Info("Unexpected value for AuthoritativeAPI", "AuthoritativeAPI", mapiMachineSet.Status.AuthoritativeAPI)
		return ctrl.Result{}, nil
	}
}

// reconcileMAPIMachineSetToCAPIMachineSet reconciles a MAPI MachineSet to a CAPI MachineSet.
func (r *MachineSetSyncReconciler) reconcileMAPIMachineSetToCAPIMachineSet(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet) (ctrl.Result, error) {
	// This function is currently a placeholder.
	return ctrl.Result{}, nil
}

// reconcileCAPIMachineSetToMAPIMachineSet reconciles a CAPI MachineSet to a MAPI MachineSet.
func (r *MachineSetSyncReconciler) reconcileCAPIMachineSetToMAPIMachineSet(ctx context.Context, capiMachineSet *capiv1beta1.MachineSet, mapiMachineSet *machinev1beta1.MachineSet) (ctrl.Result, error) {
	// TODO: wrap in switch func? have an extra layer to handle platform specific implementation
	// TODO: return synchronisation failed status and true status
	// TODO: abstract out to helper func the status update

	logger := log.FromContext(ctx).WithValues("function", "reconcileCAPIMachineSetToMAPIMachineSet")

	// Get the AWSCluster
	cluster := &awscapiv1beta1.AWSCluster{}
	clusterNamespacedName := client.ObjectKey{
		Namespace: capiMachineSet.GetNamespace(),
		Name:      capiMachineSet.Spec.ClusterName,
	}

	err := r.Get(ctx, clusterNamespacedName, cluster)
	if err != nil {
		logger.Error(err, "Failed to get CAPI Cluster", "cluster", clusterNamespacedName.Name)
		r.updateSynchronisedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, "FailedToGetCluster", err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to get CAPI Cluster: %w", err)
	}

	// Get the AWSMachineTemplate
	template := &awscapiv1beta1.AWSMachineTemplate{}
	templateNamespacedName := client.ObjectKey{
		Namespace: capiMachineSet.Spec.Template.Spec.InfrastructureRef.Namespace,
		Name:      capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name,
	}

	err = r.Get(ctx, templateNamespacedName, template)
	if err != nil {
		logger.Error(err, "Failed to get AWSMachineTemplate", "name", templateNamespacedName.Name)
		return ctrl.Result{}, fmt.Errorf("failed to get AWSMachineTemplate: %w", err)
	}

	// Convert the CAPI MachineSet and AWS resources to a MAPI MachineSet
	convertedMachineSet, warns, err := capi2mapi.FromMachineSetAndAWSMachineTemplateAndAWSCluster(capiMachineSet, template, cluster).ToMachineSet()
	if err != nil {
		logger.Error(err, "Failed to convert CAPI MachineSet to MAPI MachineSet")
		return ctrl.Result{}, fmt.Errorf("failed to convert CAPI MachineSet to MAPI MachineSet: %w", err)
	}

	for _, warning := range warns {
		logger.Info("Warning during conversion", "warning", warning)
	}

	// Use a copy of the MAPI MachineSet for any changes
	updatedMapiMachineSet := mapiMachineSet.DeepCopy()

	// Update the MAPI MachineSet spec
	updateMAPIMachineSetSpecFromCAPI(ctx, updatedMapiMachineSet, capiMachineSet, convertedMachineSet.Spec.Template.Spec.ProviderSpec)

	// Check if there are any changes after updating the spec
	if !reflect.DeepEqual(updatedMapiMachineSet.Spec, mapiMachineSet.Spec) {
		logger.Info("Updating MAPI MachineSet spec")

		if err := r.Update(ctx, updatedMapiMachineSet); err != nil {
			logger.Error(err, "Failed to update MAPI MachineSet")
			return ctrl.Result{}, fmt.Errorf("failed to update MAPI MachineSet: %w", err)
		}

		logger.Info("Successfully updated MAPI MachineSet spec")
	} else {
		logger.Info("No changes detected in MAPI MachineSet spec")
	}

	// Update the MAPI MachineSet status
	updateMAPIMachineSetStatusFromCAPI(ctx, updatedMapiMachineSet, capiMachineSet)

	// Check if there are any changes after updating the status
	if !reflect.DeepEqual(updatedMapiMachineSet.Status, mapiMachineSet.Status) {
		logger.Info("Updating MAPI MachineSet status")

		if err := r.Status().Update(ctx, updatedMapiMachineSet); err != nil {
			logger.Error(err, "Failed to update MAPI MachineSet status")
			return ctrl.Result{}, fmt.Errorf("failed to update MAPI MachineSet status: %w", err)
		}

		logger.Info("Successfully updated MAPI MachineSet status")
	} else {
		logger.Info("No changes detected in MAPI MachineSet status")
	}

	return ctrl.Result{}, nil
}

func (r *MachineSetSyncReconciler) updateSynchronisedConditionWithPatch(
	ctx context.Context,
	mapiMachineSet *machinev1beta1.MachineSet,
	status corev1.ConditionStatus,
	reason, message string,
) {
	logger := log.FromContext(ctx)

	// Create a deep copy for patch calculation
	oldMachineSet := mapiMachineSet.DeepCopy()

	// Prepare the new condition
	var newCondition *machinev1beta1.Condition
	if status == corev1.ConditionTrue {
		newCondition = conditions.TrueConditionWithReason(
			consts.ConditionSynchronised,
			reason,
			message,
		)
	} else {
		newCondition = conditions.FalseCondition(
			consts.ConditionSynchronised,
			reason,
			machinev1beta1.ConditionSeverityError,
			message,
		)
	}

	// Update the condition using the conditions package
	conditions.Set(&mapiMachineSet.Status.Conditions, newCondition)

	// Create a patch
	patch := client.MergeFrom(oldMachineSet)

	// Apply the patch to the status subresource
	if err := r.Status().Patch(ctx, mapiMachineSet, patch); err != nil {
		logger.Error(err, "Failed to patch MAPI MachineSet status with Synchronised condition")
	}
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

// updateMAPIMachineSetSpecFromCAPI updates the MAPI MachineSet spec based on the CAPI MachineSet.
func updateMAPIMachineSetSpecFromCAPI(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet, newProviderSpec machinev1beta1.ProviderSpec) {
	logger := log.FromContext(ctx).WithValues("function", "updateMAPIMachineSetSpecFromCAPI")

	// Update the ProviderSpec
	mapiMachineSet.Spec.Template.Spec.ProviderSpec = newProviderSpec

	// Update labels (CAPI labels take priority)
	mapiMachineSet.Spec.Template.Labels = util.MergeMaps(mapiMachineSet.Spec.Template.Labels, capiMachineSet.Spec.Template.Labels)

	// Update Replicas
	mapiMachineSet.Spec.Replicas = capiMachineSet.Spec.Replicas

	// Update MinReadySeconds
	mapiMachineSet.Spec.MinReadySeconds = capiMachineSet.Spec.MinReadySeconds

	// Update DeletePolicy
	mapiMachineSet.Spec.DeletePolicy = capiMachineSet.Spec.DeletePolicy

	// Update Selector
	mapiMachineSet.Spec.Selector = capiMachineSet.Spec.Selector

	logger.Info("Updated MAPI MachineSet spec from CAPI MachineSet")
}

// updateMAPIMachineSetStatusFromCAPI updates the MAPI MachineSet status based on the CAPI MachineSet.
func updateMAPIMachineSetStatusFromCAPI(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, capiMachineSet *capiv1beta1.MachineSet) {
	logger := log.FromContext(ctx).WithValues("function", "updateMAPIMachineSetStatusFromCAPI")

	// Update status fields directly
	mapiMachineSet.Status.Replicas = capiMachineSet.Status.Replicas
	mapiMachineSet.Status.FullyLabeledReplicas = capiMachineSet.Status.FullyLabeledReplicas
	mapiMachineSet.Status.ReadyReplicas = capiMachineSet.Status.ReadyReplicas
	mapiMachineSet.Status.AvailableReplicas = capiMachineSet.Status.AvailableReplicas
	mapiMachineSet.Status.ObservedGeneration = capiMachineSet.Status.ObservedGeneration

	// Update ErrorReason
	if capiMachineSet.Status.FailureReason != nil {
		reason := machinev1beta1.MachineSetStatusError(*capiMachineSet.Status.FailureReason)
		mapiMachineSet.Status.ErrorReason = &reason
	} else {
		mapiMachineSet.Status.ErrorReason = nil
	}

	// Update ErrorMessage
	mapiMachineSet.Status.ErrorMessage = capiMachineSet.Status.FailureMessage

	logger.Info("Updated MAPI MachineSet status from CAPI MachineSet")
}

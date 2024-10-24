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

	// Fetch MAPI MachineSet
	mapiMachineSet := &machinev1beta1.MachineSet{}

	if err := r.Get(ctx, client.ObjectKey{Namespace: r.MAPINamespace, Name: name}, mapiMachineSet); apierrors.IsNotFound(err) {
		logger.Info("MAPI machine set not found", "name", name)

		mapiMachineSet = nil
	} else if err != nil {
		logger.Error(err, "Failed to get MAPI machine set", "name", name)
		return nil, nil, fmt.Errorf("failed to get MAPI machine set: %w", err)
	}

	// Fetch CAPI MachineSet
	capiMachineSet := &capiv1beta1.MachineSet{}

	if err := r.Get(ctx, client.ObjectKey{Namespace: r.CAPINamespace, Name: name}, capiMachineSet); apierrors.IsNotFound(err) {
		logger.Info("CAPI machine set not found", "name", name)

		capiMachineSet = nil
	} else if err != nil {
		logger.Error(err, "Failed to get CAPI machine set", "name", name)
		return nil, nil, fmt.Errorf("failed to get CAPI machine set: %w", err)
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
		logger.Info("machine set is currently migrating", "machine set", mapiMachineSet.GetName())
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

// reconcileCAPIMachineSetToMAPIMachineSet reconciles a CAPI MachineSet to a
// MAPI MachineSet.
//
// TODO: Platform specific implementation (currently this works only for AWS,
// we want a switch on platform somewhere).
// TODO: Put Gets() for Cluster + Template in helper func.
func (r *MachineSetSyncReconciler) reconcileCAPIMachineSetToMAPIMachineSet(ctx context.Context, capiMachineSet *capiv1beta1.MachineSet, mapiMachineSet *machinev1beta1.MachineSet) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("function", "reconcileCAPIMachineSetToMAPIMachineSet")

	// Get the AWSCluster
	cluster := &awscapiv1beta1.AWSCluster{}
	clusterNamespacedName := client.ObjectKey{
		Namespace: capiMachineSet.GetNamespace(),
		Name:      capiMachineSet.Spec.ClusterName,
	}

	if err := r.Get(ctx, clusterNamespacedName, cluster); err != nil {
		logger.Error(err, "Failed to get CAPI cluster", "cluster", clusterNamespacedName.Name)

		r.updateSynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse,
			"FailedToGetCluster", err.Error(),
		)

		return ctrl.Result{}, fmt.Errorf("failed to get CAPI cluster: %w", err)
	}

	// Get the AWSMachineTemplate
	template := &awscapiv1beta1.AWSMachineTemplate{}
	templateNamespacedName := client.ObjectKey{
		Namespace: capiMachineSet.Spec.Template.Spec.InfrastructureRef.Namespace,
		Name:      capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name,
	}

	if err := r.Get(ctx, templateNamespacedName, template); err != nil {
		logger.Error(err, "Failed to get AWSMachineTemplate", "name", templateNamespacedName.Name)

		r.updateSynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse,
			"FailedToGetAWSMachineTemplate", err.Error(),
		)

		return ctrl.Result{}, fmt.Errorf("failed to get AWSMachineTemplate: %w", err)
	}

	// Convert the CAPI MachineSet and AWS resources to a MAPI MachineSet
	convertedMachineSet, warns, err := capi2mapi.FromMachineSetAndAWSMachineTemplateAndAWSCluster(
		capiMachineSet, template, cluster,
	).ToMachineSet()

	if err != nil {
		logger.Error(err, "Failed to convert CAPI MachineSet to MAPI machine set")

		r.updateSynchronizedConditionWithPatch(
			ctx, mapiMachineSet, corev1.ConditionFalse,
			"FailedToConvertMachineSet", err.Error(),
		)

		return ctrl.Result{}, fmt.Errorf("failed to convert CAPI MachineSet to MAPI machine set: %w", err)
	}

	for _, warning := range warns {
		logger.Info("Warning during conversion", "warning", warning)
		r.Recorder.Event(mapiMachineSet, corev1.EventTypeWarning, "ConversionWarning", warning)
	}

	convertedMachineSet.Spec.Template.Labels = util.MergeMaps(mapiMachineSet.Spec.Template.Labels, convertedMachineSet.Spec.Template.Labels)

	// Check if there are any changes after updating the spec
	if !reflect.DeepEqual(convertedMachineSet.Spec, mapiMachineSet.Spec) {
		logger.Info("Updating MAPI machine set spec")

		if err := r.Update(ctx, convertedMachineSet); err != nil {
			logger.Error(err, "Failed to update MAPI machine set")

			r.updateSynchronizedConditionWithPatch(
				ctx, mapiMachineSet, corev1.ConditionFalse,
				"FailedToUpdateMAPI", err.Error(),
			)

			return ctrl.Result{}, fmt.Errorf("failed to update MAPI machine set: %w", err)
		}

		logger.Info("Successfully updated MAPI machine set spec")
	} else {
		logger.Info("No changes detected in MAPI machine set spec")
	}

	r.updateSynchronizedConditionWithPatch(ctx, convertedMachineSet, corev1.ConditionTrue,
		consts.ReasonResourceSynchronized, "Synchronized CAPI to MAPI", capiMachineSet.Generation)

	return ctrl.Result{}, nil
}

func (r *MachineSetSyncReconciler) updateSynchronizedConditionWithPatch(ctx context.Context, mapiMachineSet *machinev1beta1.MachineSet, status corev1.ConditionStatus, reason, message string, generation ...int64) {
	logger := log.FromContext(ctx).WithValues("function", "updateSynchronizedConditionWithPatch")
	machinesetCopy := mapiMachineSet.DeepCopy()

	var newCondition *machinev1beta1.Condition
	if status == corev1.ConditionTrue {
		newCondition = conditions.TrueConditionWithReason(
			consts.SynchronizedCondition, reason, message, //nolint:govet
		)

		// Update the synchronizedGeneration
		mapiMachineSet.Status.SynchronizedGeneration = generation[0]
	} else {
		newCondition = conditions.FalseCondition(
			consts.SynchronizedCondition, reason,
			machinev1beta1.ConditionSeverityError, message, //nolint:govet
		)
	}

	conditions.Set(&mapiMachineSet.Status.Conditions, newCondition)

	// Create a Patch object using Apply
	patch := client.MergeFrom(machinesetCopy)

	data, err := patch.Data(mapiMachineSet)
	if err != nil {
		logger.Error(err, "failed serialising patch for log")
	}
	logger.Info("contents of patch", "data", data)

	if err := r.Status().Patch(ctx, mapiMachineSet, patch); err != nil {
		logger.Error(err, "Failed to patch MAPI machine set status with synchronized condition")
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

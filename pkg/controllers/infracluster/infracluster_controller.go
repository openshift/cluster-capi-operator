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
package infracluster

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/go-logr/logr"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"k8s.io/client-go/rest"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"sigs.k8s.io/yaml"
)

const (
	// Controller conditions for the Cluster Operator resource.

	// InfraClusterControllerAvailableCondition is the condition type that indicates the InfraCluster controller is available.
	InfraClusterControllerAvailableCondition = "InfraClusterControllerAvailable"

	// InfraClusterControllerDegradedCondition is the condition type that indicates the InfraCluster controller is degraded.
	InfraClusterControllerDegradedCondition = "InfraClusterControllerDegraded"

	defaultCAPINamespace = "openshift-cluster-api"
	defaultMAPINamespace = "openshift-machine-api"
	controllerName       = "InfraClusterController"
	clusterOperatorName  = "cluster-api"
	// This is the managedByAnnotation value that this controller sets by default when it creates an InfraCluster object.
	// If the managedByAnnotation key is set, and it has this as the value, it means this controller is managing the InfraCluster.
	managedByAnnotationValueClusterCAPIOperatorInfraClusterController = "cluster-capi-operator-infracluster-controller"

	kubeSystemNamespace    = "kube-system"
	vSphereCredentialsName = "vsphere-creds" //nolint:gosec
)

var (
	errPlatformNotSupported             = errors.New("infrastructure platform is not supported")
	errCouldNotDeepCopyInfraObject      = errors.New("unable to create a deep copy of InfraCluster object")
	errUnableToListControlPlaneMachines = errors.New("unable to list Control Plane Machines")
	errUnableToFindControlPlaneMachines = errors.New("unable to find any Control Plane Machines to extract a MAPI ProviderSpec from")
)

// InfraClusterController is a controller that manages infrastructure cluster objects.
type InfraClusterController struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme        *runtime.Scheme
	Images        map[string]string
	RestCfg       *rest.Config
	Platform      configv1.PlatformType
	Infra         *configv1.Infrastructure
	MAPINamespace string
	CAPINamespace string
}

// Reconcile reconciles the cluster-api ClusterOperator object.
func (r *InfraClusterController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)

	log.Info("Reconciling InfraCluster")

	res, err := r.reconcile(ctx, log)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error during reconcile: %w", err)
	}

	if err := r.setAvailableCondition(ctx, log); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set conditions for InfraCluster controller: %w", err)
	}

	return res, nil
}

func (r *InfraClusterController) reconcile(ctx context.Context, log logr.Logger) (ctrl.Result, error) {
	infraCluster, err := r.ensureInfraCluster(ctx, log)
	if err != nil && errors.Is(err, errPlatformNotSupported) {
		log.Info("Could not find or create an InfraCluster on this platform as it is not yet supported.")
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure InfraCluster: %w", err)
	}

	// At this point, the InfraCluster exists.
	// Check if it has the managedByAnnotation.
	return r.reconcileInfraCluster(ctx, log, infraCluster)
}

// reconcileInfraCluster reconciles the InfraCluster object.
// It first determines if the infra cluster should be managed before setting the infra cluster ready.
func (r *InfraClusterController) reconcileInfraCluster(ctx context.Context, log logr.Logger, infraCluster client.Object) (ctrl.Result, error) {
	managedByAnnotationVal, foundAnnotation := infraCluster.GetAnnotations()[clusterv1beta1.ManagedByAnnotation]

	switch {
	case !foundAnnotation:
		// Could not find the managedByAnnotation on the InfraCluster object.
		// This means, by definition, that the object is directly managed by CAPI infrastructure providers.
		// No action should be taken by this controller.
		log.Info(fmt.Sprintf("InfraCluster '%s/%s' does not have the externally managed-by annotation"+
			" - skipping as this is managed directly by the CAPI infrastructure provider",
			infraCluster.GetNamespace(), infraCluster.GetName()))

		return ctrl.Result{}, nil
	case managedByAnnotationVal != managedByAnnotationValueClusterCAPIOperatorInfraClusterController:
		// At this point it is not this controller's responsibility to manage this InfraCluster object, nor it is
		// the CAPI infra providers responsbility to do so. This means this object was created outside of these two entities - thus
		// the creating entity must manage its readiness.
		log.Info(fmt.Sprintf("InfraCluster '%s/%s' is annotated with an unrecognized externally managed annotation value %q"+
			" - skipping as it is not managed by this controller",
			infraCluster.GetNamespace(), infraCluster.GetName(), managedByAnnotationVal))

		return ctrl.Result{}, nil
	}

	// At this point it is this controller's responsibility to manage this InfraCluster object.
	isReady, err := getReadiness(infraCluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to get readiness for InfraCluster: %w", err)
	}

	if isReady {
		// The Infrastructure for this CAPI Cluster is already ready - nothing to do.
		return ctrl.Result{}, nil
	}

	infraClusterPatchCopy, ok := infraCluster.DeepCopyObject().(client.Object)
	if !ok {
		return ctrl.Result{}, errCouldNotDeepCopyInfraObject
	}

	// Set Status.Ready=true to indicate that cluster's infrastructure ready.
	if err := setReadiness(infraCluster, true); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to set readiness for InfraCluster: %w", err)
	}

	if err := r.Client.Status().Patch(ctx, infraCluster, client.MergeFrom(infraClusterPatchCopy)); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to patch InfraCluster: %w", err)
	}

	log.Info(fmt.Sprintf("InfraCluster '%s/%s' successfully set to Ready", infraCluster.GetNamespace(), infraCluster.GetName()))

	return ctrl.Result{}, nil
}

// ensureInfraCluster ensures an InfraCluster object exists in the cluster.
//
//nolint:funlen
func (r *InfraClusterController) ensureInfraCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	var infraCluster client.Object
	// TODO: implement InfraCluster generation for missing platforms.
	switch r.Platform {
	case configv1.AWSPlatformType:
		var err error

		infraCluster, err = r.ensureAWSCluster(ctx, log)
		if err != nil {
			return nil, fmt.Errorf("error ensuring AWSCluster: %w", err)
		}
	case configv1.GCPPlatformType:
		var err error

		infraCluster, err = r.ensureGCPCluster(ctx, log)
		if err != nil {
			return nil, fmt.Errorf("error ensuring GCPCluster: %w", err)
		}
	case configv1.AzurePlatformType:
		if r.Infra.Status.PlatformStatus.Azure.CloudName == configv1.AzureStackCloud {
			log.Info("%s cloud environment for platform %s is not supported", "environment", configv1.AzureStackCloud, "platform", configv1.AzurePlatformType)
			return nil, errPlatformNotSupported
		}

		var err error

		infraCluster, err = r.ensureAzureCluster(ctx, log)
		if err != nil {
			return nil, fmt.Errorf("error getting InfraCluster object: %w", err)
		}
	case configv1.PowerVSPlatformType:
		var err error

		infraCluster, err = r.ensureIBMPowerVSCluster(ctx, log)
		if err != nil {
			return nil, fmt.Errorf("error getting InfraCluster object: %w", err)
		}
	case configv1.VSpherePlatformType:
		var err error

		infraCluster, err = r.ensureVSphereCluster(ctx, log)
		if err != nil {
			return nil, fmt.Errorf("error getting InfraCluster object: %w", err)
		}
	case configv1.BareMetalPlatformType:
		var err error

		infraCluster, err = r.ensureMetal3Cluster(ctx, log)
		if err != nil {
			return nil, fmt.Errorf("error getting InfraCluster object: %w", err)
		}
	case configv1.OpenStackPlatformType:
		var err error

		infraCluster, err = r.ensureOpenStackCluster(ctx, log)
		if err != nil {
			return nil, fmt.Errorf("error getting InfraCluster object: %w", err)
		}
	default:
		return nil, errPlatformNotSupported
	}

	return infraCluster, nil
}

// setAvailableCondition sets the ClusterOperator status condition to Available.
func (r *InfraClusterController) setAvailableCondition(ctx context.Context, log logr.Logger) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster operator: %w", err)
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(InfraClusterControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"InfraCluster Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(InfraClusterControllerDegradedCondition, configv1.ConditionFalse, operatorstatus.ReasonAsExpected,
			"InfraCluster Controller works as expected"),
	}

	log.V(2).Info("InfraCluster Controller is Available")

	if err := r.SyncStatus(ctx, co, conds, r.OperandVersions(), r.RelatedObjects()); err != nil {
		return fmt.Errorf("failed to sync status: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *InfraClusterController) SetupWithManager(mgr ctrl.Manager, watchedObject client.Object) error {
	// Allow the namespaces to be set externally for test purposes, when not set,
	// default to the production namespaces.
	if r.CAPINamespace == "" {
		r.CAPINamespace = defaultCAPINamespace
	}

	if r.MAPINamespace == "" {
		r.MAPINamespace = defaultMAPINamespace
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(clusterOperatorPredicates())).
		// Watch the provider-specific InfraCluster object.
		Watches(
			watchedObject,
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(infraClusterPredicate(r.ManagedNamespace)),
		).
		// Watch CPMS as the primary source for deriving a provider spec during InfraCluster
		// generation. CPMS events should retrigger reconciliation so we re-evaluate InfraCluster creation and CO status.
		Watches(
			&mapiv1.ControlPlaneMachineSet{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(util.FilterNamespace(r.MAPINamespace)),
		).
		// Watch control plane Machines as a fallback provider spec source when CPMS is absent or inactive.
		Watches(
			&mapiv1beta1.Machine{},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(util.FilterNamespace(r.MAPINamespace)),
		).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

func setReadiness(infraCluster client.Object, readiness bool) error {
	unstructuredInfraCluster, err := runtime.DefaultUnstructuredConverter.ToUnstructured(infraCluster)
	if err != nil {
		return fmt.Errorf("unable to convert to unstructured: %w", err)
	}

	if err := unstructured.SetNestedField(unstructuredInfraCluster, readiness, "status", "ready"); err != nil {
		return fmt.Errorf("unable to set status: %w", err)
	}

	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredInfraCluster, infraCluster); err != nil {
		return fmt.Errorf("unable to convert from unstructured: %w", err)
	}

	return nil
}

func getReadiness(infraCluster client.Object) (bool, error) {
	unstructuredInfraCluster, err := runtime.DefaultUnstructuredConverter.ToUnstructured(infraCluster)
	if err != nil {
		return false, fmt.Errorf("unable to convert to unstructured: %w", err)
	}

	val, found, err := unstructured.NestedBool(unstructuredInfraCluster, "status", "ready")
	if err != nil {
		return false, fmt.Errorf("incorrect value for Status.Ready: %w", err)
	}

	if !found {
		return false, nil
	}

	return val, nil
}

// getRawMAPIProviderSpec returns a raw Machine ProviderSpec from the the cluster.
func (r *InfraClusterController) getRawMAPIProviderSpec(ctx context.Context, cl client.Client) ([]byte, error) {
	cpms, err := r.getActiveCPMS(ctx, cl)
	if err != nil {
		return nil, fmt.Errorf("unable to get control plane machine set: %w", err)
	}

	controlPlaneSelector := client.MatchingLabels{
		"machine.openshift.io/cluster-api-machine-role": "master",
		"machine.openshift.io/cluster-api-machine-type": "master",
	}

	if cpms == nil {
		// The CPMS is not present or inactive.
		// Devise providerSpec via one of the control plane machines in the cluster.
		machineList := &mapiv1beta1.MachineList{}
		if err := cl.List(ctx, machineList, controlPlaneSelector, client.InNamespace(r.MAPINamespace)); err != nil {
			return nil, fmt.Errorf("%w: %w", errUnableToListControlPlaneMachines, err)
		}

		if len(machineList.Items) == 0 {
			return nil, errUnableToFindControlPlaneMachines
		}

		sortedMachines := sortMachinesByCreationTimeDescending(machineList.Items)
		newest := sortedMachines[0]

		// Take the Provider Spec of the first in the machines slice
		// as a the one to be put on the ControlPlaneMachineSet spec.
		// Since the `machines` slice is sorted by descending creation time
		// we are guaranteed to get the newest Provider Spec of a machine.
		// This is done so that if there are control plane machines with differing
		// Provider Specs, we will use the most recent one. This is an attempt to try and inferr
		// the spec that the user might want to choose among the different ones found in the cluster.
		return newest.Spec.ProviderSpec.Value.Raw, nil
	}

	// Devise providerSpec via CPMS.
	return cpms.Spec.Template.OpenShiftMachineV1Beta1Machine.Spec.ProviderSpec.Value.Raw, nil
}

// getMAPIProviderSpec obtains the raw provider spec using the provided getter and unmarshals it into the specified type.
func getMAPIProviderSpec[T any](ctx context.Context, cl client.Client, getter func(context.Context, client.Client) ([]byte, error)) (*T, error) {
	rawProviderSpec, err := getter(ctx, cl)
	if err != nil {
		return nil, fmt.Errorf("unable to obtain MAPI ProviderSpec: %w", err)
	}

	providerSpec := new(T)
	if err := yaml.Unmarshal(rawProviderSpec, providerSpec); err != nil {
		return nil, fmt.Errorf("unable to unmarshal MAPI ProviderSpec: %w", err)
	}

	return providerSpec, nil
}

// sortMachinesByCreationTimeDescending sorts a slice of Machines by CreationTime, Name (descending).
func sortMachinesByCreationTimeDescending(machines []mapiv1beta1.Machine) []mapiv1beta1.Machine {
	// Sort in inverse order so that the newest one is first.
	sort.Slice(machines, func(i, j int) bool {
		first, second := machines[i].CreationTimestamp, machines[j].CreationTimestamp
		if first != second {
			return second.Before(&first)
		}

		return machines[i].Name > machines[j].Name
	})

	return machines
}

// getActiveCPMS returns the CPMS if it exists and it is in Active state, otherwise returns nil.
func (r *InfraClusterController) getActiveCPMS(ctx context.Context, cl client.Client) (*mapiv1.ControlPlaneMachineSet, error) {
	cpms := &mapiv1.ControlPlaneMachineSet{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "cluster", Namespace: r.MAPINamespace}, cpms); err != nil {
		if kerrors.IsNotFound(err) {
			return nil, nil //nolint:nilnil
		}

		return nil, fmt.Errorf("error while getting control plane machine set: %w", err)
	}

	if cpms.Spec.State != mapiv1.ControlPlaneMachineSetStateActive {
		return nil, nil //nolint:nilnil
	}

	return cpms, nil
}

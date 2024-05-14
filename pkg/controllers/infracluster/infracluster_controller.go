package infracluster

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/go-logr/logr"

	cerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"k8s.io/client-go/rest"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha7"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	// Controller conditions for the Cluster Operator resource
	InfraClusterControllerAvailableCondition = "InfraClusterControllerAvailable"
	InfraClusterControllerDegradedCondition  = "InfraClusterControllerDegraded"

	defaultCAPINamespace = "openshift-cluster-api"
	clusterOperatorName  = "cluster-api"
	// This is the managedByAnnotation value that this controller sets by default when it creates an InfraCluster object.
	// If the managedByAnnotation key is set, and it has this as the value, it means this controller is managing the InfraCluster.
	managedByAnnotationValueClusterCAPIOperatorInfraClusterController = "cluster-capi-operator-infracluster-controller"
)

type InfraClusterController struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme   *runtime.Scheme
	Images   map[string]string
	RestCfg  *rest.Config
	Platform configv1.PlatformType
	Infra    *configv1.Infrastructure
}

// Reconcile reconciles the cluster-api ClusterOperator object.
func (r *InfraClusterController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName("InfraClusterController")

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
		gcpCluster := &gcpv1.GCPCluster{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: defaultCAPINamespace, Name: r.Infra.Status.InfrastructureName}, gcpCluster); err != nil && !cerrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting InfraCluster object: %w", err)
		}
		infraCluster = gcpCluster
	case configv1.PowerVSPlatformType:
		powervsCluster := &ibmpowervsv1.IBMPowerVSCluster{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: defaultCAPINamespace, Name: r.Infra.Status.InfrastructureName}, powervsCluster); err != nil && !cerrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting InfraCluster object: %w", err)
		}
		infraCluster = powervsCluster
	case configv1.VSpherePlatformType:
		vsphereCluster := &vspherev1.VSphereCluster{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: defaultCAPINamespace, Name: r.Infra.Status.InfrastructureName}, vsphereCluster); err != nil && !cerrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting InfraCluster object: %w", err)
		}
		infraCluster = vsphereCluster
	case configv1.OpenStackPlatformType:
		openstackCluster := &openstackv1.OpenStackCluster{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: defaultCAPINamespace, Name: r.Infra.Status.InfrastructureName}, openstackCluster); err != nil && !cerrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting InfraCluster object: %w", err)
		}
		infraCluster = openstackCluster
	default:
		return nil, fmt.Errorf("detected platform %q is not supported, skipping capi controllers setup", r.Platform)
	}

	return infraCluster, nil
}

// reconcile performs the main business logic for installing Cluster API components in the cluster.
// Notably it fetches CAPI providers "transport" ConfigMap(s) matching the required labels,
// it extracts from those ConfigMaps the embedded CAPI providers manifests for the components
// and it applies them to the cluster.
//
//nolint:unparam
func (r *InfraClusterController) reconcile(ctx context.Context, log logr.Logger) (ctrl.Result, error) {
	infraCluster, err := r.ensureInfraCluster(ctx, log)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to ensure InfraCluster: %w", err)
	}

	if infraCluster == nil {
		log.Info("Could not find or create an InfraCluster on this supported platform. Waiting for one to be manually created.")
		// This normally means the InfraCluster generation hasn't been implemented for this supported platform yet.
		// See the TODO above.
		return ctrl.Result{}, nil
	}

	// At this point, the InfraCluster exists.
	// Check if it has the managedByAnnotation.
	managedByAnnotationVal, foundAnnotation := infraCluster.GetAnnotations()[clusterv1.ManagedByAnnotation]
	if !foundAnnotation {
		// Could not find the managedByAnnotation on the InfraCluster object.
		// This means, by definition, that the object is directly managed by CAPI infrastructure providers.
		// No action should be taken by this controller.
		log.Info(fmt.Sprintf("InfraCluster '%s/%s' does not have the externally managed-by annotation"+
			" - skipping as this is managed directly by the CAPI infrastructure provider",
			infraCluster.GetNamespace(), infraCluster.GetName()))
		return ctrl.Result{}, nil
	}

	switch managedByAnnotationVal {
	case managedByAnnotationValueClusterCAPIOperatorInfraClusterController:
		// At this point it is this controller's responsibility to manage this InfraCluster object.
		isReady, err := getReadiness(infraCluster)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to get readiness for InfraCluster: %w", err)
		}
		if isReady {
			// The Infrastructure for this CAPI Cluster is already ready - nothing to do.
			return ctrl.Result{}, nil
		}

		infraClusterPatchCopy := infraCluster.DeepCopyObject().(client.Object)

		// Set Status.Ready=true to indicate that cluster's infrastructure ready.
		if err := setReadiness(infraCluster, true); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to set readiness for InfraCluster: %v", err)
		}

		if err := r.Client.Status().Patch(ctx, infraCluster, client.MergeFrom(infraClusterPatchCopy)); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to patch InfraCluster: %v", err)
		}

		log.Info(fmt.Sprintf("InfraCluster '%s/%s' successfully set to Ready", infraCluster.GetNamespace(), infraCluster.GetName()))
	default:
		// At this point it is not this controller's responsibility to manage this InfraCluster object, nor it is
		// the CAPI infra providers responsbility to do so. This means this object was created outside of these two entities - thus
		// the creating entity must manage its readiness.
		log.Info(fmt.Sprintf("InfraCluster '%s/%s' is annotated with an unrecognized externally managed annotation value %q"+
			" - skipping as it is not managed by this controller",
			infraCluster.GetNamespace(), infraCluster.GetName(), managedByAnnotationVal))
	}

	return ctrl.Result{}, nil
}

// ensureAWSCluster ensures the AWSCluster cluster object exists.
func (r *InfraClusterController) ensureAWSCluster(ctx context.Context, log logr.Logger) (client.Object, error) {
	target := &awsv1.AWSCluster{ObjectMeta: metav1.ObjectMeta{
		Name:      r.Infra.Status.InfrastructureName,
		Namespace: defaultCAPINamespace,
	}}

	// Checking whether InfraCluster object exists. If it doesn't, create it.
	//nolint:nestif
	if err := r.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil && !cerrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get InfraCluster: %w", err)
	} else if err == nil {
		return target, nil
	}

	log.Info(fmt.Sprintf("AWSCluster %s/%s does not exist, creating it", target.Namespace, target.Name))

	apiUrl, err := url.Parse(r.Infra.Status.APIServerInternalURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiUrl: %w", err)
	}

	port, err := strconv.ParseInt(apiUrl.Port(), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse apiUrl port: %w", err)
	}

	if r.Infra.Status.PlatformStatus == nil {
		return nil, fmt.Errorf("infrastructure PlatformStatus should not be nil: %w", err)
	}

	target = &awsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.Infra.Status.InfrastructureName,
			Namespace: defaultCAPINamespace,
			// The ManagedBy Annotation is set so CAPI infra providers ignore the InfraCluster object,
			// as that's managed externally, in this case by this controller.
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: managedByAnnotationValueClusterCAPIOperatorInfraClusterController,
			},
		},
		Spec: awsv1.AWSClusterSpec{
			Region: r.Infra.Status.PlatformStatus.AWS.Region,
			ControlPlaneEndpoint: clusterv1.APIEndpoint{
				Host: apiUrl.Hostname(),
				Port: int32(port),
			},
		},
	}

	if err := r.Create(ctx, target); err != nil {
		return nil, fmt.Errorf("failed to create InfraCluster: %w", err)
	}

	log.Info(fmt.Sprintf("InfraCluster '%s/%s' successfully created", defaultCAPINamespace, r.Infra.Status.InfrastructureName))

	return target, nil
}

// setAvailableCondition sets the ClusterOperator status condition to Available.
func (r *InfraClusterController) setAvailableCondition(ctx context.Context, log logr.Logger) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return err
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(InfraClusterControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"InfraCluster Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(InfraClusterControllerDegradedCondition, configv1.ConditionFalse, operatorstatus.ReasonAsExpected,
			"InfraCluster Controller works as expected"),
	}

	co.Status.Versions = []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}
	log.V(2).Info("InfraCluster Controller is Available")
	return r.SyncStatus(ctx, co, conds)
}

// SetupWithManager sets up the controller with the Manager.
func (r *InfraClusterController) SetupWithManager(mgr ctrl.Manager, watchedObject client.Object) error {
	build := ctrl.NewControllerManagedBy(mgr).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(clusterOperatorPredicates())).
		Watches(
			watchedObject,
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(infraClusterPredicate(r.ManagedNamespace)),
		)

	return build.Complete(r)
}

func setReadiness(infraCluster client.Object, readiness bool) error {
	unstructuredInfraCluster, err := runtime.DefaultUnstructuredConverter.ToUnstructured(infraCluster)
	if err != nil {
		return fmt.Errorf("unable to convert to unstructured: %v", err)
	}

	if err := unstructured.SetNestedField(unstructuredInfraCluster, readiness, "status", "ready"); err != nil {
		return fmt.Errorf("unable to set status: %w", err)
	}

	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredInfraCluster, infraCluster); err != nil {
		return fmt.Errorf("unable to convert from unstructured: %v", err)
	}

	return nil
}

func getReadiness(infraCluster client.Object) (bool, error) {
	unstructuredInfraCluster, err := runtime.DefaultUnstructuredConverter.ToUnstructured(infraCluster)
	if err != nil {
		return false, fmt.Errorf("unable to convert to unstructured: %v", err)
	}

	val, found, err := unstructured.NestedBool(unstructuredInfraCluster, "status", "ready")
	if err != nil {
		return false, fmt.Errorf("incorrect value for Status.Ready: %v", err)
	}

	if !found {
		return false, nil
	}

	return val, nil
}

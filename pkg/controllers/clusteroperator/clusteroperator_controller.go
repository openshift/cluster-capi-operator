package clusteroperator

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	operatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/assets"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	infrastructureResourceName = "cluster"
)

// ClusterOperatorReconciler reconciles a ClusterOperator object
type ClusterOperatorReconciler struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme             *runtime.Scheme
	Images             map[string]string
	PlatformType       string
	SupportedPlatforms map[string]bool
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterOperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(clusterOperatorPredicates())).
		Watches(
			&source.Kind{Type: &configv1.Infrastructure{}},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(infrastructurePredicates()),
		).
		Complete(r)
}

// Reconcile will process the cluster-api clusterOperator
func (r *ClusterOperatorReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	klog.Infof("Intalling Cluster API components for technical preview cluster")
	// Get infrastructure object
	infra := &configv1.Infrastructure{}
	if err := r.Get(ctx, client.ObjectKey{Name: infrastructureResourceName}, infra); k8serrors.IsNotFound(err) {
		klog.Infof("Infrastructure cluster does not exist. Skipping...")
		if err := r.SetStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		klog.Errorf("Unable to retrive Infrastructure object: %v", err)
		if err := r.SetStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	// Install upstream CAPI Operator
	// TODO re-enable deployment of operator once images are built for all architecture targets (eg x86_64, aarm64, etc)
	// For extended information about this temporary disable, please see https://coreos.slack.com/archives/C01CQA76KMX/p1647367797409909?thread_ts=1647351322.648249&cid=C01CQA76KMX
	/*
		if err := r.installCAPIOperator(ctx); err != nil {
			klog.Errorf("Unable to install CAPI operator: %v", err)
			if err := r.SetStatusDegraded(ctx, err); err != nil {
				return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
			}
			return ctrl.Result{}, err
		}
	*/

	// Install core CAPI components
	if err := r.installCoreCAPIComponents(ctx); err != nil {
		klog.Errorf("Unable to install core CAPI components: %v", err)
		if err := r.SetStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	// Set platform type
	if infra.Status.PlatformStatus == nil {
		klog.Infof("No platform status exists in infrastructure object. Skipping...")
		if err := r.SetStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	r.PlatformType = strings.ToLower(string(infra.Status.PlatformStatus.Type))

	// Check if platform type is supported
	if _, ok := r.SupportedPlatforms[r.PlatformType]; !ok {
		klog.Infof("Platform type %s is not supported. Skipping...", r.PlatformType)
		if err := r.SetStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Install infrastructure CAPI components
	if err := r.installInfrastructureCAPIComponents(ctx); err != nil {
		klog.Errorf("Unable to infrastructure core CAPI components: %v", err)
		if err := r.SetStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.SetStatusAvailable(ctx)
}

// installCAPIOperator reads assets from assets/capi-operator, customizes Deployment and Service objects, and applies them
// TODO re-enable deployment of operator once images are built for all architecture targets (eg x86_64, aarm64, etc)
// For extended information about this temporary disable, please see https://coreos.slack.com/archives/C01CQA76KMX/p1647367797409909?thread_ts=1647351322.648249&cid=C01CQA76KMX
/*
func (r *ClusterOperatorReconciler) installCAPIOperator(ctx context.Context) error {
	klog.Infof("Reconciling CAPI Operator components")
	objs, err := assets.ReadOperatorAssets(r.Scheme)
	if err != nil {
		return fmt.Errorf("unable to read operator assets: %v", err)
	}

	deployment := objs[assets.OperatorDeploymentKey].(*appsv1.Deployment)
	if err := r.reconcileOperatorDeployment(ctx, deployment); err != nil {
		return fmt.Errorf("unable to reconcile operator deployment: %v", err)
	}

	service := objs[assets.OperatorServiceKey].(*corev1.Service)
	if err := r.reconcileOperatorService(ctx, service); err != nil {
		return fmt.Errorf("unable to reconcile operator service: %v", err)
	}

	return nil
}
*/

// installCoreCAPIComponents reads assets from assets/core-capi, create CRs that are consumed by upstream CAPI Operator
func (r *ClusterOperatorReconciler) installCoreCAPIComponents(ctx context.Context) error {
	klog.Infof("Reconciling Core CAPI components")
	objs, err := assets.ReadCoreProviderAssets(r.Scheme)
	if err != nil {
		return fmt.Errorf("unable to read core-capi: %v", err)
	}

	coreProvider := objs[assets.CoreProviderKey].(*operatorv1.CoreProvider)
	if err := r.reconcileCoreProvider(ctx, coreProvider); err != nil {
		return fmt.Errorf("unable to reconcile CoreProvider: %v", err)
	}

	coreProviderCM := objs[assets.CoreProviderConfigMapKey].(*corev1.ConfigMap)
	if err := r.reconcileConfigMap(ctx, coreProviderCM); err != nil {
		return fmt.Errorf("unable to reconcile core provider ConfigMap: %v", err)
	}

	return nil
}

// installInfrastructureCAPIComponents reads assets from assets/providers, create CRs that are consumed by upstream CAPI Operator
func (r *ClusterOperatorReconciler) installInfrastructureCAPIComponents(ctx context.Context) error {
	klog.Infof("Reconciling Infrastructure CAPI components")
	objs, err := assets.ReadInfrastructureProviderAssets(r.Scheme, r.PlatformType)
	if err != nil {
		return fmt.Errorf("unable to read providers: %v", err)
	}

	infraProvider := objs[assets.InfrastructureProviderKey].(*operatorv1.InfrastructureProvider)
	if err := r.reconcileInfrastructureProvider(ctx, infraProvider); err != nil {
		return fmt.Errorf("unable to reconcile InfrastructureProvider: %v", err)
	}

	infraProviderCM := objs[assets.InfrastructureProviderConfigMapKey].(*corev1.ConfigMap)
	if err := r.reconcileConfigMap(ctx, infraProviderCM); err != nil {
		return fmt.Errorf("unable to reconcile infrastructure provider ConfigMap: %v", err)
	}

	return nil
}

package clusteroperator

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	operatorv1 "sigs.k8s.io/cluster-api-operator/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/assets"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
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
	log := ctrl.LoggerFrom(ctx).WithName("ClusterOperatorController")

	log.Info("reconciling Cluster API components for technical preview cluster")
	// Get infrastructure object
	infra := &configv1.Infrastructure{}
	if err := r.Get(ctx, client.ObjectKey{Name: controllers.InfrastructureResourceName}, infra); k8serrors.IsNotFound(err) {
		log.Info("infrastructure cluster does not exist. Skipping...")
		if err := r.SetStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "unable to retrive Infrastructure object")
		if err := r.SetStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	// Install core CAPI components
	if err := r.installCoreCAPIComponents(ctx); err != nil {
		log.Error(err, "unable to install core CAPI components")
		if err := r.SetStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	// Set platform type
	if infra.Status.PlatformStatus == nil {
		log.Info("no platform status exists in infrastructure object. Skipping...")
		if err := r.SetStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	r.PlatformType = strings.ToLower(string(infra.Status.PlatformStatus.Type))

	// Check if platform type is supported
	if _, ok := r.SupportedPlatforms[r.PlatformType]; !ok {
		log.Info("platform type is not supported. Skipping...", "platformType", r.PlatformType)
		if err := r.SetStatusAvailable(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Install infrastructure CAPI components
	if err := r.installInfrastructureCAPIComponents(ctx); err != nil {
		log.Error(err, "unable to infrastructure core CAPI components")
		if err := r.SetStatusDegraded(ctx, err); err != nil {
			return ctrl.Result{}, fmt.Errorf("error syncing ClusterOperatorStatus: %v", err)
		}
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.SetStatusAvailable(ctx)
}

// installCoreCAPIComponents reads assets from assets/core-capi, create CRs that are consumed by upstream CAPI Operator
func (r *ClusterOperatorReconciler) installCoreCAPIComponents(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling Core CAPI components")
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
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling Infrastructure CAPI components")
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

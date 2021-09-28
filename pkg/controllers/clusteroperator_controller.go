package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configv1 "github.com/openshift/api/config/v1"
)

// ClusterOperatorReconciler reconciles a ClusterOperator object
type ClusterOperatorReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Recorder         record.EventRecorder
	ReleaseVersion   string
	ManagedNamespace string
	ImagesFile       string
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterOperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1.ClusterOperator{}).
		Watches(
			&source.Kind{Type: &configv1.Infrastructure{}},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(infrastructurePredicates()),
		).
		Watches(
			&source.Kind{Type: &configv1.FeatureGate{}},
			handler.EnqueueRequestsFromMapFunc(toClusterOperator),
			builder.WithPredicates(featureGatePredicates()),
		).
		Complete(r)
}

// +kubebuilder:rbac:groups=config.openshift.io,resources=clusteroperators,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.openshift.io,resources=clusteroperators/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=config.openshift.io,resources=clusteroperators/finalizers,verbs=update
// +kubebuilder:rbac:groups=config.openshift.io,resources=featuregates;infrastructures,verbs=get;list;watch

// for leaderelections
// +kubebuilder:rbac:namespace=openshift-cluster-api,groups="",resources=configmaps;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:namespace=openshift-cluster-api,groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

// Reconcile will process the cluster-api clusterOperator
func (r *ClusterOperatorReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	featureGate := &configv1.FeatureGate{}
	if err := r.Get(ctx, client.ObjectKey{Name: externalFeatureGateName}, featureGate); errors.IsNotFound(err) {
		klog.Infof("FeatureGate cluster does not exist. Skipping...")
		return ctrl.Result{}, r.setStatusAvailable(ctx)
	} else if err != nil {
		klog.Errorf("Unable to retrive FeatureGate object: %v", err)
		return ctrl.Result{}, r.setStatusDegraded(ctx, err)
	}

	// Verify FeatureGate ClusterAPIEnabled is present for operator to work in TP phase
	capiEnabled, err := isCAPIFeatureGateEnabled(featureGate)
	if err != nil {
		klog.Errorf("Could not determine cluster api feature gate state: %v", err)
		return ctrl.Result{}, r.setStatusDegraded(ctx, err)
	} else if !capiEnabled {
		klog.Infof("FeatureGate cluster does not include cluster api. Skipping...")
		return ctrl.Result{}, r.setStatusAvailable(ctx)
	}

	return ctrl.Result{}, nil
}

package controllers

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1alpha4"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// GCPClusterReconciler reconciles a GCPCluster object
type GCPClusterReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func (r *GCPClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	gcpCluster := &gcpv1.GCPCluster{}
	err := r.Get(ctx, req.NamespacedName, gcpCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	clusterToBePatched := client.MergeFrom(gcpCluster.DeepCopy())
	gcpCluster.Status.Ready = true
	klog.Info("Patching GCPCluster status")

	return ctrl.Result{}, r.Status().Patch(ctx, gcpCluster, clusterToBePatched)
}

func (r *GCPClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gcpv1.GCPCluster{}).
		Complete(r)
}

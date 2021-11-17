package controllers

import (
	"context"

	metal3v1 "github.com/metal3-io/cluster-api-provider-metal3/api/v1alpha4"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Metal3ClusterReconciler reconciles a Metal3Cluster object
type Metal3ClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *Metal3ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	m3Cluster := &metal3v1.Metal3Cluster{}
	err := r.Get(ctx, req.NamespacedName, m3Cluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	clusterToBePatched := client.MergeFrom(m3Cluster.DeepCopy())
	m3Cluster.Status.Ready = true

	klog.Info("Patching GCPCluster status")
	return ctrl.Result{}, r.Status().Patch(ctx, m3Cluster, clusterToBePatched)
}

func (r *Metal3ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&metal3v1.Metal3Cluster{}).
		Complete(r)
}

package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha4"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// OpenStackClusterReconciler reconciles an OpenStackCluster object
type OpenStackClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *OpenStackClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	osCluster := &openstackv1.OpenStackCluster{}
	err := r.Get(ctx, req.NamespacedName, osCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	clusterToBePatched := client.MergeFrom(osCluster.DeepCopy())
	osCluster.Status.Ready = true

	klog.Info("Patching OpenStackCluster status")
	return ctrl.Result{}, r.Status().Patch(ctx, osCluster, clusterToBePatched)
}

func (r *OpenStackClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openstackv1.OpenStackCluster{}).
		Complete(r)
}

package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// AzureClusterReconciler reconciles a AWSCluster object
type AzureClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *AzureClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	awsCluster := &azurev1.AzureCluster{}
	err := r.Get(ctx, req.NamespacedName, awsCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	clusterToBePatched := client.MergeFrom(awsCluster.DeepCopy())
	awsCluster.Status.Ready = true

	klog.Info("Patching AzureCluster status")
	return ctrl.Result{}, r.Status().Patch(ctx, awsCluster, clusterToBePatched)
}

func (r *AzureClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&azurev1.AzureCluster{}).
		Complete(r)
}

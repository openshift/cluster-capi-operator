package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	aws "sigs.k8s.io/cluster-api-provider-aws/api/v1alpha4"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// AWSClusterReconciler reconciles a AWSCluster object
type AWSClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *AWSClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	awsCluster := &aws.AWSCluster{}
	err := r.Get(ctx, req.NamespacedName, awsCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	clusterToBePatched := client.MergeFrom(awsCluster.DeepCopy())
	awsCluster.Status.Ready = true

	klog.Info("Patching AWSCluster status")
	return ctrl.Result{}, r.Status().Patch(ctx, awsCluster, clusterToBePatched)
}

func (r *AWSClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aws.AWSCluster{}).
		Complete(r)
}

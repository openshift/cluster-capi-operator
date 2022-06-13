package cluster

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

type CoreClusterReconciler struct {
	operatorstatus.ClusterOperatorStatusClient
	Cluster *clusterv1.Cluster
}

func (r *CoreClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(r.Cluster).
		Complete(r)
}

func (r *CoreClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName("CoreClusterController")

	cluster := &clusterv1.Cluster{}

	if err := r.Client.Get(ctx, req.NamespacedName, cluster); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if !cluster.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.SetStatusAvailable(ctx)
	}

	log.Info("Reconciling core cluster")

	clusterCopy := cluster.DeepCopy()

	conditions.MarkTrue(cluster, clusterv1.ControlPlaneInitializedCondition)
	if err := r.Status().Patch(ctx, cluster, client.MergeFrom(clusterCopy)); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to update core cluster status: %v", err)
	}

	return ctrl.Result{}, r.SetStatusAvailable(ctx)
}

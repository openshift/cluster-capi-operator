package cluster

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

type GenericInfraClusterReconciler struct {
	operatorstatus.ClusterOperatorStatusClient
	InfraCluster client.Object
}

func (r *GenericInfraClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(r.InfraCluster).
		Complete(r)
}

func (r *GenericInfraClusterReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName("InfraClusterController")

	infraClusterCopy := r.InfraCluster.DeepCopyObject().(client.Object)
	if err := r.Client.Get(ctx, req.NamespacedName, infraClusterCopy); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if !infraClusterCopy.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, r.SetStatusAvailable(ctx)
	}

	log.Info("Reconciling infrastructure cluster")

	infraClusterPatchCopy, ok := infraClusterCopy.DeepCopyObject().(client.Object)
	if !ok {
		return ctrl.Result{}, fmt.Errorf("unable to convert to client object")
	}

	// Set externally managed annotation
	infraClusterCopy.SetAnnotations(setManagedByAnnotation(infraClusterCopy.GetAnnotations()))
	if err := r.Client.Patch(ctx, infraClusterCopy, client.MergeFrom(infraClusterPatchCopy)); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to patch infra cluster: %v", err)
	}

	// Set status to ready
	unstructuredInfraCluster, err := runtime.DefaultUnstructuredConverter.ToUnstructured(infraClusterCopy)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to convert to unstructured: %v", err)
	}

	if err := unstructured.SetNestedField(unstructuredInfraCluster, true, "status", "ready"); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to set status: %w", err)
	}

	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredInfraCluster, infraClusterCopy); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to convert from unstructured: %v", err)
	}

	if err := r.Status().Patch(ctx, infraClusterCopy, client.MergeFrom(infraClusterPatchCopy)); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to patch cluster status: %w", err)
	}

	return ctrl.Result{}, r.SetStatusAvailable(ctx)
}

func setManagedByAnnotation(annotations map[string]string) map[string]string {
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[clusterv1.ManagedByAnnotation] = ""

	return annotations
}

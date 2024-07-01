package infracluster

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1 "github.com/openshift/api/config/v1"
)

// clusterOperatorPredicates defines a predicate function for the cluster-api ClusterOperator.
func clusterOperatorPredicates() predicate.Funcs {
	isClusterOperator := func(obj runtime.Object) bool {
		clusterOperator, ok := obj.(*configv1.ClusterOperator)
		return ok && clusterOperator.GetName() == clusterOperatorName
	}

	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isClusterOperator(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isClusterOperator(e.ObjectNew) },
		GenericFunc: func(e event.GenericEvent) bool { return isClusterOperator(e.Object) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isClusterOperator(e.Object) },
	}
}

// toClusterOperator maps a reconcile request to the cluster-api ClusterOperator.
func toClusterOperator(ctx context.Context, cO client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: clusterOperatorName},
	}}
}

// infraClusterPredicate defines a predicate function for owned infraClusters.
func infraClusterPredicate(namespace string) predicate.Funcs {
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isOwnedCInfraCluster(e.Object, namespace) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isOwnedCInfraCluster(e.ObjectNew, namespace) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isOwnedCInfraCluster(e.Object, namespace) },
		GenericFunc: func(e event.GenericEvent) bool { return isOwnedCInfraCluster(e.Object, namespace) },
	}
}

// isOwnedInfraCluster checks whether an object is an owned provider component.
func isOwnedCInfraCluster(obj runtime.Object, namespace string) bool {
	cO := obj.(client.Object)

	if cO.GetNamespace() != namespace {
		return false
	}

	return true
}

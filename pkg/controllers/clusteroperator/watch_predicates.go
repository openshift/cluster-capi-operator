package clusteroperator

import (
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
)

func toClusterOperator(client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: controllers.ClusterOperatorName},
	}}
}

func clusterOperatorPredicates() predicate.Funcs {
	isClusterOperatorCluster := func(obj runtime.Object) bool {
		co, ok := obj.(*configv1.ClusterOperator)
		return ok && co.GetName() == controllers.ClusterOperatorName
	}

	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isClusterOperatorCluster(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isClusterOperatorCluster(e.ObjectNew) },
		GenericFunc: func(e event.GenericEvent) bool { return isClusterOperatorCluster(e.Object) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isClusterOperatorCluster(e.Object) },
	}
}

func infrastructurePredicates() predicate.Funcs {
	isInfrastructureCluster := func(obj runtime.Object) bool {
		infra, ok := obj.(*configv1.Infrastructure)
		return ok && infra.GetName() == controllers.InfrastructureResourceName
	}

	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isInfrastructureCluster(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isInfrastructureCluster(e.ObjectNew) },
		GenericFunc: func(e event.GenericEvent) bool { return isInfrastructureCluster(e.Object) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isInfrastructureCluster(e.Object) },
	}
}

package capiinstaller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1 "github.com/openshift/api/config/v1"
)

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

func toClusterOperator(ctx context.Context, cO client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: clusterOperatorName},
	}}
}

func configMapPredicate(namespace string, platform configv1.PlatformType) predicate.Funcs {
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isOwnedProviderComponent(e.Object, namespace, platform) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isOwnedProviderComponent(e.ObjectNew, namespace, platform) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isOwnedProviderComponent(e.Object, namespace, platform) },
		GenericFunc: func(e event.GenericEvent) bool { return isOwnedProviderComponent(e.Object, namespace, platform) },
	}
}

func ownedPlatformLabelPredicate(namespace string, platform configv1.PlatformType) predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool { return isOwnedProviderComponent(e.ObjectNew, namespace, platform) },
		DeleteFunc: func(e event.DeleteEvent) bool { return isOwnedProviderComponent(e.Object, namespace, platform) },
	}
}

func isOwnedProviderComponent(obj runtime.Object, namespace string, platform configv1.PlatformType) bool {
	cO := obj.(client.Object)

	if cO.GetNamespace() != namespace {
		return false
	}

	providerName, hasLabel := cO.GetLabels()[ownedProviderComponentName]
	if !hasLabel {
		return false
	}

	switch {
	case providerName == defaultCoreProviderComponentName:
		// this is the core CAPI provider.
		return true
	case providerName == platformToInfraProviderComponentName(platform):
		return true
	}

	return false
}

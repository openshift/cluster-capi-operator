package capiinstaller

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func toProviderConfigMap(cO client.Object) []reconcile.Request {
	providerName, hasLabel := cO.GetLabels()[ownedProviderComponentName]
	if !hasLabel {
		return nil
	}

	cmName := ""
	switch {
	case providerName == "cluster-api":
		cmName = "cluster-api"
	case strings.Contains(providerName, "infrastructure-"):
		parts := strings.Split(providerName, "-")
		cmName = parts[1]
	default:
		return nil
	}

	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: cmName, Namespace: defaultCAPINamespace},
	}}
}

func configMapPredicate(targetNamespace string) predicate.Funcs {
	isOwnedConfigMap := func(obj runtime.Object) bool {
		cm, ok := obj.(*corev1.ConfigMap)

		// TODO: refine this.
		_, hasLabel := cm.Labels[configMapVersionLabelName]
		return ok && cm.GetNamespace() == targetNamespace && hasLabel
	}

	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isOwnedConfigMap(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isOwnedConfigMap(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isOwnedConfigMap(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return isOwnedConfigMap(e.Object) },
	}
}

func ownedLabelPredicate(targetNamespace string) predicate.Funcs {
	isOwnedKind := func(obj runtime.Object) bool {
		cO := obj.(client.Object)

		// TODO: refine this.
		_, hasLabel := cO.GetLabels()[ownedProviderComponentName]
		return cO.GetNamespace() == targetNamespace && hasLabel
	}

	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool { return isOwnedKind(e.ObjectNew) },
		DeleteFunc: func(e event.DeleteEvent) bool { return isOwnedKind(e.Object) },
	}
}

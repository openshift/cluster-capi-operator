package capiinstaller

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// func toConfigMap(client.Object) []reconcile.Request {
// 	return []reconcile.Request{{
// 		NamespacedName: client.ObjectKey{Name: "", Namespace: defaultCAPINamespace},
// 	}}
// }

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

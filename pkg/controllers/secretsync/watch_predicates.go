package secretsync

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func toUserDataSecret(client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: managedUserDataSecretName, Namespace: SecretSourceNamespace},
	}}
}

func userDataSecretPredicate(targetNamespace string) predicate.Funcs {
	isOwnedUserDataSecret := func(obj runtime.Object) bool {
		secret, ok := obj.(*corev1.Secret)
		return ok && secret.GetNamespace() == targetNamespace && secret.GetName() == managedUserDataSecretName
	}

	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isOwnedUserDataSecret(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isOwnedUserDataSecret(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isOwnedUserDataSecret(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return isOwnedUserDataSecret(e.Object) },
	}
}

package kubeconfig

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/controllers"
)

func toTokenSecret(client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: tokenSecretName, Namespace: controllers.DefaultManagedNamespace},
	}}
}

func tokenSecretPredicate() predicate.Funcs {
	isOwnedTokenSecret := func(obj runtime.Object) bool {
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			panic("expected to get an of object of type corev1.Secret")
		}

		return secret.GetNamespace() == controllers.DefaultManagedNamespace && secret.GetName() == tokenSecretName
	}

	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isOwnedTokenSecret(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isOwnedTokenSecret(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isOwnedTokenSecret(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return isOwnedTokenSecret(e.Object) },
	}
}

func kubeconfigSecretPredicate() predicate.Funcs {
	isKubeconfigSecret := func(obj runtime.Object) bool {
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			panic("expected to get an of object of type corev1.Secret")
		}

		return secret.GetNamespace() == controllers.DefaultManagedNamespace && strings.HasSuffix(secret.GetName(), "-kubeconfig")
	}

	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isKubeconfigSecret(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isKubeconfigSecret(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isKubeconfigSecret(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return isKubeconfigSecret(e.Object) },
	}
}

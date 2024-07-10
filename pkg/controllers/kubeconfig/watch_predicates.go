/*
Copyright 2024 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package kubeconfig

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/controllers"
)

func toTokenSecret(ctx context.Context, o client.Object) []reconcile.Request {
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

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
package secretsync

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func toUserDataSecret(namespace string) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		return []reconcile.Request{{
			NamespacedName: client.ObjectKey{Name: managedUserDataSecretName, Namespace: namespace},
		}}
	}
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

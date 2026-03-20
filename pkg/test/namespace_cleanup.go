/*
Copyright 2026 Red Hat, Inc.

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

package test

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// AddNamespaceFinalizerCleanup registers a controller on mgr that clears spec
// finalizers from terminating Namespace objects via the /finalize subresource.
//
// In envtest the namespace controller doesn't run, so namespaces get stuck in
// Terminating state. This controller watches for namespaces with a non-zero
// deletion timestamp and removes their spec finalizers so deletion completes.
func AddNamespaceFinalizerCleanup(mgr ctrl.Manager) error {
	cl := mgr.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		WithEventFilter(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetDeletionTimestamp() != nil
		})).
		Complete(reconcile.Func(func(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
			var ns corev1.Namespace
			if err := cl.Get(ctx, req.NamespacedName, &ns); err != nil {
				return reconcile.Result{}, client.IgnoreNotFound(err)
			}

			if ns.DeletionTimestamp.IsZero() || len(ns.Spec.Finalizers) == 0 {
				return reconcile.Result{}, nil
			}

			ns.Spec.Finalizers = nil
			if err := cl.SubResource("finalize").Update(ctx, &ns); err != nil {
				return reconcile.Result{}, client.IgnoreNotFound(err)
			}

			return reconcile.Result{}, nil
		}))
}

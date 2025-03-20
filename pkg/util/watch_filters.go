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

package util

import (
	"context"
	"fmt"

	"k8s.io/klog/v2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const machineSetKind = "MachineSet"

// RewriteNamespace takes a client.Object and returns a reconcile.Request for
// it in the namespace provided.
//
// It is intended for use with CAPI Machines and MachineSet requests, where we expect
// there to be a mirror object in the MAPI namespace.
func RewriteNamespace(namespace string) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		klog.V(4).Info(
			"reconcile triggered by object",
			"objectType", fmt.Sprintf("%T", obj),
			"namespace", obj.GetNamespace(),
			"name", obj.GetName(),
		)

		return []reconcile.Request{{
			NamespacedName: client.ObjectKey{Namespace: namespace, Name: obj.GetName()},
		}}
	}
}

// ResolveCAPIMachineSetFromObject should probably be renamed. It:
// 1. takes a client.Object (expecting a CAPI InfrastructureMachineTemplate)
// and checks to see if it's owned by a CAPI MachineSet
// 2. If it is, returns a reconcile.Request for the MAPI namespace, so we
// reconcile the mirror MAPI MachineSet.
func ResolveCAPIMachineSetFromObject(namespace string) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		klog.V(4).Info(
			"reconcile triggered by object",
			"objectType", fmt.Sprintf("%T", obj),
			"namespace", obj.GetNamespace(),
			"name", obj.GetName(),
		)

		ownerReferences := obj.GetOwnerReferences()
		requests := []reconcile.Request{}

		for _, ref := range ownerReferences {
			if ref.Kind != machineSetKind || ref.APIVersion != capiv1beta1.GroupVersion.String() {
				continue
			}

			klog.V(4).Info("Object is owned by a Cluster API machineset, enqueueing request",
				"machine", obj.GetName(), "machineset", ref.Name)

			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{Namespace: namespace, Name: ref.Name},
			})
		}

		return requests
	}
}

// FilterNamespace filters a client.Object request, ensuring they are in the
// namespace provided.
func FilterNamespace(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		// Check namespace is as expected
		return obj.GetNamespace() == namespace
	})
}

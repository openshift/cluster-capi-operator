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

	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"k8s.io/klog/v2"
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

// ResolveCAPIMachineSetFromInfraMachineTemplate resolves a synchronized MachineSet from an InfrastructureMachineTemplate.
// It takes a client.Object (expecting a CAPI InfrastructureMachineTemplate) and checks if it has
// the machine.openshift.io/cluster-api-machineset label. If present, it returns a reconcile.Request
// for the corresponding MachineSet in the MAPI namespace to trigger reconciliation of the mirror MAPI MachineSet.
func ResolveCAPIMachineSetFromInfraMachineTemplate(namespace string) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		klog.V(4).Info(
			"reconcile triggered by object",
			"objectType", fmt.Sprintf("%T", obj),
			"namespace", obj.GetNamespace(),
			"name", obj.GetName(),
		)

		objLabels := obj.GetLabels()
		requests := []reconcile.Request{}

		machineSetName, ok := objLabels[controllers.MachineSetOpenshiftLabelKey]
		if ok {
			klog.V(4).Info("Object has machine.openshift.io/cluster-api-machineset label, enqueueing request",
				"InfraMachineTemplate", obj.GetName(), machineSetKind, machineSetName)

			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{Namespace: namespace, Name: machineSetName},
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

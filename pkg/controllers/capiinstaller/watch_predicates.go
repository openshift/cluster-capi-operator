// Copyright 2024 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
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

// clusterOperatorPredicates defines a predicate function for the cluster-api ClusterOperator.
func clusterOperatorPredicates() predicate.Funcs {
	isClusterOperator := func(obj runtime.Object) bool {
		clusterOperator, ok := obj.(*configv1.ClusterOperator)
		return ok && clusterOperator.GetName() == clusterOperatorName
	}

	// We only want to be reconciled on creation of the cluster operator,
	// because we wait for it before reconciling. The Create event also fires
	// when the manager is started, so this will additionally ensure we are
	// called at least once at startup.
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return isClusterOperator(e.Object) },
	}
}

// toClusterOperator maps a reconcile request to the cluster-api ClusterOperator.
func toClusterOperator(ctx context.Context, cO client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: clusterOperatorName},
	}}
}

// configMapPredicate defines a predicate function for owned ConfigMaps.
func configMapPredicate(namespace string, platform configv1.PlatformType) predicate.Funcs {
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isOwnedProviderComponent(e.Object, namespace, platform) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isOwnedProviderComponent(e.ObjectNew, namespace, platform) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isOwnedProviderComponent(e.Object, namespace, platform) },
		GenericFunc: func(e event.GenericEvent) bool { return isOwnedProviderComponent(e.Object, namespace, platform) },
	}
}

// ownedPlatformLabelPredicate defines a predicate function for owned objects.
func ownedPlatformLabelPredicate(namespace string, platform configv1.PlatformType) predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool { return isOwnedProviderComponent(e.ObjectNew, namespace, platform) },
		DeleteFunc: func(e event.DeleteEvent) bool { return isOwnedProviderComponent(e.Object, namespace, platform) },
	}
}

// isOwnedProviderComponent checks whether an object is an owned provider component.
func isOwnedProviderComponent(obj runtime.Object, namespace string, platform configv1.PlatformType) bool {
	cO, ok := obj.(client.Object)
	if !ok {
		return false
	}

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

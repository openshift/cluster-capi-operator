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
package operatorstatus

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
)

// OwnedStateOption declares additional cluster-api ClusterOperator state
// owned by a controller, beyond its own Progressing and Available
// sub-conditions.
type OwnedStateOption func(*ownedClusterOperatorState)

// OwnedConditions adds extra condition types owned by the controller, for
// example the bare Upgradeable condition published alongside the health
// sub-conditions.
func OwnedConditions(conditionTypes ...configv1.ClusterStatusConditionType) OwnedStateOption {
	return func(s *ownedClusterOperatorState) {
		s.conditionTypes = append(s.conditionTypes, conditionTypes...)
	}
}

// OwnedVersions adds status.versions to the owned state. Today the
// ClusterOperator carries a single "operator" entry, so comparing the whole
// list is equivalent to comparing that entry.
func OwnedVersions() OwnedStateOption {
	return func(s *ownedClusterOperatorState) { s.compareVersions = true }
}

// OwnedRelatedObjects adds status.relatedObjects to the owned state.
func OwnedRelatedObjects() OwnedStateOption {
	return func(s *ownedClusterOperatorState) { s.compareRelatedObjects = true }
}

// ownedClusterOperatorState is the cluster-api ClusterOperator state owned by
// a single controller, used to scope its ClusterOperator watch.
type ownedClusterOperatorState struct {
	conditionTypes        []configv1.ClusterStatusConditionType
	compareVersions       bool
	compareRelatedObjects bool
}

// ClusterOperatorOwnedStateChanged returns a predicate for the cluster-api
// ClusterOperator that matches create events, and update events where state
// owned by the given controller changed: its Progressing and Available
// sub-conditions, plus anything added through options.
//
// Controllers publish status through the informer-cached client and skip the
// write when the cached state already matches. During control-plane
// disruptions the informer can lag behind the apiserver, so a write can be
// wrongly skipped against stale data with no pending requeue. Watching the
// owned state makes such a skip self-correcting: once the informer catches
// up, the change event requeues the controller to republish.
func ClusterOperatorOwnedStateChanged(g ControllerResultGenerator, opts ...OwnedStateOption) predicate.Funcs {
	owned := ownedClusterOperatorState{
		conditionTypes: []configv1.ClusterStatusConditionType{
			g.SubConditionType(ConditionProgressingSuffix),
			g.SubConditionType(ConditionAvailableSuffix),
		},
	}

	for _, opt := range opts {
		opt(&owned)
	}

	isClusterOperator := func(obj runtime.Object) bool {
		clusterOperator, ok := obj.(*configv1.ClusterOperator)
		return ok && clusterOperator.GetName() == controllers.ClusterOperatorName
	}

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return isClusterOperator(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !isClusterOperator(e.ObjectNew) {
				return false
			}

			oldCO, _ := e.ObjectOld.(*configv1.ClusterOperator)
			newCO, _ := e.ObjectNew.(*configv1.ClusterOperator)

			if oldCO == nil {
				return false
			}

			for _, condType := range owned.conditionTypes {
				if clusterOperatorConditionChanged(condType, oldCO, newCO) {
					return true
				}
			}

			if owned.compareVersions && !equality.Semantic.DeepEqual(oldCO.Status.Versions, newCO.Status.Versions) {
				return true
			}

			if owned.compareRelatedObjects && !equality.Semantic.DeepEqual(oldCO.Status.RelatedObjects, newCO.Status.RelatedObjects) {
				return true
			}

			return false
		},
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

// clusterOperatorConditionChanged reports whether the condition of the given
// type was added, removed, or changed status, reason or message. Only the
// semantic content is compared: lastTransitionTime changes alone do not match
// because they do not change what the condition's owner would republish.
func clusterOperatorConditionChanged(condType configv1.ClusterStatusConditionType, oldCO, newCO *configv1.ClusterOperator) bool {
	oldCondition := findClusterOperatorCondition(condType, oldCO.Status.Conditions)
	newCondition := findClusterOperatorCondition(condType, newCO.Status.Conditions)

	switch {
	case oldCondition == nil && newCondition == nil:
		return false
	case oldCondition == nil || newCondition == nil:
		// The condition was added or removed.
		return true
	default:
		return oldCondition.Status != newCondition.Status ||
			oldCondition.Reason != newCondition.Reason ||
			oldCondition.Message != newCondition.Message
	}
}

// ClusterOperatorStatusChanged returns a predicate that triggers on create
// events for the cluster-api ClusterOperator, and on update events when
// status.Conditions or status.Versions has changed.
func ClusterOperatorStatusChanged() predicate.Funcs {
	isClusterOperator := func(obj runtime.Object) bool {
		clusterOperator, ok := obj.(*configv1.ClusterOperator)
		return ok && clusterOperator.GetName() == controllers.ClusterOperatorName
	}

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return isClusterOperator(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !isClusterOperator(e.ObjectNew) {
				return false
			}

			oldCO, _ := e.ObjectOld.(*configv1.ClusterOperator)
			newCO, _ := e.ObjectNew.(*configv1.ClusterOperator)

			return !equality.Semantic.DeepEqual(oldCO.Status.Conditions, newCO.Status.Conditions) ||
				!equality.Semantic.DeepEqual(oldCO.Status.Versions, newCO.Status.Versions)
		},
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

// ToClusterOperator unconditionally returns a reconcile request for the cluster-api ClusterOperator.
func ToClusterOperator(_ context.Context, _ client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{Name: controllers.ClusterOperatorName},
	}}
}

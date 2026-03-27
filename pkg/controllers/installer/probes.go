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

package installer

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"pkg.package-operator.run/boxcutter/probing"
)

// allProbes returns all probes used by the installer controller.
// Each probe uses GroupKindSelector so that non-matching objects automatically pass.
func allProbes() []*probing.GroupKindSelector {
	return []*probing.GroupKindSelector{
		crdEstablishedProbe(),
		deploymentAvailableProbe(),
	}
}

// crdEstablishedProbe checks that a CRD has the Established condition set to True.
// It uses GroupKindSelector so that non-CRD objects automatically pass.
func crdEstablishedProbe() *probing.GroupKindSelector {
	return &probing.GroupKindSelector{
		GroupKind: schema.GroupKind{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"},
		Prober:    &probing.ConditionProbe{Type: "Established", Status: "True"},
	}
}

// deploymentAvailableProbe checks that a Deployment has the Available condition set to True.
// It uses GroupKindSelector so that non-Deployment objects automatically pass.
func deploymentAvailableProbe() *probing.GroupKindSelector {
	return &probing.GroupKindSelector{
		GroupKind: schema.GroupKind{Group: "apps", Kind: "Deployment"},
		Prober:    &probing.ConditionProbe{Type: "Available", Status: "True"},
	}
}

// probeSucceededPredicate returns a predicate that triggers reconciliation when
// a probed object's probe transitions to success (e.g., CRD transitions from
// not-Established to Established). It does not trigger when a probe transitions
// away from success, as boxcutter has no reconcile action to perform in that
// case. On creation, it triggers if the probe is already successful.
// Objects whose GroupKind does not match any of the provided probes return
// false, deferring to other predicates in a predicate.Or composition.
func probeSucceededPredicate(probes ...*probing.GroupKindSelector) predicate.Predicate {
	checkProbe := func(obj client.Object, fn func(p *probing.GroupKindSelector) bool) bool {
		gk := obj.GetObjectKind().GroupVersionKind().GroupKind()
		for _, p := range probes {
			if p.GroupKind == gk {
				return fn(p)
			}
		}

		return false
	}

	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return checkProbe(e.Object, func(p *probing.GroupKindSelector) bool {
				return p.Probe(e.Object).Status == probing.StatusTrue
			})
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return checkProbe(e.ObjectNew, func(p *probing.GroupKindSelector) bool {
				return p.Probe(e.ObjectOld).Status != probing.StatusTrue &&
					p.Probe(e.ObjectNew).Status == probing.StatusTrue
			})
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// noGenerationPredicate returns a predicate that passes all update events for
// objects that don't have generation tracking (e.g., ConfigMaps, Secrets).
// Objects with generation tracking have it initialised to 1 on creation, so
// generation == 0 indicates the API server does not manage generation for that
// resource type. Those objects are deferred to GenerationChangedPredicate.
func noGenerationPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectNew.GetGeneration() == 0
		},
	}
}

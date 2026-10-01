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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// conditionObj builds an object of the given apiVersion and kind whose
// .status.conditions holds the supplied type/status pairs. A condition with an
// empty status is omitted, representing a condition that has not been set yet.
func conditionObj(apiVersion, kind string, conditions map[string]string) *unstructured.Unstructured {
	entries := []any{}

	for condType, status := range conditions {
		if status == "" {
			continue
		}

		entries = append(entries, map[string]any{"type": condType, "status": status})
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": "test"},
		"status":     map[string]any{"conditions": entries},
	}}
}

// compatibilityRequirement builds a CompatibilityRequirement with the given
// Admitted and Compatible condition statuses.
func compatibilityRequirement(admitted, compatible string) *unstructured.Unstructured {
	return conditionObj("apiextensions.openshift.io/v1alpha1", "CompatibilityRequirement", map[string]string{
		"Admitted":   admitted,
		"Compatible": compatible,
	})
}

// deployment builds a Deployment with the given Available condition status.
func deployment(available string) *unstructured.Unstructured {
	return conditionObj("apps/v1", "Deployment", map[string]string{"Available": available})
}

var _ = Describe("probeSucceededPredicate", func() {
	var pred = probeSucceededPredicate(allProbes()...)

	DescribeTable("update events",
		func(objOld, objNew client.Object, expected bool) {
			Expect(pred.Update(event.UpdateEvent{ObjectOld: objOld, ObjectNew: objNew})).To(Equal(expected))
		},

		// CompatibilityRequirement has two probes registered on a single
		// GroupKind, so the predicate must evaluate both rather than answering
		// with whichever it finds first.
		Entry("Admitted transitions to True",
			compatibilityRequirement("", ""),
			compatibilityRequirement("True", "False"),
			true),
		Entry("Compatible transitions to True while Admitted is already True",
			compatibilityRequirement("True", "False"),
			compatibilityRequirement("True", "True"),
			true),
		Entry("Admitted transitions to True while Compatible is already True",
			compatibilityRequirement("False", "True"),
			compatibilityRequirement("True", "True"),
			true),
		Entry("both transition to True in one update",
			compatibilityRequirement("", ""),
			compatibilityRequirement("True", "True"),
			true),
		Entry("neither transitions",
			compatibilityRequirement("False", "False"),
			compatibilityRequirement("False", "False"),
			false),
		Entry("Compatible transitions away from True",
			compatibilityRequirement("True", "True"),
			compatibilityRequirement("True", "False"),
			false),

		// A resync re-delivers the same object as both old and new, so no probe
		// can have transitioned.
		Entry("resync of a fully satisfied CompatibilityRequirement",
			compatibilityRequirement("True", "True"),
			compatibilityRequirement("True", "True"),
			false),

		// GroupKinds with a single probe are unaffected.
		Entry("Deployment becomes Available",
			deployment("False"),
			deployment("True"),
			true),
		Entry("Deployment stays Available",
			deployment("True"),
			deployment("True"),
			false),

		// Objects we hold no probe for defer to the other predicates in the
		// predicate.Or composition.
		Entry("unprobed GroupKind",
			conditionObj("v1", "ConfigMap", nil),
			conditionObj("v1", "ConfigMap", nil),
			false),
	)

	DescribeTable("create events",
		func(obj client.Object, expected bool) {
			Expect(pred.Create(event.CreateEvent{Object: obj})).To(Equal(expected))
		},

		Entry("CompatibilityRequirement created with both conditions True",
			compatibilityRequirement("True", "True"), true),
		Entry("CompatibilityRequirement created with only Compatible True",
			compatibilityRequirement("False", "True"), true),
		Entry("CompatibilityRequirement created with no conditions",
			compatibilityRequirement("", ""), false),
		Entry("unprobed GroupKind",
			conditionObj("v1", "ConfigMap", nil), false),
	)
})

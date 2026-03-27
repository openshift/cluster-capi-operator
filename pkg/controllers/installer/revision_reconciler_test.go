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
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

// errorInjectingRESTMapper wraps a real RESTMapper and injects errors for specific GVKs.
// This allows testing error handling while using a real RESTMapper for normal cases.
type errorInjectingRESTMapper struct {
	meta.RESTMapper
	errorGKs sets.Set[schema.GroupKind] // Return generic error for these
}

func (e *errorInjectingRESTMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	if e.errorGKs.Has(gk) {
		return nil, fmt.Errorf("simulated RESTMapper error")
	}

	// Delegate to real RESTMapper
	return e.RESTMapper.RESTMapping(gk, versions...)
}

var _ = Describe("revisionReconciler.resolveCollectedObjects", func() {
	It("returns terminal error when RESTMapper returns NoMatchError", func() {
		// Create wrapper that returns NoMatchError for a nonexistent GVK
		unknownGK := schema.GroupKind{Group: "nonexistent.example.com", Kind: "FakeResource"}
		wrappedMapper := &errorInjectingRESTMapper{
			RESTMapper: cl.RESTMapper(), // Real envtest RESTMapper
			errorGKs:   sets.New[schema.GroupKind](),
		}

		r := &revisionReconciler{
			InstallerController: &InstallerController{
				restMapper: wrappedMapper,
			},
			log: test.NewVerboseGinkgoLogger(0),
			collectedNonNSObjects: sets.New(collectedObjectRef{
				gvk:  unknownGK.WithVersion("v1"),
				name: "test-object",
			}),
			crdGKResourceMapping: make(map[schema.GroupKind]string),
			relatedObjects:       sets.New[configv1.ObjectReference](),
		}

		err := r.resolveCollectedObjects()

		// Verify the error is terminal
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeTrue(), "expected terminal error")
		Expect(err.Error()).To(ContainSubstring("non-existent resource type"))
		Expect(err.Error()).To(ContainSubstring("nonexistent.example.com"))
		Expect(err.Error()).To(ContainSubstring("FakeResource"))
	})

	It("returns transient error when RESTMapper returns other errors", func() {
		// Create wrapper that returns a generic error for a specific GVK
		failingGK := schema.GroupKind{Group: "failing.example.com", Kind: "ProblematicResource"}
		wrappedMapper := &errorInjectingRESTMapper{
			RESTMapper: cl.RESTMapper(), // Real envtest RESTMapper
			errorGKs:   sets.New(failingGK),
		}

		r := &revisionReconciler{
			InstallerController: &InstallerController{
				restMapper: wrappedMapper,
			},
			log: test.NewVerboseGinkgoLogger(0),
			collectedNonNSObjects: sets.New(collectedObjectRef{
				gvk:  failingGK.WithVersion("v1"),
				name: "test-object",
			}),
			crdGKResourceMapping: make(map[schema.GroupKind]string),
			relatedObjects:       sets.New[configv1.ObjectReference](),
		}

		err := r.resolveCollectedObjects()

		// Verify the error is NOT terminal (transient/ephemeral)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeFalse(), "expected transient error")
		Expect(err.Error()).To(ContainSubstring("failed to resolve"))
	})

	It("uses CRD resource name without calling RESTMapper", func() {
		// Create wrapper that would return NoMatchError if called
		crdGK := schema.GroupKind{Group: "custom.example.com", Kind: "Widget"}
		wrappedMapper := &errorInjectingRESTMapper{
			RESTMapper: cl.RESTMapper(),
			errorGKs:   sets.New[schema.GroupKind](),
		}

		r := &revisionReconciler{
			InstallerController: &InstallerController{
				restMapper: wrappedMapper,
			},
			log: test.NewVerboseGinkgoLogger(0),
			collectedNonNSObjects: sets.New(collectedObjectRef{
				gvk:  crdGK.WithVersion("v1"),
				name: "test-widget",
			}),
			crdGKResourceMapping: map[schema.GroupKind]string{
				crdGK: "widgets", // CRD resource name from spec.names.plural
			},
			relatedObjects: sets.New[configv1.ObjectReference](),
		}

		err := r.resolveCollectedObjects()

		// Should succeed because CRD resource name is used directly (no RESTMapper call)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.relatedObjects.Has(configv1.ObjectReference{
			Group:    "custom.example.com",
			Resource: "widgets", // From crdGKResourceMapping, not RESTMapper
			Name:     "test-widget",
		})).To(BeTrue())
	})

	It("resolves real resource types using envtest RESTMapper", func() {
		// No error injection - test that real RESTMapper works for known types
		wrappedMapper := &errorInjectingRESTMapper{
			RESTMapper: cl.RESTMapper(),
			errorGKs:   sets.New[schema.GroupKind](),
		}

		// Use a real Kubernetes type that exists in envtest
		clusterRoleGK := schema.GroupKind{Group: "rbac.authorization.k8s.io", Kind: "ClusterRole"}

		r := &revisionReconciler{
			InstallerController: &InstallerController{
				restMapper: wrappedMapper,
			},
			log: test.NewVerboseGinkgoLogger(0),
			collectedNonNSObjects: sets.New(collectedObjectRef{
				gvk:  clusterRoleGK.WithVersion("v1"),
				name: "test-clusterrole",
			}),
			crdGKResourceMapping: make(map[schema.GroupKind]string),
			relatedObjects:       sets.New[configv1.ObjectReference](),
		}

		err := r.resolveCollectedObjects()

		// Should succeed and resolve to correct resource name
		Expect(err).NotTo(HaveOccurred())
		Expect(r.relatedObjects.Has(configv1.ObjectReference{
			Group:    "rbac.authorization.k8s.io",
			Resource: "clusterroles", // Resolved via RESTMapper
			Name:     "test-clusterrole",
		})).To(BeTrue())
	})
})

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

package manifesttransformer

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pkg.package-operator.run/boxcutter"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

func TestValidateTransformers(t *testing.T) {
	const componentName = "my-component"

	testObj := unstructured.Unstructured{}
	testObj.SetName("my-obj")

	testObj2 := unstructured.Unstructured{}
	testObj2.SetName("my-obj2")

	t.Run("nil transformers returns no error", func(t *testing.T) {
		g := NewWithT(t)
		rev := &fakeRevision{
			components: []revisiongenerator.RenderedComponent{
				&fakeComponent{name: componentName, objects: []*unstructured.Unstructured{&testObj}},
			},
		}
		g.Expect(ValidateTransformers(nil, rev)).To(Succeed())
	})

	t.Run("empty transformers returns no error", func(t *testing.T) {
		g := NewWithT(t)
		rev := &fakeRevision{
			components: []revisiongenerator.RenderedComponent{
				&fakeComponent{name: componentName, objects: []*unstructured.Unstructured{&testObj}},
			},
		}
		g.Expect(ValidateTransformers([]ManifestTransformer{}, rev)).To(Succeed())
	})

	t.Run("validates Objects and includes component name in error", func(t *testing.T) {
		g := NewWithT(t)
		rev := &fakeRevision{
			components: []revisiongenerator.RenderedComponent{
				&fakeComponent{name: componentName, objects: []*unstructured.Unstructured{&testObj}},
			},
		}
		stub := &stubTransformer{validateErr: errors.New("obj invalid")}
		g.Expect(ValidateTransformers([]ManifestTransformer{stub}, rev)).
			To(MatchError(SatisfyAll(
				ContainSubstring(componentName),
				ContainSubstring("my-obj"),
				ContainSubstring("obj invalid"),
			)))
	})

	t.Run("collects errors from multiple objects", func(t *testing.T) {
		g := NewWithT(t)
		rev := &fakeRevision{
			components: []revisiongenerator.RenderedComponent{
				&fakeComponent{name: componentName, objects: []*unstructured.Unstructured{&testObj, &testObj2}},
			},
		}
		stub := &stubTransformer{validateErr: errors.New("invalid")}
		g.Expect(ValidateTransformers([]ManifestTransformer{stub}, rev)).
			To(MatchError(SatisfyAll(
				ContainSubstring("my-obj"),
				ContainSubstring("my-obj2"),
			)))
	})

	t.Run("aggregates errors across multiple components rather than stopping at the first", func(t *testing.T) {
		g := NewWithT(t)

		objA := &unstructured.Unstructured{}
		objA.SetName("obj-a")

		objB := &unstructured.Unstructured{}
		objB.SetName("obj-b")

		rev := &fakeRevision{
			components: []revisiongenerator.RenderedComponent{
				&fakeComponent{name: "comp-1", objects: []*unstructured.Unstructured{objA}},
				&fakeComponent{name: "comp-2", objects: []*unstructured.Unstructured{objB}},
			},
		}
		stub := &stubTransformer{validateErr: errors.New("invalid")}

		err := ValidateTransformers([]ManifestTransformer{stub}, rev)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("comp-1"))
		g.Expect(err.Error()).To(ContainSubstring("obj-a"))
		g.Expect(err.Error()).To(ContainSubstring("comp-2"))
		g.Expect(err.Error()).To(ContainSubstring("obj-b"))
	})

	t.Run("aggregates errors from multiple transformers on the same object", func(t *testing.T) {
		g := NewWithT(t)

		rev := &fakeRevision{
			components: []revisiongenerator.RenderedComponent{
				&fakeComponent{name: componentName, objects: []*unstructured.Unstructured{&testObj}},
			},
		}
		stubA := &stubTransformer{validateErr: errors.New("error-a")}
		stubB := &stubTransformer{validateErr: errors.New("error-b")}

		err := ValidateTransformers([]ManifestTransformer{stubA, stubB}, rev)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("error-a"))
		g.Expect(err.Error()).To(ContainSubstring("error-b"))
	})
}

// stubTransformer is a test double for ManifestTransformer.
type stubTransformer struct {
	validateErr error
}

func (s *stubTransformer) TransformObject(_ context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
	return obj, nil, nil
}

func (s *stubTransformer) Validate(_ *unstructured.Unstructured) error {
	return s.validateErr
}

func (s *stubTransformer) WithRevision(_ context.Context, _ revisiongenerator.RenderedRevision) ManifestTransformer {
	return s
}

func (s *stubTransformer) WithComponent(_ context.Context, _ revisiongenerator.RenderedComponent) ManifestTransformer {
	return s
}

var _ ManifestTransformer = &stubTransformer{}

// fakeComponent implements revisiongenerator.RenderedComponent for unit tests
// that need a revision without running the full revision generator.
type fakeComponent struct {
	name    string
	objects []*unstructured.Unstructured
}

func (f *fakeComponent) Name() string                          { return f.name }
func (f *fakeComponent) Objects() []*unstructured.Unstructured { return f.objects }

// fakeRevision implements revisiongenerator.RenderedRevision for unit tests.
type fakeRevision struct {
	components []revisiongenerator.RenderedComponent
}

func (f *fakeRevision) ContentID() (string, error) { return "fake-content-id", nil }
func (f *fakeRevision) Components() []revisiongenerator.RenderedComponent {
	return f.components
}
func (f *fakeRevision) ForInstall(string, int64) (revisiongenerator.InstallerRevision, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeRevision) ManifestSubstitutions() map[string]string { return nil }

var _ revisiongenerator.RenderedRevision = &fakeRevision{}

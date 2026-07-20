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

package runtimetransformer

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pkg.package-operator.run/boxcutter"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

func TestValidateTransformers(t *testing.T) {
	const componentName = "my-component"

	crdObj := &unstructured.Unstructured{}
	crdObj.SetName("my-crd")

	regularObj := &unstructured.Unstructured{}
	regularObj.SetName("my-obj")

	t.Run("nil transformers returns no error", func(t *testing.T) {
		g := NewWithT(t)
		rev := &fakeRevision{
			components: []revisiongenerator.RenderedComponent{
				&fakeComponent{name: componentName, crds: []client.Object{crdObj}, objects: []client.Object{regularObj}},
			},
		}
		g.Expect(ValidateTransformers(nil, rev)).To(Succeed())
	})

	t.Run("empty transformers returns no error", func(t *testing.T) {
		g := NewWithT(t)
		rev := &fakeRevision{
			components: []revisiongenerator.RenderedComponent{
				&fakeComponent{name: componentName, crds: []client.Object{crdObj}, objects: []client.Object{regularObj}},
			},
		}
		g.Expect(ValidateTransformers([]RuntimeTransformer{}, rev)).To(Succeed())
	})

	t.Run("validates CRDs and includes component name in error", func(t *testing.T) {
		g := NewWithT(t)
		rev := &fakeRevision{
			components: []revisiongenerator.RenderedComponent{
				&fakeComponent{name: componentName, crds: []client.Object{crdObj}},
			},
		}
		stub := NewSimpleRuntimeTransformer(&stubTransformer{validateErr: errors.New("crd invalid")})
		g.Expect(ValidateTransformers([]RuntimeTransformer{stub}, rev)).
			To(MatchError(SatisfyAll(
				ContainSubstring(componentName),
				ContainSubstring("my-crd"),
				ContainSubstring("crd invalid"),
			)))
	})

	t.Run("validates Objects and includes component name in error", func(t *testing.T) {
		g := NewWithT(t)
		rev := &fakeRevision{
			components: []revisiongenerator.RenderedComponent{
				&fakeComponent{name: componentName, objects: []client.Object{regularObj}},
			},
		}
		stub := NewSimpleRuntimeTransformer(&stubTransformer{validateErr: errors.New("obj invalid")})
		g.Expect(ValidateTransformers([]RuntimeTransformer{stub}, rev)).
			To(MatchError(SatisfyAll(
				ContainSubstring(componentName),
				ContainSubstring("my-obj"),
				ContainSubstring("obj invalid"),
			)))
	})

	t.Run("collects errors from both CRDs and Objects", func(t *testing.T) {
		g := NewWithT(t)
		rev := &fakeRevision{
			components: []revisiongenerator.RenderedComponent{
				&fakeComponent{name: componentName, crds: []client.Object{crdObj}, objects: []client.Object{regularObj}},
			},
		}
		stub := NewSimpleRuntimeTransformer(&stubTransformer{validateErr: errors.New("invalid")})
		g.Expect(ValidateTransformers([]RuntimeTransformer{stub}, rev)).
			To(MatchError(SatisfyAll(
				ContainSubstring("my-crd"),
				ContainSubstring("my-obj"),
			)))
	})
}

// stubTransformer is a test double for RuntimeTransformer.
type stubTransformer struct {
	validateErr error
}

func (s *stubTransformer) TransformObject(_ context.Context, _ client.Object) ([]boxcutter.PhaseReconcileOption, error) {
	return nil, nil
}

func (s *stubTransformer) Validate(_ client.Object) error {
	return s.validateErr
}

var _ SimpleRuntimeTransformer = &stubTransformer{}

// fakeComponent implements revisiongenerator.RenderedComponent for unit tests
// that need a revision without running the full revision generator.
type fakeComponent struct {
	name    string
	crds    []client.Object
	objects []client.Object
}

func (f *fakeComponent) Name() string             { return f.name }
func (f *fakeComponent) CRDs() []client.Object    { return f.crds }
func (f *fakeComponent) Objects() []client.Object { return f.objects }

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

var _ revisiongenerator.RenderedRevision = &fakeRevision{}

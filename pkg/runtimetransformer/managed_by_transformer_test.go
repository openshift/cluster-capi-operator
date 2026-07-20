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
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// fakeRenderedComponent is a minimal RenderedComponent for ManagedByTransformer tests.
type fakeRenderedComponent struct {
	name string
}

func (f *fakeRenderedComponent) Name() string             { return f.name }
func (f *fakeRenderedComponent) Objects() []client.Object { return nil }

func TestManagedByTransformerTransformObject(t *testing.T) {
	ctx := context.Background()

	t.Run("adds managed-by label to object with no labels", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewManagedByTransformer().
			WithComponent(ctx, &fakeRenderedComponent{name: "my-provider"})

		obj := &unstructured.Unstructured{}
		obj.SetName("test-obj")

		_, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(obj.GetLabels()).To(HaveKeyWithValue(ManagedLabelKey, "my-provider"))
	})

	t.Run("preserves existing labels", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewManagedByTransformer().
			WithComponent(ctx, &fakeRenderedComponent{name: "my-provider"})

		obj := &unstructured.Unstructured{}
		obj.SetLabels(map[string]string{"existing": "label"})

		_, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(obj.GetLabels()).To(HaveKeyWithValue("existing", "label"))
		g.Expect(obj.GetLabels()).To(HaveKeyWithValue(ManagedLabelKey, "my-provider"))
	})

	t.Run("uses component name from WithComponent", func(t *testing.T) {
		g := NewWithT(t)

		base := NewManagedByTransformer()

		tfm1 := base.WithComponent(ctx, &fakeRenderedComponent{name: "component-one"})
		tfm2 := base.WithComponent(ctx, &fakeRenderedComponent{name: "component-two"})

		obj1 := &unstructured.Unstructured{}
		obj2 := &unstructured.Unstructured{}

		_, err := tfm1.TransformObject(ctx, obj1)
		g.Expect(err).NotTo(HaveOccurred())

		_, err = tfm2.TransformObject(ctx, obj2)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(obj1.GetLabels()).To(HaveKeyWithValue(ManagedLabelKey, "component-one"))
		g.Expect(obj2.GetLabels()).To(HaveKeyWithValue(ManagedLabelKey, "component-two"))
	})
}

func TestManagedByTransformerWithRevision(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()
	tfm := NewManagedByTransformer()
	same := tfm.WithRevision(ctx, nil)

	// WithRevision is a no-op: returns the same receiver.
	g.Expect(same).To(BeIdenticalTo(tfm))
}

func TestManagedByTransformerValidate(t *testing.T) {
	g := NewWithT(t)

	tfm := NewManagedByTransformer()
	g.Expect(tfm.Validate(&unstructured.Unstructured{})).To(Succeed())
}

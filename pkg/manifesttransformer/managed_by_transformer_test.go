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
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestManagedByTransformer_TransformObject(t *testing.T) {
	ctx := context.Background()

	t.Run("adds managed-by label to object with no labels", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewManagedByTransformer().
			WithComponent(ctx, &fakeComponent{name: "my-provider"})

		obj := &unstructured.Unstructured{}
		obj.SetName("test-obj")

		transformed, opts, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(opts).To(BeNil())
		g.Expect(transformed.GetLabels()).To(HaveKeyWithValue(ManagedLabelKey, "my-provider"))

		// The original object must not be mutated.
		g.Expect(obj.GetLabels()).To(BeEmpty())
	})

	t.Run("preserves existing labels", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewManagedByTransformer().
			WithComponent(ctx, &fakeComponent{name: "my-provider"})

		obj := &unstructured.Unstructured{}
		obj.SetLabels(map[string]string{"existing": "label"})

		transformed, _, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(transformed.GetLabels()).To(HaveKeyWithValue("existing", "label"))
		g.Expect(transformed.GetLabels()).To(HaveKeyWithValue(ManagedLabelKey, "my-provider"))
	})

	t.Run("uses component name from WithComponent", func(t *testing.T) {
		g := NewWithT(t)

		base := NewManagedByTransformer()

		tfm1 := base.WithComponent(ctx, &fakeComponent{name: "component-one"})
		tfm2 := base.WithComponent(ctx, &fakeComponent{name: "component-two"})

		obj1 := &unstructured.Unstructured{}
		obj2 := &unstructured.Unstructured{}

		transformed1, _, err := tfm1.TransformObject(ctx, obj1)
		g.Expect(err).NotTo(HaveOccurred())

		transformed2, _, err := tfm2.TransformObject(ctx, obj2)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(transformed1.GetLabels()).To(HaveKeyWithValue(ManagedLabelKey, "component-one"))
		g.Expect(transformed2.GetLabels()).To(HaveKeyWithValue(ManagedLabelKey, "component-two"))
	})
}

func TestManagedByTransformer_WithRevision(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()
	tfm := NewManagedByTransformer()
	same := tfm.WithRevision(ctx, nil)

	// WithRevision is a no-op: returns the same receiver.
	g.Expect(same).To(BeIdenticalTo(tfm))
}

func TestManagedByTransformer_Validate(t *testing.T) {
	g := NewWithT(t)

	tfm := NewManagedByTransformer()
	g.Expect(tfm.Validate(&unstructured.Unstructured{})).To(Succeed())
}

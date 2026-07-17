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
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

func adoptTestObject(name string, annotations map[string]string) client.Object {
	obj := &unstructured.Unstructured{}
	obj.SetName(name)

	if len(annotations) > 0 {
		obj.SetAnnotations(annotations)
	}

	return obj
}

func TestAdoptExistingTransformer_TransformObject(t *testing.T) {
	ctx := context.Background()
	transformer := &AdoptExistingTransformer{}

	t.Run("object without annotation returns nil options and is unchanged", func(t *testing.T) {
		g := NewWithT(t)
		obj := adoptTestObject("no-annotation", nil)

		opts, err := transformer.TransformObject(ctx, obj)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(opts).To(BeNil())
		g.Expect(obj.GetAnnotations()).NotTo(HaveKey(revisiongenerator.AdoptExistingAnnotation))
	})

	t.Run("object with always: annotation stripped and CollisionProtectionNone option returned", func(t *testing.T) {
		g := NewWithT(t)
		obj := adoptTestObject("adopt-always", map[string]string{
			revisiongenerator.AdoptExistingAnnotation: revisiongenerator.AdoptExistingAlways,
		})

		opts, err := transformer.TransformObject(ctx, obj)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(opts).To(HaveLen(1))
		g.Expect(obj.GetAnnotations()).NotTo(HaveKey(revisiongenerator.AdoptExistingAnnotation))
	})

	t.Run("object with never: annotation stripped and nil options returned", func(t *testing.T) {
		g := NewWithT(t)
		obj := adoptTestObject("adopt-never", map[string]string{
			revisiongenerator.AdoptExistingAnnotation: revisiongenerator.AdoptExistingNever,
		})

		opts, err := transformer.TransformObject(ctx, obj)

		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(opts).To(BeNil())
		g.Expect(obj.GetAnnotations()).NotTo(HaveKey(revisiongenerator.AdoptExistingAnnotation))
	})
}

func TestAdoptExistingTransformer_Validate(t *testing.T) {
	transformer := &AdoptExistingTransformer{}

	t.Run("object without annotation returns nil", func(t *testing.T) {
		g := NewWithT(t)
		g.Expect(transformer.Validate(adoptTestObject("no-annotation", nil))).To(Succeed())
	})

	t.Run("object with always returns nil", func(t *testing.T) {
		g := NewWithT(t)
		obj := adoptTestObject("valid-always", map[string]string{
			revisiongenerator.AdoptExistingAnnotation: revisiongenerator.AdoptExistingAlways,
		})
		g.Expect(transformer.Validate(obj)).To(Succeed())
	})

	t.Run("object with never returns nil", func(t *testing.T) {
		g := NewWithT(t)
		obj := adoptTestObject("valid-never", map[string]string{
			revisiongenerator.AdoptExistingAnnotation: revisiongenerator.AdoptExistingNever,
		})
		g.Expect(transformer.Validate(obj)).To(Succeed())
	})

	t.Run("object with invalid value returns error wrapping ErrInvalidAdoptExistingAnnotation", func(t *testing.T) {
		g := NewWithT(t)
		obj := adoptTestObject("bad-annotation", map[string]string{
			revisiongenerator.AdoptExistingAnnotation: "bogus",
		})
		err := transformer.Validate(obj)
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.Is(err, ErrInvalidAdoptExistingAnnotation)).To(BeTrue())
		g.Expect(err).To(MatchError(ContainSubstring("bogus")))
	})
}

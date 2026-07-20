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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

// fakeRevisionWithSubs is a minimal RenderedRevision for EnvsubstTransformer tests.
type fakeRevisionWithSubs struct {
	subs map[string]string
}

func (f *fakeRevisionWithSubs) ContentID() (string, error)                        { return "fake", nil }
func (f *fakeRevisionWithSubs) Components() []revisiongenerator.RenderedComponent { return nil }
func (f *fakeRevisionWithSubs) ForInstall(string, int64) (revisiongenerator.InstallerRevision, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeRevisionWithSubs) ManifestSubstitutions() map[string]string {
	out := make(map[string]string, len(f.subs))
	for k, v := range f.subs {
		out[k] = v
	}

	return out
}

func makeUnstructured(data map[string]interface{}) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.Object = data

	return u
}

func TestEnvsubstTransformerTransformObject(t *testing.T) {
	ctx := context.Background()

	t.Run("expands string values using merged substitutions", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(nil)
		tfm2 := tfm.WithRevision(ctx, &fakeRevisionWithSubs{subs: map[string]string{"FOO": "bar"}})

		obj := makeUnstructured(map[string]interface{}{
			"spec": map[string]interface{}{
				"value": "${FOO}",
			},
		})

		_, err := tfm2.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(obj.Object["spec"].(map[string]interface{})["value"]).To(Equal("bar"))
	})

	t.Run("expands strings in nested maps", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(nil).
			WithRevision(ctx, &fakeRevisionWithSubs{subs: map[string]string{"K": "v"}})

		obj := makeUnstructured(map[string]interface{}{
			"a": map[string]interface{}{
				"b": map[string]interface{}{
					"c": "${K}",
				},
			},
		})

		_, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(obj.Object["a"].(map[string]interface{})["b"].(map[string]interface{})["c"]).To(Equal("v"))
	})

	t.Run("expands strings inside slices", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(nil).
			WithRevision(ctx, &fakeRevisionWithSubs{subs: map[string]string{"X": "hello"}})

		obj := makeUnstructured(map[string]interface{}{
			"items": []interface{}{"${X}", "literal"},
		})

		_, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())

		items := obj.Object["items"].([]interface{})
		g.Expect(items[0]).To(Equal("hello"))
		g.Expect(items[1]).To(Equal("literal"))
	})

	t.Run("expands strings in maps nested inside slices", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(nil).
			WithRevision(ctx, &fakeRevisionWithSubs{subs: map[string]string{"Y": "world"}})

		obj := makeUnstructured(map[string]interface{}{
			"containers": []interface{}{
				map[string]interface{}{"name": "${Y}"},
			},
		})

		_, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())

		containers := obj.Object["containers"].([]interface{})
		g.Expect(containers[0].(map[string]interface{})["name"]).To(Equal("world"))
	})

	t.Run("leaves non-string values unchanged", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(nil).
			WithRevision(ctx, &fakeRevisionWithSubs{subs: map[string]string{"X": "x"}})

		obj := makeUnstructured(map[string]interface{}{
			"replicas": int64(3),
			"enabled":  true,
		})

		_, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(obj.Object["replicas"]).To(Equal(int64(3)))
		g.Expect(obj.Object["enabled"]).To(Equal(true))
	})

	t.Run("unknown variable replaced with empty string", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(nil).
			WithRevision(ctx, &fakeRevisionWithSubs{subs: nil})

		obj := makeUnstructured(map[string]interface{}{
			"val": "${UNKNOWN}",
		})

		_, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(obj.Object["val"]).To(Equal(""))
	})

	t.Run("default value syntax works when variable is unset", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(nil).
			WithRevision(ctx, &fakeRevisionWithSubs{subs: nil})

		obj := makeUnstructured(map[string]interface{}{
			"val": "${MY_VAR:-fallback}",
		})

		_, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(obj.Object["val"]).To(Equal("fallback"))
	})

	t.Run("returns error for non-Unstructured object", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(nil)
		_, err := tfm.TransformObject(ctx, &corev1.ConfigMap{})
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("EnvsubstTransformer"))
	})
}

func TestEnvsubstTransformerWithRevision(t *testing.T) {
	ctx := context.Background()

	t.Run("static subs take precedence over revision subs", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(map[string]string{"VAR": "static"}).
			WithRevision(ctx, &fakeRevisionWithSubs{subs: map[string]string{"VAR": "revision"}})

		obj := makeUnstructured(map[string]interface{}{"val": "${VAR}"})

		_, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(obj.Object["val"]).To(Equal("static"))
	})

	t.Run("revision subs used when no static sub for key", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(map[string]string{"A": "from-static"}).
			WithRevision(ctx, &fakeRevisionWithSubs{subs: map[string]string{"B": "from-revision"}})

		obj := makeUnstructured(map[string]interface{}{
			"a": "${A}",
			"b": "${B}",
		})

		_, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(obj.Object["a"]).To(Equal("from-static"))
		g.Expect(obj.Object["b"]).To(Equal("from-revision"))
	})

	t.Run("WithComponent is a no-op", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(map[string]string{"V": "x"}).
			WithRevision(ctx, &fakeRevisionWithSubs{subs: nil})

		same := tfm.WithComponent(ctx, nil)
		g.Expect(same).To(BeIdenticalTo(tfm))
	})
}

func TestEnvsubstTransformerValidate(t *testing.T) {
	g := NewWithT(t)

	tfm := NewEnvsubstTransformer(nil)
	g.Expect(tfm.Validate(&corev1.ConfigMap{})).To(Succeed())
}

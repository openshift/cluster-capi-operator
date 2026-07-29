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

var _ revisiongenerator.RenderedRevision = &fakeRevisionWithSubs{}

type anyMap = map[string]interface{}

func envsubstTestObject(data anyMap) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.Object = data

	return obj
}

func TestEnvsubstTransformer_TransformObject(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name         string
		staticSubs   map[string]string
		revisionSubs map[string]string
		input        anyMap
		want         anyMap
	}{
		{
			name:         "expands string values using merged substitutions",
			revisionSubs: map[string]string{"FOO": "bar"},
			input: anyMap{
				"spec": anyMap{
					"value": "${FOO}",
				},
			},
			want: anyMap{
				"spec": anyMap{
					"value": "bar",
				},
			},
		},
		{
			name:         "expands strings in nested maps",
			revisionSubs: map[string]string{"K": "v"},
			input: anyMap{
				"a": anyMap{
					"b": anyMap{
						"c": "${K}",
					},
				},
			},
			want: anyMap{
				"a": anyMap{
					"b": anyMap{
						"c": "v",
					},
				},
			},
		},
		{
			name:         "expands strings inside slices",
			revisionSubs: map[string]string{"X": "hello"},
			input: anyMap{
				"items": []interface{}{"${X}", "literal"},
			},
			want: anyMap{
				"items": []interface{}{"hello", "literal"},
			},
		},
		{
			name:         "expands strings in maps nested inside slices",
			revisionSubs: map[string]string{"Y": "world"},
			input: anyMap{
				"containers": []interface{}{anyMap{"name": "${Y}"}},
			},
			want: anyMap{
				"containers": []interface{}{anyMap{"name": "world"}},
			},
		},
		{
			name:         "leaves non-string values unchanged",
			revisionSubs: map[string]string{"X": "x"},
			input: anyMap{
				"replicas": int64(3),
				"enabled":  true,
			},
			want: anyMap{
				"replicas": int64(3),
				"enabled":  true,
			},
		},
		{
			name:  "unknown variable replaced with empty string",
			input: anyMap{"val": "${UNKNOWN}"},
			want:  anyMap{"val": ""},
		},
		{
			name:  "default value syntax works when variable is unset",
			input: anyMap{"val": "${MY_VAR:-fallback}"},
			want:  anyMap{"val": "fallback"},
		},
		{
			name:         "static subs take precedence over revision subs",
			staticSubs:   map[string]string{"VAR": "static"},
			revisionSubs: map[string]string{"VAR": "revision"},
			input:        anyMap{"val": "${VAR}"},
			want:         anyMap{"val": "static"},
		},
		{
			name:         "revision subs used when no static sub for key",
			staticSubs:   map[string]string{"A": "from-static"},
			revisionSubs: map[string]string{"B": "from-revision"},
			input: anyMap{
				"a": "${A}",
				"b": "${B}",
			},
			want: anyMap{
				"a": "from-static",
				"b": "from-revision",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			tfm := NewEnvsubstTransformer(tc.staticSubs).
				WithRevision(ctx, &fakeRevisionWithSubs{subs: tc.revisionSubs})

			obj := envsubstTestObject(tc.input)

			transformed, opts, err := tfm.TransformObject(ctx, obj)

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(opts).To(BeNil())
			g.Expect(transformed.Object).To(Equal(tc.want))
		})
	}

	t.Run("does not mutate the original object", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(nil).
			WithRevision(ctx, &fakeRevisionWithSubs{subs: map[string]string{"FOO": "bar"}})

		obj := envsubstTestObject(anyMap{"val": "${FOO}"})

		_, _, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(obj.Object["val"]).To(Equal("${FOO}"))
	})

	t.Run("does not panic when revision has no substitutions and static subs are set", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(map[string]string{"A": "static"}).
			WithRevision(ctx, &fakeRevisionWithSubs{subs: nil})

		obj := envsubstTestObject(anyMap{"a": "${A}"})

		transformed, _, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(transformed.Object["a"]).To(Equal("static"))
	})

	t.Run("object with no substitutions configured expands to empty string", func(t *testing.T) {
		g := NewWithT(t)

		tfm := NewEnvsubstTransformer(nil)

		obj := envsubstTestObject(anyMap{"val": "${UNSET}"})

		transformed, _, err := tfm.TransformObject(ctx, obj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(transformed.Object["val"]).To(Equal(""))
	})
}

func TestEnvsubstTransformer_WithComponent(t *testing.T) {
	g := NewWithT(t)

	ctx := context.Background()
	tfm := NewEnvsubstTransformer(map[string]string{"V": "x"}).
		WithRevision(ctx, &fakeRevisionWithSubs{subs: nil})

	g.Expect(tfm.WithComponent(ctx, nil)).To(BeIdenticalTo(tfm))
}

func TestEnvsubstTransformer_Validate(t *testing.T) {
	g := NewWithT(t)

	tfm := NewEnvsubstTransformer(nil)
	g.Expect(tfm.Validate(&unstructured.Unstructured{})).To(Succeed())
}

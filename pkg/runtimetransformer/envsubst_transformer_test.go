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
	"maps"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

// fakeRevisionWithSubs is a minimal RenderedRevision for EnvsubstTransformer tests.
type fakeRevisionWithSubs struct {
	subs map[string]string
}

func (f *fakeRevisionWithSubs) ContentID() (string, error)                      { return "fake", nil }
func (f *fakeRevisionWithSubs) Components() []revisiongenerator.ParsedComponent { return nil }
func (f *fakeRevisionWithSubs) ForInstall(string, int64) (revisiongenerator.InstallerRevision, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeRevisionWithSubs) ManifestSubstitutions() map[string]string {
	out := make(map[string]string, len(f.subs))
	maps.Copy(out, f.subs)

	return out
}

func makeUnstructured(data map[string]interface{}) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.Object = data

	return u
}

type anyMap = map[string]any

func TestEnvsubstTransformerTransformObject(t *testing.T) {
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
				"items": []any{"${X}", "literal"},
			},
			want: anyMap{
				"items": []any{"hello", "literal"},
			},
		},
		{
			name:         "expands strings in maps nested inside slices",
			revisionSubs: map[string]string{"Y": "world"},
			input: anyMap{
				"containers": []any{anyMap{"name": "${Y}"}},
			},
			want: anyMap{
				"containers": []any{anyMap{"name": "world"}},
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

			obj := makeUnstructured(tc.input)
			_, err := tfm.TransformObject(ctx, obj)

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(obj.Object).To(Equal(tc.want))
		})
	}
}

func TestEnvsubstTransformerTransformObjectErrors(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		obj     client.Object
		wantErr []string
	}{
		{
			name:    "returns error for non-Unstructured object",
			obj:     &corev1.ConfigMap{},
			wantErr: []string{"EnvsubstTransformer"},
		},
		{
			name: "aggregates errors from multiple malformed expressions",
			obj: makeUnstructured(anyMap{
				"first":  "${UNCLOSED",
				"second": "${ALSO UNCLOSED",
			}),
			wantErr: []string{".first", ".second"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			_, err := NewEnvsubstTransformer(nil).TransformObject(ctx, tc.obj)

			g.Expect(err).To(HaveOccurred())

			for _, substr := range tc.wantErr {
				g.Expect(err.Error()).To(ContainSubstring(substr))
			}
		})
	}
}

func TestEnvsubstTransformerWithComponentIsNoop(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	tfm := NewEnvsubstTransformer(map[string]string{"V": "x"}).
		WithRevision(ctx, &fakeRevisionWithSubs{subs: nil})

	g.Expect(tfm.WithComponent(ctx, nil)).To(BeIdenticalTo(tfm))
}

func TestEnvsubstTransformerValidate(t *testing.T) {
	tests := []struct {
		name     string
		obj      client.Object
		wantErrs []string // empty means Validate should succeed
	}{
		{
			name: "accepts well-formed expressions",
			obj: makeUnstructured(anyMap{
				"simple":  "${VAR}",
				"default": "${VAR:-fallback}",
				"plain":   "no substitution",
				"nested":  anyMap{"deep": "${NESTED}"},
				"list":    []interface{}{"${ITEM}", "plain"},
			}),
		},
		{
			name:     "rejects malformed expression",
			obj:      makeUnstructured(anyMap{"mapKey": "${UNCLOSED"}),
			wantErrs: []string{".mapKey"},
		},
		{
			name: "rejects malformed expression in nested map",
			obj: makeUnstructured(anyMap{
				"nested": anyMap{
					"deep": "${UNCLOSED"},
			},
			),
			wantErrs: []string{".nested.deep"},
		},
		{
			name: "rejects malformed expression in slice",
			obj: makeUnstructured(anyMap{
				"list": []any{"${UNCLOSED"},
			}),
			wantErrs: []string{".list[0]"},
		},
		{
			name:     "rejects non-Unstructured object",
			obj:      &corev1.ConfigMap{},
			wantErrs: []string{"ConfigMap"},
		},
		{
			name: "returns all errors from multiple malformed expressions",
			obj: makeUnstructured(anyMap{
				"first":  "${UNCLOSED",
				"second": "${ALSO UNCLOSED",
			}),
			wantErrs: []string{".first", ".second"},
		},
		{
			name: "returns all errors across mixed map and slice nesting",
			obj: makeUnstructured(anyMap{
				"spec": anyMap{
					"items": []any{anyMap{
						"value": "${UNCLOSED"},
					},
				},
				"other": "${ALSO UNCLOSED",
			}),
			wantErrs: []string{".spec.items[0].value", ".other"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			err := NewEnvsubstTransformer(nil).Validate(tc.obj)

			if len(tc.wantErrs) == 0 {
				g.Expect(err).NotTo(HaveOccurred())
				return
			}

			g.Expect(err).To(HaveOccurred())

			for _, substr := range tc.wantErrs {
				g.Expect(err.Error()).To(ContainSubstring(substr))
			}
		})
	}
}

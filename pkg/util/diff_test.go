/*
Copyright 2025 Red Hat, Inc.

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
package util

import (
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

//nolint:funlen
func TestDiff_basic_operations(t *testing.T) {
	tests := []struct {
		name        string
		a           unstructured.Unstructured
		b           unstructured.Unstructured
		wantChanged bool
		want        string
	}{
		{
			name: "no diff on empty objects",
			a: unstructured.Unstructured{
				Object: map[string]any{},
			},
			b: unstructured.Unstructured{
				Object: map[string]any{},
			},
			wantChanged: false,
			want:        "",
		},
		{
			name: "diff when adding a field",
			a: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": 2,
			}},
			wantChanged: true,
			want:        ".[b]: <nil pointer> != 2",
		},
		{
			name: "diff when adding a field nested",
			a: unstructured.Unstructured{Object: map[string]any{
				"foo": map[string]any{
					"a": 1,
					"c": map[string]any{
						"d": 3,
					},
				},
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"foo": map[string]any{
					"a": 1,
					"b": 2,
					"c": map[string]any{
						"d": 4,
					},
				},
			}},
			wantChanged: true,
			want:        ".[foo].[b]: <does not have key> != 2, .[foo].[c].[d]: 3 != 4",
		},
		{
			name: "diff when removing a field",
			a: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": 2,
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
			}},
			wantChanged: true,
			want:        ".[b]: 2 != <nil pointer>",
		},
		{
			name: "diff when changing a field",
			a: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": 2,
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": 3,
			}},
			wantChanged: true,
			want:        ".[b]: 2 != 3",
		},
		{
			name: "diff when adding a entry to a list",
			a: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": []int{1, 2},
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": []int{1, 2, 3},
			}},
			wantChanged: true,
			want:        ".[b][2]: <no value> != 3",
		},
		{
			name: "diff when removing a entry from a list",
			a: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": []int{1, 2, 3},
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": []int{1, 2},
			}},
			wantChanged: true,
			want:        ".[b][2]: 3 != <no value>",
		},
		{
			name: "diff when changing a entry in a list",
			a: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": []int{1, 2, 3},
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": []int{1, 2, 4},
			}},
			wantChanged: true,
			want:        ".[b][2]: 3 != 4",
		},
		{
			name: "diff when deleting a list",
			a: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": []int{1, 2, 3},
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
			}},
			wantChanged: true,
			want:        ".[b]: [1 2 3] != <nil pointer>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			differ := newDiffer()

			diff, err := differ.Diff(&tt.a, &tt.b)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(diff.HasChanges()).To(Equal(tt.wantChanged))
			g.Expect(diff.String()).To(Equal(tt.want))
		})
	}
}

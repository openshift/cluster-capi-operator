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
)

//nolint:funlen
func TestDiff_basic_operations(t *testing.T) {
	tests := []struct {
		name        string
		a           map[string]any
		b           map[string]any
		wantChanged bool
		want        string
	}{
		{
			name:        "no diff on empty objects",
			a:           map[string]any{},
			b:           map[string]any{},
			wantChanged: false,
			want:        "",
		},
		{
			name: "diff when adding a field",
			a: map[string]any{
				"a": 1,
			},
			b: map[string]any{
				"a": 1,
				"b": 2,
			},
			wantChanged: true,
			want:        ".[b]: <does not have key> != 2",
		},
		{
			name: "diff when removing a field",
			a: map[string]any{
				"a": 1,
				"b": 2,
			},
			b: map[string]any{
				"a": 1,
			},
			wantChanged: true,
			want:        ".[b]: 2 != <does not have key>",
		},
		{
			name: "diff when changing a field",
			a: map[string]any{
				"a": 1,
				"b": 2,
			},
			b: map[string]any{
				"a": 1,
				"b": 3,
			},
			wantChanged: true,
			want:        ".[b]: 2 != 3",
		},
		{
			name: "diff when adding a entry to a list",
			a: map[string]any{
				"a": 1,
				"b": []int{1, 2},
			},
			b: map[string]any{
				"a": 1,
				"b": []int{1, 2, 3},
			},
			wantChanged: true,
			want:        ".[b][2]: <no value> != 3",
		},
		{
			name: "diff when removing a entry from a list",
			a: map[string]any{
				"a": 1,
				"b": []int{1, 2, 3},
			},
			b: map[string]any{
				"a": 1,
				"b": []int{1, 2},
			},
			wantChanged: true,
			want:        ".[b][2]: 3 != <no value>",
		},
		{
			name: "diff when changing a entry in a list",
			a: map[string]any{
				"a": 1,
				"b": []int{1, 2, 3},
			},
			b: map[string]any{
				"a": 1,
				"b": []int{1, 2, 4},
			},
			wantChanged: true,
			want:        ".[b][2]: 3 != 4",
		},
		{
			name: "diff when deleting a list",
			a: map[string]any{
				"a": 1,
				"b": []int{1, 2, 3},
			},
			b: map[string]any{
				"a": 1,
			},
			wantChanged: true,
			want:        ".[b]: [1 2 3] != <does not have key>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			differ := NewDiffer()

			diff, err := differ.Diff(tt.a, tt.b)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(diff.Changed()).To(Equal(tt.wantChanged))
			g.Expect(diff.String()).To(Equal(tt.want))
		})
	}
}

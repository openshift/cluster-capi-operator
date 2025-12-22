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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("Unit test Diff", func() {

	Describe("error cases", func() {
		It("should return an error if the objects are not of the same type", func() {
			a := &mapiv1beta1.MachineSet{}
			b := &mapiv1beta1.Machine{}
			_, err := NewDefaultDiffer().Diff(a, b)
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(ContainSubstring("objects to diff are not of the same type")))
		})
	})

	type testInput struct {
		a           unstructured.Unstructured
		b           unstructured.Unstructured
		diffOpts    []DiffOption
		wantChanged bool
		want        string
	}
	DescribeTable("basic operations", func(tt testInput) {
		differ := newDiffer(tt.diffOpts...)
		diff, err := differ.Diff(&tt.a, &tt.b)
		Expect(err).ToNot(HaveOccurred())
		Expect(diff.HasChanges()).To(Equal(tt.wantChanged))
		Expect(diff.String()).To(Equal(tt.want))
	},
		Entry("no diff on empty objects", testInput{
			a: unstructured.Unstructured{
				Object: map[string]any{},
			},
			b: unstructured.Unstructured{
				Object: map[string]any{},
			},
			wantChanged: false,
			want:        "",
		}),
		Entry("no diff on matching objects", testInput{
			a: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": 2,
				"c": map[string]any{},
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": 2,
				"c": map[string]any{},
			}},
			wantChanged: false,
			want:        "",
		}),
		Entry("diff when adding a field", testInput{
			a: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": 2,
			}},
			wantChanged: true,
			want:        ".[b]: <nil pointer> != 2",
		}),
		Entry("diff when adding a field nested", testInput{
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
		}),
		Entry("diff when removing a field", testInput{
			a: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": 2,
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
			}},
			wantChanged: true,
			want:        ".[b]: 2 != <nil pointer>",
		}),
		Entry("diff when changing a field", testInput{
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
		}),
		Entry("diff when adding a entry to a list", testInput{
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
		}),
		Entry("diff when removing a entry from a list", testInput{
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
		}),
		Entry("diff when changing a entry in a list", testInput{
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
		}),
		Entry("diff when deleting a list", testInput{
			a: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
				"b": []int{1, 2, 3},
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"a": 1,
			}},
			wantChanged: true,
			want:        ".[b]: [1 2 3] != <nil pointer>",
		}),
		Entry("no diff on matching objects with ignore fields", testInput{
			a: unstructured.Unstructured{Object: map[string]any{
				"someKey": "someValue",
				"changed": 1,
				"removed": 2,
				"nil":     map[string]any{},
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"someKey": "someValue",
				"changed": 2,
				"nil":     nil,
			}},
			diffOpts: []DiffOption{
				WithIgnoreField("changed"),
				WithIgnoreField("removed"),
				WithIgnoreField("nil"),
			},
			wantChanged: false,
			want:        "",
		}),
		Entry("no diff on matching objects with ignore fields that does not exist", testInput{
			a: unstructured.Unstructured{Object: map[string]any{
				"someKey": "someValue",
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"someKey": "someValue",
			}},
			diffOpts: []DiffOption{
				WithIgnoreField("doesnotexist"),
			},
			wantChanged: false,
			want:        "",
		}),
		Entry("diff on not matching objects with ignore fields that do not exist or are properly ignored", testInput{
			a: unstructured.Unstructured{Object: map[string]any{
				"someKey":         "someValue",
				"shouldbeignored": 1,
				"someChangedKey":  "a",
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"someKey":         "someValue",
				"shouldbeignored": 2,
				"someChangedKey":  "b",
			}},
			diffOpts: []DiffOption{
				WithIgnoreField("doesnotexist"),
				WithIgnoreField("shouldbeignored"),
			},
			wantChanged: true,
			want:        ".[someChangedKey]: a != b",
		}),
		Entry("no diff on matching objects with modifyFunc", testInput{
			a: unstructured.Unstructured{Object: map[string]any{
				"someKey": "someValue",
				"changed": 1,
				"removed": 2,
				"nil":     map[string]any{},
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"someKey": "someValue",
				"changed": 2,
				"nil":     nil,
			}},
			diffOpts: []DiffOption{
				func(d *differ) {
					d.modifyFuncs["test"] = func(obj map[string]interface{}) error { //nolint:unparam
						obj["new"] = "new"
						obj["changed"] = 3
						obj["removed"] = 3
						obj["nil"] = nil

						return nil
					}
				},
			},
			wantChanged: false,
			want:        "",
		}),
		Entry("no diff on objects having conditions in a different order", testInput{
			a: unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{

					"conditions": []any{
						map[string]any{
							"type":   "Foo",
							"status": "True",
						},
						map[string]any{
							"type":   "Bar",
							"status": "True",
						},
					},
				},
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{
					"conditions": []any{
						map[string]any{
							"type":   "Bar",
							"status": "True",
						},
						map[string]any{
							"type":   "Foo",
							"status": "True",
						},
					},
				},
			}},
			diffOpts: []DiffOption{
				WithConditionsAsMap(),
			},
			wantChanged: false,
			want:        "",
		}),
		Entry("diff on objects having conditions in a different order and one is different", testInput{
			a: unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{"conditions": []any{
					map[string]any{
						"type":   "Foo",
						"status": "False",
					},
					map[string]any{
						"type":   "Bar",
						"status": "True",
					},
				}},
			}},
			b: unstructured.Unstructured{Object: map[string]any{
				"status": map[string]any{"conditions": []any{
					map[string]any{
						"type":   "Bar",
						"status": "True",
					},
					map[string]any{
						"type":   "Foo",
						"status": "True",
					},
				}},
			}},
			diffOpts: []DiffOption{
				WithConditionsAsMap(),
			},
			wantChanged: true,
			want:        ".[status].[conditions].[type=Foo].[status]: False != True",
		}),
	)
})

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

package objectpruning

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// walkTestScenario defines a test case for the walk functions
type walkTestScenario struct {
	Schema         apiextensionsv1.JSONSchemaProps
	InputObject    map[string]interface{}
	ExpectedObject map[string]interface{}
}

// walkArrayTestScenario defines a test case for the walkUnstructuredArray function
type walkArrayTestScenario struct {
	Schema        apiextensionsv1.JSONSchemaProps
	InputArray    []interface{}
	ExpectedArray []interface{}
}

var _ = Describe("walkUnstructuredObject", func() {
	DescribeTable("field pruning scenarios",
		func(scenario walkTestScenario) {
			// Make a deep copy of the input object to avoid modifying the test data
			testObj := deepCopyMap(scenario.InputObject)

			// Call the function under test - modifies testObj in place
			walkUnstructuredObject(testObj, scenario.Schema)

			// Verify the result matches expected output
			Expect(testObj).To(Equal(scenario.ExpectedObject))
		},

		Entry("simple object with XPreserveUnknownFields=false", walkTestScenario{
			Schema: apiextensionsv1.JSONSchemaProps{
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"allowedField": {Type: "string"},
					"numberField":  {Type: "integer"},
				},
				XPreserveUnknownFields: ptr.To(false),
			},
			InputObject: map[string]interface{}{
				"allowedField": "keepThis",
				"numberField":  42,
				"unknownField": "removeThis",
				"extraField":   "alsoRemove",
			},
			ExpectedObject: map[string]interface{}{
				"allowedField": "keepThis",
				"numberField":  42,
			},
		}),

		Entry("simple object with XPreserveUnknownFields=true", walkTestScenario{
			Schema: apiextensionsv1.JSONSchemaProps{
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"allowedField": {Type: "string"},
				},
				XPreserveUnknownFields: ptr.To(true),
			},
			InputObject: map[string]interface{}{
				"allowedField": "keepThis",
				"unknownField": "alsoKeepThis",
				"extraField":   "keepThisToo",
			},
			ExpectedObject: map[string]interface{}{
				"allowedField": "keepThis",
				"unknownField": "alsoKeepThis",
				"extraField":   "keepThisToo",
			},
		}),

		Entry("nested object with mixed XPreserveUnknownFields", walkTestScenario{
			Schema: apiextensionsv1.JSONSchemaProps{
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"metadata": {
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"name": {Type: "string"},
							"labels": {
								Type:                 "object",
								AdditionalProperties: &apiextensionsv1.JSONSchemaPropsOrBool{Schema: &apiextensionsv1.JSONSchemaProps{Type: "string"}},
							},
						},
						XPreserveUnknownFields: ptr.To(false), // Metadata does not preserve unknown fields
					},
					"spec": {
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"replicas": {Type: "integer"},
						},
						XPreserveUnknownFields: ptr.To(false),
					},
				},
				XPreserveUnknownFields: ptr.To(false),
			},
			InputObject: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "test-object",
					"labels": map[string]interface{}{
						"app":     "myapp",
						"version": "v1.0",  // Should be kept (labels preserves unknown)
						"custom":  "value", // Should be kept (labels preserves unknown)
					},
					"annotations": "removeThis", // Should be removed (metadata doesn't preserve unknown)
				},
				"spec": map[string]interface{}{
					"replicas":    3,
					"unknownSpec": "removeThis", // Should be removed
				},
				"unknownRoot": "removeThis", // Should be removed
			},
			ExpectedObject: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name": "test-object",
					"labels": map[string]interface{}{
						"app":     "myapp",
						"version": "v1.0",
						"custom":  "value",
					},
				},
				"spec": map[string]interface{}{
					"replicas": 3,
				},
			},
		}),

		Entry("object with array field", walkTestScenario{
			Schema: apiextensionsv1.JSONSchemaProps{
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"items": {
						Type: "array",
						Items: &apiextensionsv1.JSONSchemaPropsOrArray{
							Schema: &apiextensionsv1.JSONSchemaProps{
								Type: "object",
								Properties: map[string]apiextensionsv1.JSONSchemaProps{
									"name":  {Type: "string"},
									"value": {Type: "string"},
								},
							},
						},
					},
				},
			},
			InputObject: map[string]interface{}{
				"items": []interface{}{
					map[string]interface{}{
						"name":         "item1",
						"value":        "value1",
						"unknownField": "removeThis",
					},
					map[string]interface{}{
						"name":           "item2",
						"value":          "value2",
						"anotherUnknown": "alsoRemove",
					},
				},
				"unknownTop": "removeThis",
			},
			ExpectedObject: map[string]interface{}{
				"items": []interface{}{
					map[string]interface{}{
						"name":  "item1",
						"value": "value1",
					},
					map[string]interface{}{
						"name":  "item2",
						"value": "value2",
					},
				},
			},
		}),

		Entry("object with no properties defined", walkTestScenario{
			Schema: apiextensionsv1.JSONSchemaProps{
				Type:                   "object",
				XPreserveUnknownFields: ptr.To(false),
				// No Properties defined
			},
			InputObject: map[string]interface{}{
				"field1": "removeThis",
				"field2": "alsoRemove",
				"field3": 42,
			},
			ExpectedObject: map[string]interface{}{},
		}),

		Entry("object with no properties but XPreserveUnknownFields=true", walkTestScenario{
			Schema: apiextensionsv1.JSONSchemaProps{
				Type:                   "object",
				XPreserveUnknownFields: ptr.To(true),
			},
			InputObject: map[string]interface{}{
				"field1": "keepThis",
				"field2": "alsoKeep",
				"field3": 42,
			},
			ExpectedObject: map[string]interface{}{
				"field1": "keepThis",
				"field2": "alsoKeep",
				"field3": 42,
			},
		}),

		Entry("complex nested scenario with arrays and objects", walkTestScenario{
			Schema: apiextensionsv1.JSONSchemaProps{
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"spec": {
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"template": {
								Type: "object",
								Properties: map[string]apiextensionsv1.JSONSchemaProps{
									"spec": {
										Type: "object",
										Properties: map[string]apiextensionsv1.JSONSchemaProps{
											"containers": {
												Type: "array",
												Items: &apiextensionsv1.JSONSchemaPropsOrArray{
													Schema: &apiextensionsv1.JSONSchemaProps{
														Type: "object",
														Properties: map[string]apiextensionsv1.JSONSchemaProps{
															"name":  {Type: "string"},
															"image": {Type: "string"},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			InputObject: map[string]interface{}{
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{
									"name":         "nginx",
									"image":        "nginx:latest",
									"unknownProp1": "removeThis",
								},
								map[string]interface{}{
									"name":         "sidecar",
									"image":        "sidecar:v1",
									"unknownProp2": "alsoRemove",
								},
							},
							"unknownSpecField": "removeThis",
						},
						"unknownTemplateField": "removeThis",
					},
					"unknownSpecRoot": "removeThis",
				},
				"unknownRoot": "removeThis",
			},
			ExpectedObject: map[string]interface{}{
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{
									"name":  "nginx",
									"image": "nginx:latest",
								},
								map[string]interface{}{
									"name":  "sidecar",
									"image": "sidecar:v1",
								},
							},
						},
					},
				},
			},
		}),
	)
})

var _ = Describe("walkUnstructuredArray", func() {
	DescribeTable("array processing scenarios",
		func(scenario walkArrayTestScenario) {
			// Make a deep copy of the input array to avoid modifying the test data
			testArray := deepCopyArray(scenario.InputArray)

			// Call the function under test - modifies testArray in place
			walkUnstructuredArray(testArray, scenario.Schema)

			// Verify the result matches expected output
			Expect(testArray).To(Equal(scenario.ExpectedArray))
		},

		Entry("arrays with object items containing unknown fields", walkArrayTestScenario{
			Schema: apiextensionsv1.JSONSchemaProps{
				Type: "array",
				Items: &apiextensionsv1.JSONSchemaPropsOrArray{
					Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"name":  {Type: "string"},
							"value": {Type: "string"},
						},
						XPreserveUnknownFields: ptr.To(false),
					},
				},
			},
			InputArray: []interface{}{
				map[string]interface{}{
					"name":         "item1",
					"value":        "value1",
					"unknownField": "removeThis",
				},
				map[string]interface{}{
					"name":           "item2",
					"value":          "value2",
					"anotherUnknown": "alsoRemove",
				},
			},
			ExpectedArray: []interface{}{
				map[string]interface{}{
					"name":  "item1",
					"value": "value1",
				},
				map[string]interface{}{
					"name":  "item2",
					"value": "value2",
				},
			},
		}),

		Entry("array schema with no items definition", walkArrayTestScenario{
			Schema: apiextensionsv1.JSONSchemaProps{
				Type: "array",
				// No Items defined
			},
			InputArray: []interface{}{
				map[string]interface{}{
					"name":    "item1",
					"unknown": "shouldStay", // Should not be modified since no schema
				},
			},
			ExpectedArray: []interface{}{
				map[string]interface{}{
					"name":    "item1",
					"unknown": "shouldStay", // Should remain unchanged
				},
			},
		}),

		Entry("array schema with nil items schema", walkArrayTestScenario{
			Schema: apiextensionsv1.JSONSchemaProps{
				Type: "array",
				Items: &apiextensionsv1.JSONSchemaPropsOrArray{
					Schema: nil, // Nil schema
				},
			},
			InputArray: []interface{}{
				map[string]interface{}{
					"name":    "item1",
					"unknown": "shouldStay",
				},
			},
			ExpectedArray: []interface{}{
				map[string]interface{}{
					"name":    "item1",
					"unknown": "shouldStay", // Should remain unchanged when items schema is nil
				},
			},
		}),
	)
})

// Helper functions for deep copying test data
func deepCopyMap(original map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{})
	for k, v := range original {
		switch value := v.(type) {
		case map[string]interface{}:
			copy[k] = deepCopyMap(value)
		case []interface{}:
			copy[k] = deepCopyArray(value)
		default:
			copy[k] = value
		}
	}
	return copy
}

func deepCopyArray(original []interface{}) []interface{} {
	copy := make([]interface{}, len(original))
	for i, v := range original {
		switch value := v.(type) {
		case map[string]interface{}:
			copy[i] = deepCopyMap(value)
		case []interface{}:
			copy[i] = deepCopyArray(value)
		default:
			copy[i] = value
		}
	}
	return copy
}

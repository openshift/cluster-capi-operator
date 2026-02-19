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

package test

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// unstructuredBuilder provides a fluent interface for building test objects
// that match CRD schemas for validation testing.
type unstructuredBuilder struct {
	obj *unstructured.Unstructured
}

// NewTestObject creates a builder for unstructured objects with the given GVK.
func NewTestObject(gvk schema.GroupVersionKind) *unstructuredBuilder {
	return &unstructuredBuilder{
		obj: &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": gvk.GroupVersion().String(),
				"kind":       gvk.Kind,
				"metadata": map[string]interface{}{
					"name":      "test-object",
					"namespace": "default",
				},
			},
		},
	}
}

// WithName sets the object name.
func (b *unstructuredBuilder) WithName(name string) *unstructuredBuilder {
	b.obj.SetName(name)
	return b
}

// WithNamespace sets the object namespace.
func (b *unstructuredBuilder) WithNamespace(namespace string) *unstructuredBuilder {
	b.obj.SetNamespace(namespace)
	return b
}

// WithSpec sets the spec field with the provided data.
func (b *unstructuredBuilder) WithSpec(spec map[string]interface{}) *unstructuredBuilder {
	b.obj.Object["spec"] = spec
	return b
}

// WithStatus sets the status field with the provided data.
func (b *unstructuredBuilder) WithStatus(status map[string]interface{}) *unstructuredBuilder {
	b.obj.Object["status"] = status
	return b
}

// WithField sets an arbitrary field in the object.
func (b *unstructuredBuilder) WithField(field string, value interface{}) *unstructuredBuilder {
	b.obj.Object[field] = value
	return b
}

// WithNestedField sets a nested field using dot notation (e.g., "spec.replicas").
func (b *unstructuredBuilder) WithNestedField(fieldPath string, value interface{}) *unstructuredBuilder {
	fields := []string{}
	current := ""

	for _, char := range fieldPath {
		if char == '.' {
			fields = append(fields, current)
			current = ""
		} else {
			current += string(char)
		}
	}

	fields = append(fields, current)

	if err := unstructured.SetNestedField(b.obj.Object, value, fields...); err != nil {
		panic(err) // This is a test utility, panicking on error is acceptable
	}

	return b
}

// Build returns a deep copy of the constructed unstructured object.
func (b *unstructuredBuilder) Build() *unstructured.Unstructured {
	return b.obj.DeepCopy()
}

// CRDSchemaBuilder provides utilities for manipulating CRD schemas for testing.
type CRDSchemaBuilder struct {
	crd *apiextensionsv1.CustomResourceDefinition
}

// NewCRDSchemaBuilder creates a builder for CRD schema manipulation.
func NewCRDSchemaBuilder() *CRDSchemaBuilder {
	baseCRD := GenerateTestCRD()
	return &CRDSchemaBuilder{crd: baseCRD}
}

// FromCRD creates a builder from an existing CRD.
func FromCRD(crd *apiextensionsv1.CustomResourceDefinition) *CRDSchemaBuilder {
	return &CRDSchemaBuilder{crd: crd.DeepCopy()}
}

// WithSchema replaces the entire OpenAPIV3Schema with the provided schema.
func (b *CRDSchemaBuilder) WithSchema(schema *apiextensionsv1.JSONSchemaProps) *CRDSchemaBuilder {
	b.crd.Spec.Versions[0].Schema.OpenAPIV3Schema = schema
	return b
}

// WithSchemaBuilder replaces the entire OpenAPIV3Schema with a schema built from JSONSchemaPropsBuilder.
func (b *CRDSchemaBuilder) WithSchemaBuilder(schemaBuilder *JSONSchemaPropsBuilder) *CRDSchemaBuilder {
	return b.WithSchema(schemaBuilder.Build())
}

// WithProperty adds a property to the CRD's schema.
func (b *CRDSchemaBuilder) WithProperty(name string, propSchema apiextensionsv1.JSONSchemaProps) *CRDSchemaBuilder {
	schema := b.crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	if schema.Properties == nil {
		schema.Properties = make(map[string]apiextensionsv1.JSONSchemaProps)
	}

	schema.Properties[name] = propSchema

	return b
}

// WithStringProperty adds a string property to the CRD's schema.
func (b *CRDSchemaBuilder) WithStringProperty(name string) *CRDSchemaBuilder {
	return b.WithProperty(name, apiextensionsv1.JSONSchemaProps{
		Type: "string",
	})
}

// WithRequiredStringProperty adds a required string property to the CRD's schema.
func (b *CRDSchemaBuilder) WithRequiredStringProperty(name string) *CRDSchemaBuilder {
	b.WithStringProperty(name)
	b.WithRequiredField(name)

	return b
}

// WithIntegerProperty adds an integer property to the CRD's schema.
func (b *CRDSchemaBuilder) WithIntegerProperty(name string) *CRDSchemaBuilder {
	return b.WithProperty(name, apiextensionsv1.JSONSchemaProps{
		Type: "integer",
	})
}

// WithObjectProperty adds an object property with nested properties.
func (b *CRDSchemaBuilder) WithObjectProperty(name string, properties map[string]apiextensionsv1.JSONSchemaProps) *CRDSchemaBuilder {
	return b.WithProperty(name, apiextensionsv1.JSONSchemaProps{
		Type:       "object",
		Properties: properties,
	})
}

// WithArrayProperty adds an array property to the CRD's schema.
func (b *CRDSchemaBuilder) WithArrayProperty(name string, itemSchema apiextensionsv1.JSONSchemaProps) *CRDSchemaBuilder {
	return b.WithProperty(name, apiextensionsv1.JSONSchemaProps{
		Type: "array",
		Items: &apiextensionsv1.JSONSchemaPropsOrArray{
			Schema: &itemSchema,
		},
	})
}

// WithRequiredField adds a field to the required fields list.
func (b *CRDSchemaBuilder) WithRequiredField(fieldName string) *CRDSchemaBuilder {
	schema := b.crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	if schema.Required == nil {
		schema.Required = []string{}
	}

	schema.Required = append(schema.Required, fieldName)

	return b
}

// WithPattern adds a pattern validation to a string property.
func (b *CRDSchemaBuilder) WithPattern(propertyName, pattern string) *CRDSchemaBuilder {
	schema := b.crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	if prop, exists := schema.Properties[propertyName]; exists {
		prop.Pattern = pattern
		schema.Properties[propertyName] = prop
	}

	return b
}

// WithAdditionalProperties sets whether additional properties are allowed.
func (b *CRDSchemaBuilder) WithAdditionalProperties(allowed bool) *CRDSchemaBuilder {
	schema := b.crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	schema.AdditionalProperties = &apiextensionsv1.JSONSchemaPropsOrBool{
		Allows: allowed,
	}

	return b
}

// RemoveProperty removes a property from the CRD's schema.
func (b *CRDSchemaBuilder) RemoveProperty(name string) *CRDSchemaBuilder {
	schema := b.crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	if schema.Properties != nil {
		delete(schema.Properties, name)
	}

	return b
}

// RemoveRequiredField removes a field from the required fields list.
func (b *CRDSchemaBuilder) RemoveRequiredField(fieldName string) *CRDSchemaBuilder {
	schema := b.crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	if schema.Required != nil {
		newRequired := []string{}

		for _, field := range schema.Required {
			if field != fieldName {
				newRequired = append(newRequired, field)
			}
		}

		schema.Required = newRequired
	}

	return b
}

// Build returns a deep copy of the constructed CRD.
func (b *CRDSchemaBuilder) Build() *apiextensionsv1.CustomResourceDefinition {
	return b.crd.DeepCopy()
}

// TestObjectScenarios provides pre-built object scenarios for common test cases.

// ValidObjectForCRD creates an object that should pass validation for the given CRD.
func ValidObjectForCRD(crd *apiextensionsv1.CustomResourceDefinition) *unstructured.Unstructured {
	gvk := schema.GroupVersionKind{
		Group:   crd.Spec.Group,
		Version: crd.Spec.Versions[0].Name,
		Kind:    crd.Spec.Names.Kind,
	}

	builder := NewTestObject(gvk)

	// Add required fields based on the CRD schema
	schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	if schema.Required != nil {
		for _, requiredField := range schema.Required {
			if prop, exists := schema.Properties[requiredField]; exists {
				value := defaultValueForProperty(prop)
				builder.WithField(requiredField, value)
			}
		}
	}

	return builder.Build()
}

// InvalidObjectMissingRequiredField creates an object missing a required field.
func InvalidObjectMissingRequiredField(crd *apiextensionsv1.CustomResourceDefinition, missingField string) *unstructured.Unstructured {
	obj := ValidObjectForCRD(crd)
	delete(obj.Object, missingField)

	return obj
}

// ObjectWithExtraField creates an object with an additional field not in the schema.
func ObjectWithExtraField(crd *apiextensionsv1.CustomResourceDefinition, extraField string, value interface{}) *unstructured.Unstructured {
	obj := ValidObjectForCRD(crd)
	obj.Object[extraField] = value

	return obj
}

// defaultValueForProperty returns an appropriate default value for a given JSON schema property.
func defaultValueForProperty(prop apiextensionsv1.JSONSchemaProps) interface{} {
	switch prop.Type {
	case "string":
		return "test-value"
	case "integer":
		return int64(1)
	case "number":
		return float64(1.0)
	case "boolean":
		return true
	case "array":
		return []interface{}{}
	case "object":
		obj := map[string]interface{}{}
		// Recursively add required nested properties
		if prop.Required != nil {
			for _, requiredField := range prop.Required {
				if nestedProp, exists := prop.Properties[requiredField]; exists {
					obj[requiredField] = defaultValueForProperty(nestedProp)
				}
			}
		}

		return obj
	default:
		return "default-value"
	}
}

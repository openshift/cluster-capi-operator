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
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// JSONSchemaPropsBuilder provides a fluent interface for building JSONSchemaProps
// for use in CRD schema construction and testing.
type JSONSchemaPropsBuilder struct {
	schema *apiextensionsv1.JSONSchemaProps
}

// NewJSONSchemaPropsBuilder creates a new builder for JSONSchemaProps.
func NewJSONSchemaPropsBuilder() *JSONSchemaPropsBuilder {
	return &JSONSchemaPropsBuilder{
		schema: &apiextensionsv1.JSONSchemaProps{},
	}
}

// NewStringSchema creates a builder for a string schema.
func NewStringSchema() *JSONSchemaPropsBuilder {
	return NewJSONSchemaPropsBuilder().WithType("string")
}

// NewIntegerSchema creates a builder for an integer schema.
func NewIntegerSchema() *JSONSchemaPropsBuilder {
	return NewJSONSchemaPropsBuilder().WithType("integer")
}

// NewNumberSchema creates a builder for a number schema.
func NewNumberSchema() *JSONSchemaPropsBuilder {
	return NewJSONSchemaPropsBuilder().WithType("number")
}

// NewBooleanSchema creates a builder for a boolean schema.
func NewBooleanSchema() *JSONSchemaPropsBuilder {
	return NewJSONSchemaPropsBuilder().WithType("boolean")
}

// NewObjectSchema creates a builder for an object schema.
func NewObjectSchema() *JSONSchemaPropsBuilder {
	return NewJSONSchemaPropsBuilder().WithType("object")
}

// NewArraySchema creates a builder for an array schema.
func NewArraySchema() *JSONSchemaPropsBuilder {
	return NewJSONSchemaPropsBuilder().WithType("array")
}

// WithType sets the type field.
func (b *JSONSchemaPropsBuilder) WithType(schemaType string) *JSONSchemaPropsBuilder {
	b.schema.Type = schemaType
	return b
}

// WithFormat sets the format field (e.g., "date-time", "email").
func (b *JSONSchemaPropsBuilder) WithFormat(format string) *JSONSchemaPropsBuilder {
	b.schema.Format = format
	return b
}

// WithPattern sets a regex pattern for string validation.
func (b *JSONSchemaPropsBuilder) WithPattern(pattern string) *JSONSchemaPropsBuilder {
	b.schema.Pattern = pattern
	return b
}

// WithMinLength sets the minimum length for strings.
func (b *JSONSchemaPropsBuilder) WithMinLength(minLength int64) *JSONSchemaPropsBuilder {
	b.schema.MinLength = &minLength
	return b
}

// WithMaxLength sets the maximum length for strings.
func (b *JSONSchemaPropsBuilder) WithMaxLength(maxLength int64) *JSONSchemaPropsBuilder {
	b.schema.MaxLength = &maxLength
	return b
}

// WithMinimum sets the minimum value for numbers.
func (b *JSONSchemaPropsBuilder) WithMinimum(minimum float64) *JSONSchemaPropsBuilder {
	b.schema.Minimum = &minimum
	return b
}

// WithMaximum sets the maximum value for numbers.
func (b *JSONSchemaPropsBuilder) WithMaximum(maximum float64) *JSONSchemaPropsBuilder {
	b.schema.Maximum = &maximum
	return b
}

// WithExclusiveMinimum sets whether the minimum is exclusive.
func (b *JSONSchemaPropsBuilder) WithExclusiveMinimum(exclusive bool) *JSONSchemaPropsBuilder {
	b.schema.ExclusiveMinimum = exclusive
	return b
}

// WithExclusiveMaximum sets whether the maximum is exclusive.
func (b *JSONSchemaPropsBuilder) WithExclusiveMaximum(exclusive bool) *JSONSchemaPropsBuilder {
	b.schema.ExclusiveMaximum = exclusive
	return b
}

// WithMinItems sets the minimum number of items for arrays.
func (b *JSONSchemaPropsBuilder) WithMinItems(minItems int64) *JSONSchemaPropsBuilder {
	b.schema.MinItems = &minItems
	return b
}

// WithMaxItems sets the maximum number of items for arrays.
func (b *JSONSchemaPropsBuilder) WithMaxItems(maxItems int64) *JSONSchemaPropsBuilder {
	b.schema.MaxItems = &maxItems
	return b
}

// WithUniqueItems sets whether array items must be unique.
func (b *JSONSchemaPropsBuilder) WithUniqueItems(unique bool) *JSONSchemaPropsBuilder {
	b.schema.UniqueItems = unique
	return b
}

// WithProperty adds a property to an object schema.
func (b *JSONSchemaPropsBuilder) WithProperty(name string, propSchema apiextensionsv1.JSONSchemaProps) *JSONSchemaPropsBuilder {
	if b.schema.Properties == nil {
		b.schema.Properties = make(map[string]apiextensionsv1.JSONSchemaProps)
	}

	b.schema.Properties[name] = propSchema

	return b
}

// WithStringProperty adds a string property to an object schema.
func (b *JSONSchemaPropsBuilder) WithStringProperty(name string) *JSONSchemaPropsBuilder {
	return b.WithProperty(name, *NewStringSchema().Build())
}

// WithIntegerProperty adds an integer property to an object schema.
func (b *JSONSchemaPropsBuilder) WithIntegerProperty(name string) *JSONSchemaPropsBuilder {
	return b.WithProperty(name, *NewIntegerSchema().Build())
}

// WithNumberProperty adds a number property to an object schema.
func (b *JSONSchemaPropsBuilder) WithNumberProperty(name string) *JSONSchemaPropsBuilder {
	return b.WithProperty(name, *NewNumberSchema().Build())
}

// WithBooleanProperty adds a boolean property to an object schema.
func (b *JSONSchemaPropsBuilder) WithBooleanProperty(name string) *JSONSchemaPropsBuilder {
	return b.WithProperty(name, *NewBooleanSchema().Build())
}

// WithObjectProperty adds an object property to an object schema.
func (b *JSONSchemaPropsBuilder) WithObjectProperty(name string, objectBuilder *JSONSchemaPropsBuilder) *JSONSchemaPropsBuilder {
	return b.WithProperty(name, *objectBuilder.Build())
}

// WithArrayProperty adds an array property to an object schema.
func (b *JSONSchemaPropsBuilder) WithArrayProperty(name string, arrayBuilder *JSONSchemaPropsBuilder) *JSONSchemaPropsBuilder {
	return b.WithProperty(name, *arrayBuilder.Build())
}

// WithRequiredProperty adds a property and marks it as required.
func (b *JSONSchemaPropsBuilder) WithRequiredProperty(name string, propSchema apiextensionsv1.JSONSchemaProps) *JSONSchemaPropsBuilder {
	b.WithProperty(name, propSchema)
	return b.WithRequiredField(name)
}

// WithRequiredStringProperty adds a required string property.
func (b *JSONSchemaPropsBuilder) WithRequiredStringProperty(name string) *JSONSchemaPropsBuilder {
	return b.WithRequiredProperty(name, *NewStringSchema().Build())
}

// WithRequiredIntegerProperty adds a required integer property.
func (b *JSONSchemaPropsBuilder) WithRequiredIntegerProperty(name string) *JSONSchemaPropsBuilder {
	return b.WithRequiredProperty(name, *NewIntegerSchema().Build())
}

// WithRequiredNumberProperty adds a required number property.
func (b *JSONSchemaPropsBuilder) WithRequiredNumberProperty(name string) *JSONSchemaPropsBuilder {
	return b.WithRequiredProperty(name, *NewNumberSchema().Build())
}

// WithRequiredBooleanProperty adds a required boolean property.
func (b *JSONSchemaPropsBuilder) WithRequiredBooleanProperty(name string) *JSONSchemaPropsBuilder {
	return b.WithRequiredProperty(name, *NewBooleanSchema().Build())
}

// WithRequiredObjectProperty adds a required object property.
func (b *JSONSchemaPropsBuilder) WithRequiredObjectProperty(name string, objectBuilder *JSONSchemaPropsBuilder) *JSONSchemaPropsBuilder {
	return b.WithRequiredProperty(name, *objectBuilder.Build())
}

// WithRequiredArrayProperty adds a required array property.
func (b *JSONSchemaPropsBuilder) WithRequiredArrayProperty(name string, arrayBuilder *JSONSchemaPropsBuilder) *JSONSchemaPropsBuilder {
	return b.WithRequiredProperty(name, *arrayBuilder.Build())
}

// WithRequiredField adds a field to the required fields list.
func (b *JSONSchemaPropsBuilder) WithRequiredField(fieldName string) *JSONSchemaPropsBuilder {
	if b.schema.Required == nil {
		b.schema.Required = []string{}
	}
	// Check if already required to avoid duplicates
	for _, existing := range b.schema.Required {
		if existing == fieldName {
			return b
		}
	}

	b.schema.Required = append(b.schema.Required, fieldName)

	return b
}

// WithRequiredFields adds multiple fields to the required fields list.
func (b *JSONSchemaPropsBuilder) WithRequiredFields(fieldNames ...string) *JSONSchemaPropsBuilder {
	for _, fieldName := range fieldNames {
		b.WithRequiredField(fieldName)
	}

	return b
}

// WithItems sets the items schema for arrays.
func (b *JSONSchemaPropsBuilder) WithItems(itemsSchema *JSONSchemaPropsBuilder) *JSONSchemaPropsBuilder {
	b.schema.Items = &apiextensionsv1.JSONSchemaPropsOrArray{
		Schema: itemsSchema.Build(),
	}

	return b
}

// WithStringItems creates an array schema with string items.
func (b *JSONSchemaPropsBuilder) WithStringItems() *JSONSchemaPropsBuilder {
	return b.WithItems(NewStringSchema())
}

// WithIntegerItems creates an array schema with integer items.
func (b *JSONSchemaPropsBuilder) WithIntegerItems() *JSONSchemaPropsBuilder {
	return b.WithItems(NewIntegerSchema())
}

// WithNumberItems creates an array schema with number items.
func (b *JSONSchemaPropsBuilder) WithNumberItems() *JSONSchemaPropsBuilder {
	return b.WithItems(NewNumberSchema())
}

// WithObjectItems creates an array schema with object items.
func (b *JSONSchemaPropsBuilder) WithObjectItems(objectBuilder *JSONSchemaPropsBuilder) *JSONSchemaPropsBuilder {
	return b.WithItems(objectBuilder)
}

// WithAdditionalProperties sets whether additional properties are allowed.
func (b *JSONSchemaPropsBuilder) WithAdditionalProperties(allowed bool) *JSONSchemaPropsBuilder {
	b.schema.AdditionalProperties = &apiextensionsv1.JSONSchemaPropsOrBool{
		Allows: allowed,
	}

	return b
}

// WithAdditionalPropertiesSchema sets a schema for additional properties.
func (b *JSONSchemaPropsBuilder) WithAdditionalPropertiesSchema(schemaBuilder *JSONSchemaPropsBuilder) *JSONSchemaPropsBuilder {
	b.schema.AdditionalProperties = &apiextensionsv1.JSONSchemaPropsOrBool{
		Schema: schemaBuilder.Build(),
	}

	return b
}

// WithEnum adds enum values for validation.
func (b *JSONSchemaPropsBuilder) WithEnum(values ...interface{}) *JSONSchemaPropsBuilder {
	enumValues := make([]apiextensionsv1.JSON, len(values))
	for i, value := range values {
		enumValues[i] = apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf("%v", value))}
	}

	b.schema.Enum = enumValues

	return b
}

// WithStringEnum adds string enum values.
func (b *JSONSchemaPropsBuilder) WithStringEnum(values ...string) *JSONSchemaPropsBuilder {
	enumValues := make([]apiextensionsv1.JSON, len(values))
	for i, value := range values {
		enumValues[i] = apiextensionsv1.JSON{Raw: []byte(`"` + value + `"`)}
	}

	b.schema.Enum = enumValues

	return b
}

// WithDefault sets a default value.
func (b *JSONSchemaPropsBuilder) WithDefault(defaultValue interface{}) *JSONSchemaPropsBuilder {
	switch v := defaultValue.(type) {
	case string:
		b.schema.Default = &apiextensionsv1.JSON{Raw: []byte(`"` + v + `"`)}
	case int:
		b.schema.Default = &apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf("%d", v))}
	case int64:
		b.schema.Default = &apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf("%d", v))}
	case float64:
		b.schema.Default = &apiextensionsv1.JSON{Raw: []byte(fmt.Sprintf("%g", v))}
	case bool:
		if v {
			b.schema.Default = &apiextensionsv1.JSON{Raw: []byte("true")}
		} else {
			b.schema.Default = &apiextensionsv1.JSON{Raw: []byte("false")}
		}
	}

	return b
}

// WithDescription sets the description field.
func (b *JSONSchemaPropsBuilder) WithDescription(description string) *JSONSchemaPropsBuilder {
	b.schema.Description = description

	return b
}

// WithTitle sets the title field.
func (b *JSONSchemaPropsBuilder) WithTitle(title string) *JSONSchemaPropsBuilder {
	b.schema.Title = title

	return b
}

// Build returns a deep copy of the constructed JSONSchemaProps.
func (b *JSONSchemaPropsBuilder) Build() *apiextensionsv1.JSONSchemaProps {
	// Create a deep copy to avoid aliasing
	schemaCopy := b.schema.DeepCopy()

	return schemaCopy
}

// --- Convenience builders for common scenarios ---

// SimpleStringSchema creates a simple string schema with optional constraints.
func SimpleStringSchema(pattern string, minLength, maxLength *int64) *JSONSchemaPropsBuilder {
	builder := NewStringSchema()
	if pattern != "" {
		builder.WithPattern(pattern)
	}

	if minLength != nil {
		builder.WithMinLength(*minLength)
	}

	if maxLength != nil {
		builder.WithMaxLength(*maxLength)
	}

	return builder
}

// SimpleIntegerSchema creates a simple integer schema with optional constraints.
func SimpleIntegerSchema(minimum, maximum *float64) *JSONSchemaPropsBuilder {
	builder := NewIntegerSchema()

	if minimum != nil {
		builder.WithMinimum(*minimum)
	}

	if maximum != nil {
		builder.WithMaximum(*maximum)
	}

	return builder
}

// SimpleArraySchema creates a simple array schema with the given item type.
func SimpleArraySchema(itemBuilder *JSONSchemaPropsBuilder, minItems, maxItems *int64, uniqueItems bool) *JSONSchemaPropsBuilder {
	builder := NewArraySchema().WithItems(itemBuilder)
	if minItems != nil {
		builder.WithMinItems(*minItems)
	}

	if maxItems != nil {
		builder.WithMaxItems(*maxItems)
	}

	if uniqueItems {
		builder.WithUniqueItems(true)
	}

	return builder
}

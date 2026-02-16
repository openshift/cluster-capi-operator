package objectpruning

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/utils/ptr"
)

func walkUnstructuredObject(obj map[string]interface{}, schema apiextensionsv1.JSONSchemaProps) {
	for k, v := range obj {
		propSchema, ok := schema.Properties[k]
		if !ok {
			if ptr.Deref(schema.XPreserveUnknownFields, false) || schema.AdditionalProperties != nil {
				// If there's an additional properties schema, or the schema preserves unknown fields, then we keep the property.
				// This matches the behaviour of the API server's pruning algorithm.
				continue
			}

			// This property is not in the schema, and this part of the schema does not preserve unknown fields, so prune it.
			delete(obj, k)
			continue
		}

		switch propSchema.Type {
		case "object":
			walkUnstructuredObject(v.(map[string]interface{}), propSchema)
		case "array":
			walkUnstructuredArray(v.([]interface{}), propSchema)
		default:
			// This property is a scalar, so we don't need to walk it.
		}
	}
}

func walkUnstructuredArray(arr []interface{}, schema apiextensionsv1.JSONSchemaProps) {
	if schema.Items == nil || schema.Items.Schema == nil {
		// For an array schema, there shoul be a valid items schema.
		return
	}

	for _, v := range arr {
		walkUnstructuredObject(v.(map[string]interface{}), *schema.Items.Schema)
	}
}

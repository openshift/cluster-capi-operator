// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package objectpruning

import (
	"context"
	"fmt"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"go.yaml.in/yaml/v2"
	apiextensionshelpers "k8s.io/apiextensions-apiserver/pkg/apihelpers"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	// WebhookPrefix is the static path prefix of our object admission endpoint.
	// Requests will be sent to a sub-path with the next component of the path
	// as a compatibility requirement name.
	WebhookPrefix = "/compatibility-requirement-object-mutation/"
)

// validator implements the admission.Handler to have a custom Handle function which is able to
// validate arbitrary objects against CompatibilityRequirements by leveraging unstructured.
type validator struct {
	client  client.Reader
	decoder admission.Decoder
}

// NewValidator returns a partially initialized ObjectValidator.
func NewValidator() *validator {
	return &validator{
		// This decoder is only used to decode to unstructured and for CompatibilityRequirements.
		decoder: admission.NewDecoder(runtime.NewScheme()),
	}
}

type controllerOption func(*builder.Builder) *builder.Builder

// SetupWithManager registers the objectValidator webhook with an manager.
func (v *validator) SetupWithManager(ctx context.Context, mgr ctrl.Manager, opts ...controllerOption) error {
	v.client = mgr.GetClient()

	// Register a webhook on a path with a dynamic component for the compatibility requirement name.
	// we will extract this component into the context so that the handler can identify which compatibility
	// requirement the request was intended to validate against.
	mgr.GetWebhookServer().Register(WebhookPrefix+"{CompatibilityRequirement}", &admission.Webhook{
		Handler:         v,
		WithContextFunc: compatibilityRequrementIntoContext,
	})

	return nil
}

// handleObjectPruning handles the pruning of an object.
func (v *validator) handleObjectPruning(ctx context.Context, compatibilityRequirementName string, obj *unstructured.Unstructured) error {
	schema, err := v.getCopmatibilityRequirementCRDSchema(ctx, compatibilityRequirementName, obj.GroupVersionKind().Version)
	if err != nil {
		return fmt.Errorf("failed to get schema for CompatibilityRequirement %q: %w", compatibilityRequirementName, err)
	}

	if schema.OpenAPIV3Schema == nil {
		return fmt.Errorf("schema for CompatibilityRequirement %q is not valid", compatibilityRequirementName)
	}

	prunedObject := runtime.DeepCopyJSONValue(obj.Object).(map[string]interface{})
	
	// Ignore these fields. While they are part of the schema we aren't interested in pruning them.
	delete(prunedObject, "apiVersion")
	delete(prunedObject, "kind")
	delete(prunedObject, "metadata")

	walkUnstructuredObject(prunedObject, *schema.OpenAPIV3Schema)

	// Restore the fields that were deleted.
	prunedObject["apiVersion"] = obj.GetAPIVersion()
	prunedObject["kind"] = obj.GetKind()
	prunedObject["metadata"] = obj.Object["metadata"]

	obj.Object = prunedObject
	return nil
}

func (v *validator) getCopmatibilityRequirementCRDSchema(ctx context.Context, compatibilityRequirementName string, version string) (*apiextensionsv1.CustomResourceValidation, error) {
	compatibilityRequirement := &apiextensionsv1alpha1.CompatibilityRequirement{}
	if err := v.client.Get(ctx, client.ObjectKey{Name: compatibilityRequirementName}, compatibilityRequirement); err != nil {
		return nil, fmt.Errorf("failed to get CompatibilityRequirement %q: %w", compatibilityRequirementName, err)
	}

	compatibilityCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal([]byte(compatibilityRequirement.Spec.CompatibilitySchema.CustomResourceDefinition.Data), compatibilityCRD); err != nil {
		return nil, fmt.Errorf("failed to parse compatibility schema data for CompatibilityRequirement %q: %w", compatibilityRequirement.Name, err)
	}

	schema, err := apiextensionshelpers.GetSchemaForVersion(compatibilityCRD, version)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema for CompatibilityRequirement %q: %w", compatibilityRequirementName, err)
	}

	return schema, nil
}

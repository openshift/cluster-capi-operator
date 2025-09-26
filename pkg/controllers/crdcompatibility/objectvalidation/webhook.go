// Copyright 2025 Red Hat, Inc.
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

package objectvalidation

import (
	"context"
	"fmt"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	apiextensionshelpers "k8s.io/apiextensions-apiserver/pkg/apihelpers"
	apiextensionsinternal "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"
)

const (
	webhookPrefix = "/crdcompatibility/"
)

var _ admission.Handler = &objectValidator{}

// objectValidator implements the admission.Handler to have a custom Handle function which is able to
// validate arbitrary objects against CRDCompatibilityRequirements by leveraging unstructured.
type objectValidator struct {
	client  client.Reader
	decoder admission.Decoder
}

func NewObjectValidator() *objectValidator {
	return &objectValidator{
		// This decoder is only used to decode to unstructured and for CRDCompatibilityRequirements.
		decoder: admission.NewDecoder(runtime.NewScheme()),
	}
}

type controllerOption func(*builder.Builder) *builder.Builder

func (h *objectValidator) SetupWithManager(ctx context.Context, mgr ctrl.Manager, opts ...controllerOption) error {
	h.client = mgr.GetClient()
	mgr.GetWebhookServer().Register(webhookPrefix+"{CRDCompatibilityRequirement}", &admission.Webhook{
		Handler:         h,
		WithContextFunc: crdCompatibilityRequrementIntoContext,
	})

	return nil
}

func (h *objectValidator) ValidateCreate(ctx context.Context, crdCompatibilityRequirementName string, obj *unstructured.Unstructured) (warnings admission.Warnings, err error) {
	validator, celValidator, err := h.createSchemaValidator(ctx, crdCompatibilityRequirementName, obj.GroupVersionKind().Version)
	if err != nil {
		return nil, fmt.Errorf("failed to validate: %w", err)
	}

	res := validator.Validate(obj)
	if !res.IsValid() && res.HasWarnings() {
		for _, warning := range res.Warnings {
			warnings = append(warnings, warning.Error())
		}
	}

	if celErrs, _ := celValidator.Validate(ctx, nil, nil, obj.Object, nil, celconfig.RuntimeCELCostBudget); celErrs != nil {
		res.Errors = append(res.Errors, celErrs.ToAggregate())
	}

	return warnings, res.AsError()
}

func (h *objectValidator) ValidateUpdate(ctx context.Context, crdCompatibilityRequirementName string, oldObj, obj *unstructured.Unstructured) (warnings admission.Warnings, err error) {
	validator, celValidator, err := h.createSchemaValidator(ctx, crdCompatibilityRequirementName, obj.GroupVersionKind().Version)
	if err != nil {
		return nil, fmt.Errorf("failed to validate: %w", err)
	}

	res := validator.ValidateUpdate(obj, oldObj)
	if !res.IsValid() && res.HasWarnings() {
		for _, warning := range res.Warnings {
			warnings = append(warnings, warning.Error())
		}
	}

	if celErrs, _ := celValidator.Validate(ctx, nil, nil, obj, nil, celconfig.RuntimeCELCostBudget); celErrs != nil {
		res.Errors = append(res.Errors, celErrs.ToAggregate())
	}

	return warnings, res.AsError()
}

func (h *objectValidator) ValidateDelete(ctx context.Context, crdCompatibilityRequirementName string, obj *unstructured.Unstructured) (warnings admission.Warnings, err error) {
	return nil, nil
}

func (h *objectValidator) createSchemaValidator(ctx context.Context, crdCompatibilityRequirementName string, version string) (apiextensionsvalidation.SchemaValidator, *cel.Validator, error) {
	// Get the CRDCompatibilityRequirement
	crdCompatibilityRequirement := &operatorv1alpha1.CRDCompatibilityRequirement{}
	if err := h.client.Get(ctx, client.ObjectKey{Name: crdCompatibilityRequirementName}, crdCompatibilityRequirement); err != nil {
		return nil, nil, fmt.Errorf("failed to get CRDCompatibilityRequirement %q: %w", crdCompatibilityRequirementName, err)
	}

	// Extract the CRD so we can use the schema.
	compatibilityCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal([]byte(crdCompatibilityRequirement.Spec.CompatibilityCRD), &compatibilityCRD); err != nil {
		return nil, nil, fmt.Errorf("failed to parse compatibilityCRD for CRDCompatibilityRequirement %q: %w", crdCompatibilityRequirement.Name, err)
	}

	if compatibilityCRD.APIVersion != "apiextensions.k8s.io/v1" || compatibilityCRD.Kind != "CustomResourceDefinition" {
		return nil, nil, fmt.Errorf("expected APIVersion to be apiextensions.k8s.io/v1 and Kind to be CustomResourceDefinition, got %s/%s", compatibilityCRD.APIVersion, compatibilityCRD.Kind)
	}

	// Adapted from k8s.io/apiextensions-apiserver/pkg/apiserver/customresource_handler.go getOrCreateServingInfoFor.
	// Creates the validator from JSONSchemaProps.
	validationSchema, err := apiextensionshelpers.GetSchemaForVersion(compatibilityCRD, version)
	if err != nil {
		return nil, nil, fmt.Errorf("the server could not properly serve the CR schema")
	}
	if validationSchema == nil {
		return nil, nil, fmt.Errorf("the server could not create the validationSchema")
	}

	var internalSchemaProps *apiextensionsinternal.JSONSchemaProps
	var internalValidationSchema *apiextensionsinternal.CustomResourceValidation
	if validationSchema != nil {
		internalValidationSchema = &apiextensionsinternal.CustomResourceValidation{}
		if err := apiextensionsv1.Convert_v1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(validationSchema, internalValidationSchema, nil); err != nil {
			return nil, nil, fmt.Errorf("failed to convert CRD validation to internal version: %v", err)
		}
		internalSchemaProps = internalValidationSchema.OpenAPIV3Schema
	}

	structuralSchema, err := structuralschema.NewStructural(internalValidationSchema.OpenAPIV3Schema)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert CRD validation to internal version: %v", err)
	}

	celValidator := cel.NewValidator(structuralSchema, true, celconfig.PerCallLimit)

	validator, _, err := apiextensionsvalidation.NewSchemaValidator(internalSchemaProps)
	if err != nil {
		return nil, nil, fmt.Errorf("the server could not properly create the SchemaValidator")
	}

	return validator, celValidator, nil
}

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

package crdcompatibility

import (
	"context"
	"errors"
	"fmt"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/kubernetes/pkg/apis/admissionregistration"
	admissionregistrationvalidation "k8s.io/kubernetes/pkg/apis/admissionregistration/validation"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
)

var (
	errExpectedCompatibilityRequirement = errors.New("expected a CompatibilityRequirement")
	errInvalidCompatibilityCRD          = errors.New("expected a valid CustomResourceDefinition in YAML format")
	errPathNotFound                     = errors.New("path not found in schema")
)

type crdRequirementValidator struct{}

var _ admission.CustomValidator = &crdRequirementValidator{}

// ValidateCreate validates a Create event for a CompatibilityRequirement.
func (v *crdRequirementValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(ctx, obj)
}

// ValidateUpdate validates an Update event for a CompatibilityRequirement.
func (v *crdRequirementValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(ctx, newObj)
}

// validateCreateOrUpdate ensures that the compatibility CRD is valid YAML.
func (v *crdRequirementValidator) validateCreateOrUpdate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	compatibilityRequirement, ok := obj.(*apiextensionsv1alpha1.CompatibilityRequirement)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errExpectedCompatibilityRequirement, obj)
	}

	errs := validateCompatibilitySchema(ctx, field.NewPath("spec").Child("compatibilitySchema"), compatibilityRequirement.Spec.CompatibilitySchema)

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}

	// Generate and then validate the expected ValidatingWebhookConfiguration from the CompatibilityRequirement object validation specification.
	desiredValidatingWebhookConfiguration := &admissionregistration.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: compatibilityRequirement.Name,
		},
		Webhooks: []admissionregistration.ValidatingWebhook{
			{
				Name:                    "a.b.c",                                           // This doesn't matter for what we are testing, but must pass being a three segment domain.
				SideEffects:             ptr.To(admissionregistration.SideEffectClassNone), // A value is required to pass validation.
				AdmissionReviewVersions: []string{"v1"},                                    // A value is required to pass validation (either v1 or v1beta1).
				ClientConfig: admissionregistration.WebhookClientConfig{
					URL: ptr.To("https://a.b.c"), // A value is required to pass validation.
				},

				// These we actually care about validating.
				MatchConditions:   convertToMatchConditions(compatibilityRequirement.Spec.ObjectSchemaValidation.MatchConditions),
				NamespaceSelector: compatibilityRequirement.Spec.ObjectSchemaValidation.NamespaceSelector.DeepCopy(),
				ObjectSelector:    compatibilityRequirement.Spec.ObjectSchemaValidation.ObjectSelector.DeepCopy(),
			},
		},
	}

	errs = admissionregistrationvalidation.ValidateValidatingWebhookConfiguration(desiredValidatingWebhookConfiguration)
	if len(errs) > 0 {
		return nil, fmt.Errorf("desiredValidatingWebhookConfiguration is not valid: %w", errs.ToAggregate())
	}

	return nil, nil
}

// ValidateDelete validates a Delete event for a CompatibilityRequirement.
func (v *crdRequirementValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) {
	// We have no validation requirements for deletion.
	return nil, nil
}

func convertToMatchConditions(matchConditions []admissionregistrationv1.MatchCondition) []admissionregistration.MatchCondition {
	out := make([]admissionregistration.MatchCondition, len(matchConditions))
	for i, matchCondition := range matchConditions {
		out[i] = admissionregistration.MatchCondition{
			Name:       matchCondition.Name,
			Expression: matchCondition.Expression,
		}
	}

	return out
}

func convertToInternalCRD(compatibilityCRD *apiextensionsv1.CustomResourceDefinition) (*apiextensions.CustomResourceDefinition, error) {
	compatibilityCRDSpec := &apiextensions.CustomResourceDefinitionSpec{}
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinitionSpec_To_apiextensions_CustomResourceDefinitionSpec(compatibilityCRD.Spec.DeepCopy(), compatibilityCRDSpec, nil); err != nil {
		return nil, fmt.Errorf("failed to convert CRD spec: %w", err)
	}

	crd := &apiextensions.CustomResourceDefinition{
		ObjectMeta: compatibilityCRD.ObjectMeta,
		Spec:       *compatibilityCRDSpec,
	}

	// Copied from the CustomResourceDefintion Strategy. Required to pass validation.
	// https://github.com/kubernetes/apiextensions-apiserver/blob/1b4f52c293fc29eb5dfdf4867e7744f7c398fe55/pkg/registry/customresourcedefinition/strategy.go#L79C2-L86C3
	for _, v := range crd.Spec.Versions {
		if v.Storage {
			if !apiextensions.IsStoredVersion(crd, v.Name) {
				crd.Status.StoredVersions = append(crd.Status.StoredVersions, v.Name)
			}

			break
		}
	}

	return crd, nil
}

func validateCompatibilitySchema(ctx context.Context, fldPath *field.Path, compatibilitySchema apiextensionsv1alpha1.CompatibilitySchema) field.ErrorList {
	compatibilityCRD, errs := validateCompatibilitySchemaCustomResourceDefinition(ctx, fldPath.Child("customResourceDefinition"), compatibilitySchema)
	if len(errs) > 0 {
		return errs
	}

	errs = append(errs, validateExcludedFields(fldPath.Child("excludedFields"), compatibilityCRD, compatibilitySchema.ExcludedFields)...)

	return errs
}

func validateCompatibilitySchemaCustomResourceDefinition(ctx context.Context, fldPath *field.Path, compatibilitySchema apiextensionsv1alpha1.CompatibilitySchema) (*apiextensionsv1.CustomResourceDefinition, field.ErrorList) {
	// Parse the CRD in compatibilityCRD into a CRD object.
	compatibilityCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal([]byte(compatibilitySchema.CustomResourceDefinition.Data), &compatibilityCRD); err != nil {
		return nil, field.ErrorList{field.Invalid(fldPath.Child("data"), compatibilitySchema.CustomResourceDefinition.Data, fmt.Errorf("%w: %w", errInvalidCompatibilityCRD, err).Error())}
	}

	if compatibilityCRD.APIVersion != "apiextensions.k8s.io/v1" || compatibilityCRD.Kind != "CustomResourceDefinition" {
		return nil, field.ErrorList{field.Invalid(fldPath.Child("data"), compatibilityCRD.APIVersion, fmt.Errorf("%w: expected APIVersion to be apiextensions.k8s.io/v1 and Kind to be CustomResourceDefinition, got %s/%s", errInvalidCompatibilityCRD, compatibilityCRD.APIVersion, compatibilityCRD.Kind).Error())}
	}

	// Convert the CRD to the internal type so that we can validate it.
	internalCRD, err := convertToInternalCRD(compatibilityCRD)
	if err != nil {
		return nil, field.ErrorList{field.Invalid(fldPath.Child("customResourceDefinition"), compatibilitySchema.CustomResourceDefinition.Data, fmt.Errorf("failed to convert CRD to internal CRD: %w", err).Error())}
	}

	// Validate that the CRD we have been given is a complete, and valid CRD.
	errs := apiextensionsvalidation.ValidateCustomResourceDefinition(ctx, internalCRD)

	return compatibilityCRD, errs
}

func validateExcludedFields(fldPath *field.Path, compatibilityCRD *apiextensionsv1.CustomResourceDefinition, excludedFields []apiextensionsv1alpha1.APIExcludedField) field.ErrorList {
	errs := field.ErrorList{}

	for i, excludedField := range excludedFields {
		errs = append(errs, validateExcludedField(fldPath.Index(i), compatibilityCRD, excludedField)...)
	}

	return errs
}

func validateExcludedField(fldPath *field.Path, compatibilityCRD *apiextensionsv1.CustomResourceDefinition, excludedField apiextensionsv1alpha1.APIExcludedField) field.ErrorList {
	errs := field.ErrorList{}

	if len(excludedField.Versions) == 0 {
		for _, schema := range compatibilityCRD.Spec.Versions {
			err := validatePathExists(schema.Schema.OpenAPIV3Schema, strings.Split(excludedField.Path, "."))
			if err != nil {
				errs = append(errs, field.Invalid(fldPath.Child("path"), excludedField.Path, err.Error()))
			}
		}

		return errs
	}

	for i, version := range excludedField.Versions {
		found := false

		for _, schema := range compatibilityCRD.Spec.Versions {
			if schema.Name == string(version) {
				if schema.Schema == nil || schema.Schema.OpenAPIV3Schema == nil {
					// This can only happen if the CRD sets PreserveUnknownFields to true at the top spec level.
					errs = append(errs, field.Invalid(fldPath.Child("versions").Index(i), version, fmt.Sprintf("version %s does not have a schema", version)))
					continue
				}

				if err := validatePathExists(schema.Schema.OpenAPIV3Schema, strings.Split(excludedField.Path, ".")); err != nil {
					errs = append(errs, field.Invalid(fldPath.Child("path"), excludedField.Path, err.Error()))
				}

				found = true
			}
		}

		if !found {
			errs = append(errs, field.Invalid(fldPath.Child("versions").Index(i), version, fmt.Sprintf("version %s not found in compatibility schema", version)))
		}
	}

	return errs
}

func validatePathExists(schema *apiextensionsv1.JSONSchemaProps, path []string) error {
	desiredPath := field.NewPath("^", path...)
	currentPath := field.NewPath("^")

	for i, key := range path {
		parentPath := currentPath.String()
		currentPath = currentPath.Child(key)

		// We should always be looking at an object.
		// If we find an array, we extract the object schema for the next loop.
		// If we find a scalar and have not reached the end of the path, we return an error.
		propSchema, ok := schema.Properties[key]
		if !ok {
			return fmt.Errorf("%w: desired path %s, path %s is missing child %s", errPathNotFound, desiredPath, parentPath, key)
		}

		switch {
		case i == len(path)-1:
			// This is the last key in the path, so we found the path.
			return nil
		case propSchema.Type == "object":
			schema = &propSchema
		case propSchema.Type == "array":
			if propSchema.Items == nil || propSchema.Items.Schema == nil {
				return fmt.Errorf("%w: desired path %s, path %s is an array but does not have an items schema", errPathNotFound, desiredPath, currentPath)
			}

			schema = propSchema.Items.Schema
		default:
			return fmt.Errorf("%w: desired path %s, path %s is not an object", errPathNotFound, desiredPath, currentPath)
		}
	}

	return fmt.Errorf("%w: desired path %s", errPathNotFound, desiredPath)
}

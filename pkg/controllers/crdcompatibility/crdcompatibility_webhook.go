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

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

	// Parse the CRD in compatibilityCRD into a CRD object.
	compatibilityCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal([]byte(compatibilityRequirement.Spec.CompatibilitySchema.CustomResourceDefinition.Data), &compatibilityCRD); err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidCompatibilityCRD, err)
	}

	if compatibilityCRD.APIVersion != "apiextensions.k8s.io/v1" || compatibilityCRD.Kind != "CustomResourceDefinition" {
		return nil, fmt.Errorf("%w: expected APIVersion to be apiextensions.k8s.io/v1 and Kind to be CustomResourceDefinition, got %s/%s", errInvalidCompatibilityCRD, compatibilityCRD.APIVersion, compatibilityCRD.Kind)
	}

	// Convert the CRD to the internal type so that we can validate it.
	internalCRD, err := convertToInternalCRD(compatibilityCRD)
	if err != nil {
		return nil, fmt.Errorf("failed to convert CRD to internal CRD: %w", err)
	}

	// Validate that the CRD we have been given is a complete, and valid CRD.
	errs := apiextensionsvalidation.ValidateCustomResourceDefinition(ctx, internalCRD)
	if len(errs) > 0 {
		return nil, fmt.Errorf("compatibilityCRD is not valid: %w", errs.ToAggregate())
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

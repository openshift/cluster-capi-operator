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

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
)

var (
	errExpectedCRDCompatibilityRequirement = errors.New("expected a CRDCompatibilityRequirement")
	errInvalidCompatibilityCRD             = errors.New("expected a valid CustomResourceDefinition in YAML format")
)

type crdRequirementValidator struct{}

var _ admission.CustomValidator = &crdRequirementValidator{}

// ValidateCreate validates a Create event for a CRDCompatibilityRequirement.
func (v *crdRequirementValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(ctx, obj)
}

// ValidateUpdate validates an Update event for a CRDCompatibilityRequirement.
func (v *crdRequirementValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(ctx, newObj)
}

// validateCreateOrUpdate ensures that the compatibility CRD is valid YAML.
func (v *crdRequirementValidator) validateCreateOrUpdate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	crdCompatibilityRequirement, ok := obj.(*operatorv1alpha1.CRDCompatibilityRequirement)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errExpectedCRDCompatibilityRequirement, obj)
	}

	// Parse the CRD in compatibilityCRD into a CRD object
	compatibilityCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal([]byte(crdCompatibilityRequirement.Spec.CompatibilityCRD), &compatibilityCRD); err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidCompatibilityCRD, err)
	}

	if compatibilityCRD.APIVersion != "apiextensions.k8s.io/v1" || compatibilityCRD.Kind != "CustomResourceDefinition" {
		return nil, fmt.Errorf("%w: expected APIVersion to be apiextensions.k8s.io/v1 and Kind to be CustomResourceDefinition, got %s/%s", errInvalidCompatibilityCRD, compatibilityCRD.APIVersion, compatibilityCRD.Kind)
	}

	// TODO: investigate fully validating the CRD with k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation/ValidateCustomResourceDefinition

	return nil, nil
}

// ValidateDelete validates a Delete event for a CRDCompatibilityRequirement.
func (v *crdRequirementValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) {
	// We have no validation requirements for deletion.
	return nil, nil
}

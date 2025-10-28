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

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
)

var (
	errExpectedCompatibilityRequirement = errors.New("expected a CompatibilityRequirement")
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
func (v *crdRequirementValidator) validateCreateOrUpdate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	_, ok := obj.(*apiextensionsv1alpha1.CompatibilityRequirement)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errExpectedCompatibilityRequirement, obj)
	}

	return nil, nil
}

// ValidateDelete validates a Delete event for a CompatibilityRequirement.
func (v *crdRequirementValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) {
	// We have no validation requirements for deletion.
	return nil, nil
}

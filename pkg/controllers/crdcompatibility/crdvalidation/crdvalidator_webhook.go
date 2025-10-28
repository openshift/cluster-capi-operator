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

package crdvalidation

import (
	"context"
	"errors"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/index"
)

var (
	// ErrCRDHasRequirements is the error which signals a deletion of a CRD is disallowed.
	ErrCRDHasRequirements = errors.New("cannot delete CRD because it has CompatibilityRequirements")
	errExpectedCRD        = errors.New("expected a CustomResourceDefinition")
)

// NewValidator returns a partially initialised Validator.
func NewValidator(client client.Client) *Validator {
	return &Validator{
		client: client,
	}
}

type controllerOption func(*builder.Builder) *builder.Builder

// SetupWithManager sets up the controller with the Manager.
func (v *Validator) SetupWithManager(ctx context.Context, mgr ctrl.Manager, opts ...controllerOption) error {
	// Create field index for spec.crdRef
	if err := index.AddIndexThreadSafe(ctx, mgr, &apiextensionsv1.CustomResourceDefinition{}, index.FieldCRDByName, index.CRDByName); err != nil {
		return fmt.Errorf("failed to add index to CompatibilityRequirements: %w", err)
	}

	err := ctrl.NewWebhookManagedBy(mgr).
		For(&apiextensionsv1.CustomResourceDefinition{}).
		WithValidator(v).Complete()
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// Validator implements the crd validation webhook to validate a CRD for compatibility against defined CompatibilityRequirements.
type Validator struct {
	client client.Reader
}

var _ admission.CustomValidator = &Validator{}

func (v *Validator) validateCreateOrUpdate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	_, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errExpectedCRD, obj)
	}

	return nil, nil
}

// ValidateCreate validates a Create event for a CustomResourceDefinition.
func (v *Validator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(ctx, obj)
}

// ValidateUpdate validates an Update event for a CustomResourceDefinition.
func (v *Validator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(ctx, newObj)
}

// ValidateDelete validates a Delete event for a CustomResourceDefinition.
func (v *Validator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	_, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errExpectedCRD, obj)
	}

	return nil, nil
}

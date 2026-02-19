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
	"slices"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/index"
	"github.com/openshift/cluster-capi-operator/pkg/crdchecker"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

var (
	// ErrCRDHasRequirements is the error which signals a deletion of a CRD is disallowed.
	ErrCRDHasRequirements    = errors.New("cannot delete CRD because it has CompatibilityRequirements")
	errExpectedCRD           = errors.New("expected a CustomResourceDefinition")
	errCRDNotCompatible      = errors.New("CRD is not compatible with CompatibilityRequirements")
	errUnknownCRDAdmitAction = errors.New("unknown value for CompatibilityRequirement.spec.customResourceDefinitionSchemaValidation.action")
	errPathNotFound          = errors.New("path not found in schema")
	errVersionNoSchema       = errors.New("version does not have a schema")
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

func (v *Validator) validateCreateOrUpdate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errExpectedCRD, obj)
	}

	compatibilityRequirements := apiextensionsv1alpha1.CompatibilityRequirementList{}
	if err := v.client.List(ctx, &compatibilityRequirements, &client.ListOptions{FieldSelector: fields.SelectorFromSet(fields.Set{index.FieldCRDByName: crd.GetName()})}); err != nil {
		return nil, fmt.Errorf("failed to list CompatibilityRequirements: %w for CRD %q", err, crd.GetName())
	}

	var (
		allReqErrors   []string
		allReqWarnings []string
	)

	for _, compatibilityRequirement := range compatibilityRequirements.Items {
		// This is an optional field, so if the object is zero, this means the field was not set, and no CRD admission validation is required.
		if compatibilityRequirement.Spec.CustomResourceDefinitionSchemaValidation == (apiextensionsv1alpha1.CustomResourceDefinitionSchemaValidation{}) {
			continue
		}

		compatibilityCRD := &apiextensionsv1.CustomResourceDefinition{}
		if err := yaml.Unmarshal([]byte(compatibilityRequirement.Spec.CompatibilitySchema.CustomResourceDefinition.Data), compatibilityCRD); err != nil {
			return nil, fmt.Errorf("failed to parse compatibilityCRD for CompatibilityRequirement %q: %w", compatibilityRequirement.Name, err)
		}

		prunedCRD, err := pruneExcludedFields(compatibilityCRD, compatibilityRequirement.Spec.CompatibilitySchema.ExcludedFields)
		if err != nil {
			return nil, fmt.Errorf("failed to prune excluded fields for CompatibilityRequirement %q: %w", compatibilityRequirement.Name, err)
		}

		reqErrors, reqWarnings, err := crdchecker.CheckCompatibilityRequirement(prunedCRD, crd)
		if err != nil {
			return nil, fmt.Errorf("failed to check CRD compatibility: %w", err)
		}

		prependName := func(s string) string {
			return fmt.Sprintf("This requirement was added by CompatibilityRequirement %s: %s", compatibilityRequirement.Name, s)
		}

		switch compatibilityRequirement.Spec.CustomResourceDefinitionSchemaValidation.Action {
		case apiextensionsv1alpha1.CRDAdmitActionWarn:
			allReqWarnings = append(allReqWarnings, util.SliceMap(reqErrors, prependName)...)
		case apiextensionsv1alpha1.CRDAdmitActionDeny:
			allReqErrors = append(allReqErrors, util.SliceMap(reqErrors, prependName)...)
		default:
			// Note: This should be impossible as validation on the action is enforced by openapi as an enum.
			return nil, fmt.Errorf("%w: %q for requirement %s", errUnknownCRDAdmitAction, compatibilityRequirement.Spec.CustomResourceDefinitionSchemaValidation.Action, compatibilityRequirement.Name)
		}

		allReqWarnings = append(allReqWarnings, util.SliceMap(reqWarnings, prependName)...)
	}

	if len(allReqErrors) > 0 {
		return allReqWarnings, fmt.Errorf("%w: %s", errCRDNotCompatible, strings.Join(allReqErrors, "\n"))
	}

	return allReqWarnings, nil
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
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errExpectedCRD, obj)
	}

	compatibilityRequirements := apiextensionsv1alpha1.CompatibilityRequirementList{}
	if err := v.client.List(ctx, &compatibilityRequirements, &client.ListOptions{FieldSelector: fields.SelectorFromSet(fields.Set{index.FieldCRDByName: crd.GetName()})}); err != nil {
		return nil, fmt.Errorf("failed to list CompatibilityRequirements: %w for CRD %q", err, crd.GetName())
	}

	if len(compatibilityRequirements.Items) > 0 {
		names := []string{}
		for _, compatibilityRequirement := range compatibilityRequirements.Items {
			names = append(names, compatibilityRequirement.Name)
		}

		return nil, fmt.Errorf("%w: %s", ErrCRDHasRequirements, strings.Join(names, ", "))
	}

	return nil, nil
}

func pruneExcludedFields(crd *apiextensionsv1.CustomResourceDefinition, excludedFields []apiextensionsv1alpha1.APIExcludedField) (*apiextensionsv1.CustomResourceDefinition, error) {
	pathsByVersion := make(map[string][][]string)

	// First split all paths into their components and group them by version.
	for _, excludedField := range excludedFields {
		paths := strings.Split(excludedField.Path, ".")

		// Apply to all versions if no version is specified.
		// Use `""` to denote all versions since this is not a valid version string.
		if len(excludedField.Versions) == 0 {
			pathsByVersion[""] = append(pathsByVersion[""], paths)
		} else {
			for _, version := range excludedField.Versions {
				pathsByVersion[string(version)] = append(pathsByVersion[string(version)], paths)
			}
		}
	}

	prunedCRD := crd.DeepCopy()

	var errs []error

	for _, schema := range prunedCRD.Spec.Versions {
		paths := slices.Concat(pathsByVersion[""], pathsByVersion[schema.Name])
		if len(paths) == 0 {
			continue
		}

		if schema.Schema == nil || schema.Schema.OpenAPIV3Schema == nil {
			errs = append(errs, fmt.Errorf("%w: version %s does not have a schema", errVersionNoSchema, schema.Name))
			continue
		}

		rootSchema := schema.Schema.OpenAPIV3Schema

		for _, path := range paths {
			err := prunePath(rootSchema, field.NewPath("^", path...), field.NewPath("^"), path)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("failed to prune excluded fields: %w", utilerrors.NewAggregate(errs))
	}

	return prunedCRD, nil
}

func prunePath(schema *apiextensionsv1.JSONSchemaProps, desiredPath *field.Path, currentPath *field.Path, pathSegments []string) error {
	key := pathSegments[0]

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
	case len(pathSegments) == 1:
		// This is the last key in the path so we prune the property.
		delete(schema.Properties, key)
		schema.Required = util.SliceFilter(schema.Required, func(f string) bool { return f != key })
	case propSchema.Type == "object":
		if err := prunePath(&propSchema, desiredPath, currentPath, pathSegments[1:]); err != nil {
			return err
		}

		// Update the properties map as we have a modified copy of the child schema.
		schema.Properties[key] = propSchema
	case propSchema.Type == "array":
		if propSchema.Items == nil || propSchema.Items.Schema == nil {
			return fmt.Errorf("%w: desired path %s, path %s is an array but does not have an items schema", errPathNotFound, desiredPath, currentPath)
		}

		if err := prunePath(propSchema.Items.Schema, desiredPath, currentPath, pathSegments[1:]); err != nil {
			return err
		}

		// Update the properties map as we have a modified copy of the child schema.
		schema.Properties[key] = propSchema
	default:
		return fmt.Errorf("%w: desired path %s, path %s is not an object", errPathNotFound, desiredPath, currentPath)
	}

	return nil
}

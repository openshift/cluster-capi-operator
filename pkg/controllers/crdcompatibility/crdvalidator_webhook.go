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

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/crdchecker"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

var (
	errExpectedCRD           = errors.New("expected a CustomResourceDefinition")
	errCRDHasRequirements    = errors.New("cannot delete CRD because it has CRDCompatibilityRequirements")
	errCRDNotCompatible      = errors.New("CRD is not compatible with CRDCompatibilityRequirements")
	errUnknownCRDAdmitAction = errors.New("unknown CRDAdmitAction")
)

type requirement struct {
	CRD         *apiextensionsv1.CustomResourceDefinition
	Requirement *operatorv1alpha1.CRDCompatibilityRequirement
}

type crdValidator struct {
	client client.Reader
}

var _ admission.CustomValidator = &crdValidator{}

func (v *crdValidator) validateCreateOrUpdate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errExpectedCRD, obj)
	}

	crdCompatibilityRequirements := operatorv1alpha1.CRDCompatibilityRequirementList{}
	if err := v.client.List(ctx, &crdCompatibilityRequirements, &client.ListOptions{FieldSelector: fields.SelectorFromSet(fields.Set{fieldIndexCRDRef: crd.GetName()})}); err != nil {
		return nil, fmt.Errorf("failed to list CRDCompatibilityRequirements: %w for CRD %q", err, crd.GetName())
	}

	var (
		allReqErrors   []string
		allReqWarnings []string
	)

	for _, crdCompatibilityRequirement := range crdCompatibilityRequirements.Items {
		compatibilityCRD := &apiextensionsv1.CustomResourceDefinition{}
		if err := yaml.Unmarshal([]byte(crdCompatibilityRequirement.Spec.CompatibilityCRD), compatibilityCRD); err != nil {
			return nil, fmt.Errorf("failed to parse compatibilityCRD for CRDCompatibilityRequirement %q: %w", crdCompatibilityRequirement.Name, err)
		}

		reqErrors, reqWarnings, err := crdchecker.CheckCRDCompatibility(compatibilityCRD, crd)
		if err != nil {
			return nil, fmt.Errorf("failed to check CRD compatibility: %w", err)
		}

		prependName := func(s string) string {
			return fmt.Sprintf("This requirement was added by %s: requirement %s: %s", crdCompatibilityRequirement.Spec.CreatorDescription, crdCompatibilityRequirement.Name, s)
		}

		switch crdCompatibilityRequirement.Spec.CRDAdmitAction {
		case operatorv1alpha1.CRDAdmitActionWarn:
			allReqWarnings = append(allReqWarnings, util.SliceMap(reqErrors, prependName)...)
		case operatorv1alpha1.CRDAdmitActionEnforce:
			allReqErrors = append(allReqErrors, util.SliceMap(reqErrors, prependName)...)
		default:
			return nil, fmt.Errorf("%w: %q for requirement %s", errUnknownCRDAdmitAction, crdCompatibilityRequirement.Spec.CRDAdmitAction, crdCompatibilityRequirement.Name)
		}

		allReqWarnings = append(allReqWarnings, util.SliceMap(reqWarnings, prependName)...)
	}

	if len(allReqErrors) > 0 {
		return allReqWarnings, fmt.Errorf("%w: %s", errCRDNotCompatible, strings.Join(allReqErrors, "\n"))
	}

	return allReqWarnings, nil
}

// ValidateCreate validates a Create event for a CustomResourceDefinition.
func (v *crdValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(ctx, obj)
}

// ValidateUpdate validates an Update event for a CustomResourceDefinition.
func (v *crdValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(ctx, newObj)
}

// ValidateDelete validates a Delete event for a CustomResourceDefinition.
func (v *crdValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errExpectedCRD, obj)
	}

	crdCompatibilityRequirements := operatorv1alpha1.CRDCompatibilityRequirementList{}
	if err := v.client.List(ctx, &crdCompatibilityRequirements, &client.ListOptions{FieldSelector: fields.SelectorFromSet(fields.Set{fieldIndexCRDRef: crd.GetName()})}); err != nil {
		return nil, fmt.Errorf("failed to list CRDCompatibilityRequirements: %w for CRD %q", err, crd.GetName())
	}

	if len(crdCompatibilityRequirements.Items) > 0 {
		names := []string{}
		for _, crdCompatibilityRequirement := range crdCompatibilityRequirements.Items {
			names = append(names, crdCompatibilityRequirement.Name)
		}

		return nil, fmt.Errorf("%w: %s", errCRDHasRequirements, strings.Join(names, ", "))
	}

	return nil, nil
}

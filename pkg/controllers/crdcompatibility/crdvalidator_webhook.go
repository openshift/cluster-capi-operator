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
	"maps"
	"slices"
	"strings"
	"sync"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/openshift/cluster-capi-operator/pkg/crdchecker"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

var (
	errExpectedCRD        = errors.New("expected a CustomResourceDefinition")
	errCRDHasRequirements = errors.New("cannot delete CRD because it has CRDCompatibilityRequirements")
	errCRDNotCompatible   = errors.New("CRD is not compatible with CRDCompatibilityRequirements")
)

type crdValidator struct {
	client client.Client

	requirements     map[string]map[string]*apiextensionsv1.CustomResourceDefinition
	requirementsLock sync.RWMutex
}

var _ admission.CustomValidator = &crdValidator{}

func (v *crdValidator) updateRequirements(crdRef string, fn func(requirements map[string]*apiextensionsv1.CustomResourceDefinition)) {
	v.requirementsLock.Lock()
	defer v.requirementsLock.Unlock()

	if v.requirements == nil {
		v.requirements = make(map[string]map[string]*apiextensionsv1.CustomResourceDefinition)
	}

	if v.requirements[crdRef] == nil {
		v.requirements[crdRef] = make(map[string]*apiextensionsv1.CustomResourceDefinition)
	}

	fn(v.requirements[crdRef])
}

func (v *crdValidator) setRequirement(crdRef string, requirementName string, crd *apiextensionsv1.CustomResourceDefinition) {
	v.updateRequirements(crdRef, func(requirements map[string]*apiextensionsv1.CustomResourceDefinition) {
		requirements[requirementName] = crd
	})
}

func (v *crdValidator) unsetRequirement(crdRef string, requirementName string) {
	v.updateRequirements(crdRef, func(requirements map[string]*apiextensionsv1.CustomResourceDefinition) {
		delete(requirements, requirementName)
	})
}

func (v *crdValidator) validateCreateOrUpdate(obj runtime.Object) (admission.Warnings, error) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errExpectedCRD, obj)
	}

	// We don't need to hold a lock while we use the requirement because nothing
	// mutates the requirements themselves.
	v.requirementsLock.RLock()
	defer v.requirementsLock.RUnlock()

	var (
		allReqErrors   []string
		allReqWarnings []string
	)

	for name, requirement := range v.requirements[crd.Name] {
		reqErrors, reqWarnings, err := crdchecker.CheckCRDCompatibility(requirement, crd)
		if err != nil {
			return nil, fmt.Errorf("failed to check CRD compatibility: %w", err)
		}

		prependName := func(s string) string {
			return fmt.Sprintf("requirement %s: %s", name, s)
		}
		allReqErrors = append(allReqErrors, util.SliceMap(reqErrors, prependName)...)
		allReqWarnings = append(allReqWarnings, util.SliceMap(reqWarnings, prependName)...)
	}

	if len(allReqErrors) > 0 {
		return nil, fmt.Errorf("%w: %s", errCRDNotCompatible, strings.Join(allReqErrors, "\n"))
	}

	return allReqWarnings, nil
}

// ValidateCreate validates a Create event for a CustomResourceDefinition.
func (v *crdValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(obj)
}

// ValidateUpdate validates an Update event for a CustomResourceDefinition.
func (v *crdValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(newObj)
}

// ValidateDelete validates a Delete event for a CustomResourceDefinition.
func (v *crdValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("%w: got %T", errExpectedCRD, obj)
	}

	// A CRD may not be deleted if it has requirements
	v.requirementsLock.RLock()
	defer v.requirementsLock.RUnlock()

	if requirements := v.requirements[crd.Name]; len(requirements) > 0 {
		return nil, fmt.Errorf("%w: %s", errCRDHasRequirements, strings.Join(slices.Collect(maps.Keys(requirements)), ", "))
	}

	return nil, nil
}

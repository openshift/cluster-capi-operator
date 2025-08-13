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
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/crdchecker"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

type crdValidator struct {
	client client.Client

	requirements     map[string]map[string]*apiextensionsv1.CustomResourceDefinition
	requirementsLock sync.RWMutex
}

var _ admission.CustomValidator = &crdValidator{}

func (v *crdValidator) setRequirement(crdRef string, crd *apiextensionsv1.CustomResourceDefinition) {
	v.requirementsLock.Lock()
	defer v.requirementsLock.Unlock()

	if v.requirements == nil {
		v.requirements = make(map[string]map[string]*apiextensionsv1.CustomResourceDefinition)
	}

	if v.requirements[crdRef] == nil {
		v.requirements[crdRef] = make(map[string]*apiextensionsv1.CustomResourceDefinition)
	}

	if crd == nil {
		delete(v.requirements[crdRef], crdRef)
	} else {
		v.requirements[crdRef][crd.Name] = crd
	}
}

func (v *crdValidator) validateCreateOrUpdate(obj runtime.Object) (admission.Warnings, error) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("expected a CustomResourceDefinition, got %T", obj)
	}

	// We don't need to hold a lock while we use the requirement because nothing
	// mutates the requirements themselves.
	v.requirementsLock.RLock()
	defer v.requirementsLock.RUnlock()

	var allReqErrors []string
	var allReqWarnings []string

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
		return nil, fmt.Errorf("new CRD is not compatible with the following: %s", strings.Join(allReqErrors, "\n"))
	}

	return allReqWarnings, nil
}

func (v *crdValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(obj)
}

func (v *crdValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	return v.validateCreateOrUpdate(newObj)
}

func (v *crdValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("expected a CustomResourceDefinition, got %T", obj)
	}

	// A CRD may not be deleted if it has requirements
	v.requirementsLock.RLock()
	defer v.requirementsLock.RUnlock()

	if requirements := v.requirements[crd.Name]; len(requirements) > 0 {
		return nil, fmt.Errorf("cannot delete CRD because it has the following CRDCompatibilityRequirements: %s", strings.Join(slices.Collect(maps.Keys(requirements)), ", "))
	}

	return nil, nil
}

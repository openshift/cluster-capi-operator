/*
Copyright 2026 Red Hat, Inc.

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

package runtimetransformer

import (
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

// ValidateTransformers calls Validate on each transformer for every object in the revision.
// All errors are collected and returned together via errors.Join.
func ValidateTransformers(transformers []RuntimeTransformer, rev revisiongenerator.ParsedRevision) error {
	var allErrs []error

	for _, component := range rev.Components() {
		for _, obj := range component.Objects() {
			for _, t := range transformers {
				if err := t.Validate(obj); err != nil {
					allErrs = append(allErrs, fmt.Errorf("%s %s: %w", component.Name(), client.ObjectKeyFromObject(obj), err))
				}
			}
		}
	}

	return errors.Join(allErrs...)
}

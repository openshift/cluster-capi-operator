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

package crdchecker

import (
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"sigs.k8s.io/crdify/pkg/config"
	"sigs.k8s.io/crdify/pkg/runner"
)

// CheckCRDCompatibility checks if the target CRD is compatible with the requirement CRD.
// It returns a list of errors and warnings.
// If there are no errors, the target CRD is compatible with the requirement CRD.
// If there are errors, the target CRD is not compatible with the requirement CRD.
// If there are warnings, the target CRD is compatible with the requirement CRD, but there are some potential issues.
func CheckCRDCompatibility(requirement, target *apiextensionsv1.CustomResourceDefinition) ([]string, []string, error) {
	cfg := &config.Config{}

	run, err := runner.New(cfg, runner.DefaultRegistry())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create crdify runner: %w", err)
	}

	results := run.Run(requirement, target)

	var errors []string
	var warnings []string
	for _, result := range results.CRDValidation {
		errors = append(errors, result.Errors...)
		warnings = append(warnings, result.Warnings...)
	}
	return errors, warnings, nil
}

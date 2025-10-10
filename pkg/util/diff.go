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
package util

import (
	"fmt"
	"strings"

	"github.com/go-test/deep"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// DiffResult is the interface that represents the result of a diff operation.
type DiffResult interface {
	Changed() bool
	String() string
}

type diffResult struct {
	diff []string
}

// Changed returns true if the diff detected any changes.
func (d *diffResult) Changed() bool {
	return len(d.diff) > 0
}

// String returns the diff as a string.
func (d *diffResult) String() string {
	return strings.Join(d.diff, ", ")
}

type differ struct {
	ignoreConditionsLastTransitionTime bool
	ignoredPath                        [][]string
}

// Diff compares the objects a and b, and returns a DiffResult.
func (d *differ) Diff(a, b any) (DiffResult, error) {
	unstructuredA, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&a)
	if err != nil {
		return nil, fmt.Errorf("failed to convert b to unstructured: %w", err)
	}

	unstructuredB, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&b)
	if err != nil {
		return nil, fmt.Errorf("failed to convert b to unstructured: %w", err)
	}

	var additionalIgnoredPaths [][]string

	if d.ignoreConditionsLastTransitionTime {
		if err := removeConditionsLastTransitionTime(unstructuredA); err != nil {
			return nil, fmt.Errorf("failed to remove conditions last transition time from a: %w", err)
		}

		if err := removeConditionsLastTransitionTime(unstructuredB); err != nil {
			return nil, fmt.Errorf("failed to remove conditions last transition time from b: %w", err)
		}
	}

	// Remove fields that we want to ignore.
	for _, ignorePath := range append(d.ignoredPath, additionalIgnoredPaths...) {
		unstructured.RemoveNestedField(unstructuredA, ignorePath...)
		unstructured.RemoveNestedField(unstructuredB, ignorePath...)
	}

	diff := deep.Equal(unstructuredA, unstructuredB)

	// Make the result deterministic.
	sort.Strings(diff)

	return &diffResult{diff: diff}, nil
}

func removeConditionsLastTransitionTime(a map[string]interface{}) error {
	conditionPaths := [][]string{
		{"conditions"},
		{"v1beta2", "conditions"},
		{"deprecated", "v1beta1", "conditions"},
	}

	for _, conditionPath := range conditionPaths {
		conditions, found, err := unstructured.NestedSlice(a, conditionPath...)
		if !found || err != nil {
			continue
		}

		for i, condition := range conditions {
			conditionMap, ok := condition.(map[string]interface{})
			if !ok {
				continue
			}

			conditionMap["lastTransitionTime"] = "ignored"
			conditions[i] = conditionMap
		}

		if err := unstructured.SetNestedField(a, conditions, conditionPath...); err != nil {
			return fmt.Errorf("failed to set nested field %s: %w", strings.Join(conditionPath, "."), err)
		}
	}

	return nil
}

type diffopts func(*differ)

// NewDiffer creates a new differ with the given options.
func NewDiffer(opts ...diffopts) *differ {
	d := &differ{}
	for _, opt := range opts {
		opt(d)
	}

	return d
}

// WithIgnoreField adds a path to the list of paths to ignore when executing Diff.
func WithIgnoreField(path ...string) diffopts {
	return func(d *differ) {
		d.ignoredPath = append(d.ignoredPath, path)
	}
}

// WithIgnoreConditionsLastTransitionTime configures the differ to ignore LastTransitionTime for conditions when executing Diff.
func WithIgnoreConditionsLastTransitionTime() diffopts {
	return func(d *differ) {
		d.ignoreConditionsLastTransitionTime = true
	}
}

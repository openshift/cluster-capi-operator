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
	"errors"
	"fmt"
	"sort"
	"strings"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/go-test/deep"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var errTimedOutWaitingForFeatureGates = errors.New("objects to diff cannot be nil")

// DiffResult is the interface that represents the result of a diff operation.
type DiffResult interface {
	HasChanges() bool
	String() string
	HasMetadataChanges() bool
	HasSpecChanges() bool
	HasProviderSpecChanges() bool
	HasStatusChanges() bool
}

type diffResult struct {
	diff             map[string][]string
	providerSpecPath string
}

// HasChanges returns true if the diff detected any changes.
func (d *diffResult) HasChanges() bool {
	return len(d.diff) > 0
}

// HasMetadataChanges returns true if the diff detected any changes to the metadata.
func (d *diffResult) HasMetadataChanges() bool {
	_, ok := d.diff["metadata"]
	return ok
}

// HasSpecChanges returns true if the diff detected any changes to the spec.
func (d *diffResult) HasSpecChanges() bool {
	_, ok := d.diff["spec"]
	return ok
}

// HasProviderSpecChanges returns true if the diff detected any changes to the providerSpec.
func (d *diffResult) HasProviderSpecChanges() bool {
	if d.providerSpecPath == "" {
		return false
	}

	_, ok := d.diff[d.providerSpecPath]

	return ok
}

// HasStatusChanges returns true if the diff detected any changes to the status.
func (d *diffResult) HasStatusChanges() bool {
	_, ok := d.diff["status"]
	return ok
}

// String returns the diff as a string.
func (d *diffResult) String() string {
	if !d.HasChanges() {
		return ""
	}

	diff := []string{}

	for k, v := range d.diff {
		for _, d := range v {
			if strings.Contains(d, "]: ") {
				diff = append(diff, fmt.Sprintf("[%s].%s", k, d))
			} else {
				diff = append(diff, fmt.Sprintf("[%s]: %s", k, d))
			}
		}
	}

	sort.Strings(diff)

	out := "." + strings.Join(diff, ", .")
	out = strings.ReplaceAll(out, ".slice[", "[")
	out = strings.ReplaceAll(out, "map[", "[")

	return out
}

type differ struct {
	ignoreConditionsLastTransitionTime bool
	modifyFuncs                        map[string]func(obj map[string]interface{}) error
	ignoredPath                        [][]string
	providerSpecPath                   string
}

// Diff compares the objects a and b, and returns a DiffResult.
//
//nolint:funlen
func (d *differ) Diff(a, b client.Object) (DiffResult, error) {
	if a == nil || b == nil {
		return nil, errTimedOutWaitingForFeatureGates
	}

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

	for funcName, modifyFunc := range d.modifyFuncs {
		if err := modifyFunc(unstructuredA); err != nil {
			return nil, fmt.Errorf("failed to run modify function %s on a: %w", funcName, err)
		}

		if err := modifyFunc(unstructuredB); err != nil {
			return nil, fmt.Errorf("failed to run modify function %son b: %w", funcName, err)
		}
	}
	// Remove fields that we want to ignore.
	for _, ignorePath := range append(d.ignoredPath, additionalIgnoredPaths...) {
		unstructured.RemoveNestedField(unstructuredA, ignorePath...)
		unstructured.RemoveNestedField(unstructuredB, ignorePath...)
	}

	allKeys := sets.Set[string]{}

	for k := range unstructuredA {
		allKeys.Insert(k)
	}

	for k := range unstructuredB {
		allKeys.Insert(k)
	}

	diff := map[string][]string{}

	for k := range allKeys {
		d := deep.Equal(unstructuredA[k], unstructuredB[k])

		// Make the result deterministic.
		sort.Strings(d)

		if len(d) > 0 {
			diff[k] = d
		}
	}

	return &diffResult{diff: diff, providerSpecPath: d.providerSpecPath}, nil
}

func removeConditionsLastTransitionTime(a map[string]interface{}) error {
	conditionPaths := [][]string{
		{"status", "conditions"},
		{"status", "v1beta2", "conditions"},
		{"status", "deprecated", "v1beta1", "conditions"},
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

func newDiffer(opts ...diffopts) *differ {
	d := &differ{
		modifyFuncs: map[string]func(obj map[string]interface{}) error{},
	}
	for _, opt := range opts {
		opt(d)
	}

	return d
}

// NewDefaultDiffer creates a new default differ with the default options.
func NewDefaultDiffer(opts ...diffopts) *differ {
	return newDiffer(append(opts,
		// Options for handling of metadata fields.

		// Special handling for CAPI's conversion-data label.
		WithIgnoreField("metadata", "annotations", "cluster.x-k8s.io/conversion-data"),

		WithIgnoreField("metadata", "name"),
		WithIgnoreField("metadata", "generateName"),
		WithIgnoreField("metadata", "namespace"),
		WithIgnoreField("metadata", "selfLink"),
		WithIgnoreField("metadata", "uid"),
		WithIgnoreField("metadata", "resourceVersion"),
		WithIgnoreField("metadata", "generation"),
		WithIgnoreField("metadata", "creationTimestamp"),
		WithIgnoreField("metadata", "deletionTimestamp"),
		WithIgnoreField("metadata", "deletionGracePeriodSeconds"),
		WithIgnoreField("metadata", "finalizers"),
		WithIgnoreField("metadata", "managedFields"),

		// Options for handling of status fields.
		WithIgnoreConditionsLastTransitionTime(),
	)...)
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
		d.modifyFuncs["RemoveConditionsLastTransitionTime"] = removeConditionsLastTransitionTime
	}
}

// WithProviderSpec configures the differ to separately diff .spec.providerSpec.
func WithProviderSpec(platform configv1.PlatformType, path []string, marshalProviderSpec func(platform configv1.PlatformType, rawExtension *runtime.RawExtension) (any, error)) diffopts {
	return func(d *differ) {
		d.providerSpecPath = strings.Join(path, ".")

		d.modifyFuncs["ProviderSpec"] = func(obj map[string]interface{}) error {
			rawExtensionMap, found, err := unstructured.NestedMap(obj, path...)
			if !found || err != nil {
				return fmt.Errorf("failed to get providerSpec value: %w", err)
			}

			rawExtension := &runtime.RawExtension{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(rawExtensionMap, rawExtension); err != nil {
				return fmt.Errorf("failed to convert providerSpec value map to raw extension: %w", err)
			}

			providerSpec, err := marshalProviderSpec(platform, rawExtension)
			if err != nil {
				return fmt.Errorf("failed to marshal providerSpec: %w", err)
			}

			// unset the nested field
			unstructured.RemoveNestedField(obj, path...)
			// add it as top-level field
			if err := unstructured.SetNestedField(obj, providerSpec, d.providerSpecPath); err != nil {
				return fmt.Errorf("failed to set nested field %s: %w", d.providerSpecPath, err)
			}

			return nil
		}
	}
}

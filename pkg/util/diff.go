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

	"github.com/go-test/deep"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

type UnstructuredDiffer[T any] struct {
	customDiff   []func(a, b T) ([]string, error)
	ignoreFields [][]string
}

func (d *UnstructuredDiffer[T]) Diff(a, b T) (map[string]any, error) {
	diffs := map[string]any{}

	for i, customDiff := range d.customDiff {
		diff, err := customDiff(a, b)
		if err != nil {
			return nil, err
		}

		if len(diff) > 0 {
			diffs[fmt.Sprintf("customDiff.%d", i)] = diff
		}
	}

	unstructuredA, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&a)
	if err != nil {
		return nil, fmt.Errorf("failed to convert b to unstructured: %w", err)
	}

	unstructuredB, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&b)
	if err != nil {
		return nil, fmt.Errorf("failed to convert b to unstructured: %w", err)
	}

	for _, nestedField := range d.ignoreFields {
		unstructured.RemoveNestedField(unstructuredA, nestedField...)
		unstructured.RemoveNestedField(unstructuredB, nestedField...)
	}

	diff := deep.Equal(unstructuredA, unstructuredB)
	if len(diff) > 0 {
		diffs["deep.Equal"] = diff
	}

	return diffs, nil
}

type diffopts[T any] func(*UnstructuredDiffer[T])

func NewUnstructuredDiffer[T any](opts ...diffopts[T]) *UnstructuredDiffer[T] {
	d := &UnstructuredDiffer[T]{}
	for _, opt := range opts {
		opt(d)
	}

	return d
}

func WithIgnoreField[T any](path ...string) diffopts[T] {
	return func(d *UnstructuredDiffer[T]) {
		d.ignoreFields = append(d.ignoreFields, path)
	}
}

func WithCustomDiff[T any](diff func(a, b T) ([]string, error)) diffopts[T] {
	return func(d *UnstructuredDiffer[T]) {
		d.customDiff = append(d.customDiff, diff)
	}
}

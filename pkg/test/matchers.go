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
package test

import (
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
)

// k8sNotFoundMatcher is a matcher that checks if an error is a NotFound error.
type k8sNotFoundMatcher struct{}

// BeK8SNotFound is a gomega matcher that checks if an error is a NotFound error
// returned by the Kubernetes API.
func BeK8SNotFound() types.GomegaMatcher {
	return &k8sNotFoundMatcher{}
}

// Match checks if the actual value is a Kubernetes NotFound error.
func (m *k8sNotFoundMatcher) Match(actual interface{}) (bool, error) {
	err, ok := actual.(error)
	if !ok {
		return false, fmt.Errorf("BeK8SNotFound matcher expects an error, but got %T", actual)
	}

	return apierrors.IsNotFound(err), nil
}

// FailureMessage returns a descriptive message when the matcher fails.
func (m *k8sNotFoundMatcher) FailureMessage(actual interface{}) string {
	if err, ok := actual.(error); ok {
		return fmt.Sprintf("Expected error to be a Kubernetes NotFound error, but got:\n\t%v", err)
	}

	return fmt.Sprintf("Expected a Kubernetes NotFound error, but got %T: %v", actual, actual)
}

// NegatedFailureMessage returns a message for the negated matcher.
func (m *k8sNotFoundMatcher) NegatedFailureMessage(actual interface{}) string {
	if err, ok := actual.(error); ok {
		return fmt.Sprintf("Expected error to NOT be a Kubernetes NotFound error, but it is:\n\t%v", err)
	}

	return "Expected value to be something other than a Kubernetes NotFound error"
}

// MatchViaDiff is a gomega matcher that checks if the actual object is equal to the expected object by using cmp.Diff.
// This is useful for complex objects where you want to focus on a small subset of the object when there is a difference.
func MatchViaDiff(expected any) types.GomegaMatcher {
	return gomega.WithTransform(func(actual any) string {
		return cmp.Diff(expected, actual)
	}, gomega.BeEmpty())
}

// IgnoreFields is a gomega matcher that ignores the specified fields in the object.
func IgnoreFields(fields []string, matcher types.GomegaMatcher) types.GomegaMatcher {
	return gomega.WithTransform(func(obj map[string]interface{}) map[string]interface{} {
		fieldSet := sets.New(fields...)

		newObj := map[string]interface{}{}

		// Copy across all fields that are not ignored so that we don't mutate the original object.
		for k, v := range obj {
			if !fieldSet.Has(k) {
				newObj[k] = v
			}
		}

		return newObj
	}, matcher)
}

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
package matchers

import (
	"errors"
	"fmt"

	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// errActualTypeMismatchCAPICondition is used when the type of the actual object does not match the expected type of clusterv1.Condition.
var errActualTypeMismatchCAPICondition = errors.New("actual should be of type clusterv1.Condition")

// MatchCAPICondition returns a custom matcher to check equality of clusterv1.Condition.
// It follows the same pattern as testutils.MatchCondition but for CAPI v1beta1.Condition types.
// Note: This matcher deliberately ignores LastTransitionTime field
// as it may vary between conversions and fuzzing.
func MatchCAPICondition(expected clusterv1.Condition) types.GomegaMatcher {
	return &matchCAPICondition{
		expected: expected,
	}
}

type matchCAPICondition struct {
	expected clusterv1.Condition
}

// Match checks for equality between the actual and expected objects.
func (m matchCAPICondition) Match(actual interface{}) (success bool, err error) {
	actualCondition, ok := actual.(clusterv1.Condition)
	if !ok {
		return false, errActualTypeMismatchCAPICondition
	}

	ok, err = gomega.Equal(m.expected.Type).Match(actualCondition.Type)
	if !ok {
		return false, fmt.Errorf("condition type does not match: %w", err)
	}

	ok, err = gomega.Equal(m.expected.Status).Match(actualCondition.Status)
	if !ok {
		return false, fmt.Errorf("condition status does not match: %w", err)
	}

	ok, err = gomega.Equal(m.expected.Reason).Match(actualCondition.Reason)
	if !ok {
		return false, fmt.Errorf("condition reason does not match: %w", err)
	}

	ok, err = gomega.Equal(m.expected.Message).Match(actualCondition.Message)
	if !ok {
		return false, fmt.Errorf("condition message does not match: %w", err)
	}

	ok, err = gomega.Equal(m.expected.Severity).Match(actualCondition.Severity)
	if !ok {
		return false, fmt.Errorf("condition severity does not match: %w", err)
	}

	// Note: We deliberately ignore LastTransitionTime field
	// as it may vary between conversions and fuzzing

	return true, nil
}

// FailureMessage is the message returned to the test when the actual and expected
// objects do not match.
func (m matchCAPICondition) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected\n\t%#v\nto match\n\t%#v\n", actual, m.expected)
}

// NegatedFailureMessage is the negated version of the FailureMessage.
func (m matchCAPICondition) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected\n\t%#v\nto not match\n\t%#v\n", actual, m.expected)
}

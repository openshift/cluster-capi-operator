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
//nolint:dupl
package matchers

import (
	"errors"
	"fmt"

	"github.com/onsi/gomega/types"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// errActualTypeMismatchMAPICondition is used when the type of the actual object does not match the expected type of mapiv1.Condition.
var errActualTypeMismatchMAPICondition = errors.New("actual should be of type mapiv1.Condition")

// convertMAPIConditionToMetav1Condition converts a mapiv1.Condition to metav1.Condition.
// Note: This conversion deliberately ignores the Severity field as metav1.Condition
// does not have a Severity field.
func convertMAPIConditionToMetav1Condition(mapiCondition mapiv1.Condition) metav1.Condition {
	return metav1.Condition{
		Type:               string(mapiCondition.Type),
		Status:             metav1.ConditionStatus(mapiCondition.Status),
		Reason:             mapiCondition.Reason,
		Message:            mapiCondition.Message,
		LastTransitionTime: mapiCondition.LastTransitionTime,
		// Note: We deliberately ignore the Severity field
		// as it is not supported by metav1.Condition.
	}
}

// MatchMAPICondition returns a custom matcher to check equality of mapiv1.Condition.
// It converts the MAPI condition to metav1.Condition and delegates to testutils.MatchCondition.
// Note: This matcher deliberately ignores LastTransitionTime and the Severity field
// as it is not supported by metav1.Condition.
func MatchMAPICondition(expected mapiv1.Condition) types.GomegaMatcher {
	return &matchMAPICondition{
		expected: expected,
	}
}

type matchMAPICondition struct {
	expected mapiv1.Condition
}

// Match checks for equality between the actual and expected objects.
func (m matchMAPICondition) Match(actual interface{}) (success bool, err error) {
	actualCondition, ok := actual.(mapiv1.Condition)
	if !ok {
		return false, errActualTypeMismatchMAPICondition
	}

	// Convert both expected and actual MAPI conditions to metav1.Condition
	expectedMetav1Condition := convertMAPIConditionToMetav1Condition(m.expected)
	actualMetav1Condition := convertMAPIConditionToMetav1Condition(actualCondition)

	// Delegate to the original metav1.Condition matcher
	success, err = testutils.MatchCondition(expectedMetav1Condition).Match(actualMetav1Condition)
	if err != nil {
		return false, fmt.Errorf("condition matching failed: %w", err)
	}

	return success, nil
}

// FailureMessage is the message returned to the test when the actual and expected
// objects do not match.
func (m matchMAPICondition) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected\n\t%#v\nto match\n\t%#v\n", actual, m.expected)
}

// NegatedFailureMessage is the negated version of the FailureMessage.
func (m matchMAPICondition) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("expected\n\t%#v\nto not match\n\t%#v\n", actual, m.expected)
}

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
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// errActualTypeMismatchCAPICondition is used when the type of the actual object does not match the expected type of clusterv1.Condition.
var errActualTypeMismatchCAPICondition = errors.New("actual should be of type clusterv1.Condition")

// convertCAPIConditionToMetav1Condition converts a clusterv1.Condition to metav1.Condition.
// Note: This conversion deliberately ignores the Severity field as metav1.Condition
// does not have a Severity field.
func convertCAPIConditionToMetav1Condition(capiCondition clusterv1.Condition) metav1.Condition {
	return metav1.Condition{
		Type:               string(capiCondition.Type),
		Status:             metav1.ConditionStatus(capiCondition.Status),
		Reason:             capiCondition.Reason,
		Message:            capiCondition.Message,
		LastTransitionTime: capiCondition.LastTransitionTime,
		// Note: We deliberately ignore the Severity field
		// as it is not supported by metav1.Condition.
	}
}

// MatchCAPICondition returns a custom matcher to check equality of clusterv1.Condition.
// It converts the CAPI condition to metav1.Condition and delegates to testutils.MatchCondition.
// Note: This matcher deliberately ignores the Severity field
// as it is not supported by metav1.Condition.
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

	// Convert both expected and actual CAPI conditions to metav1.Condition
	expectedMetav1Condition := convertCAPIConditionToMetav1Condition(m.expected)
	actualMetav1Condition := convertCAPIConditionToMetav1Condition(actualCondition)

	// Delegate to the original metav1.Condition matcher
	success, err = testutils.MatchCondition(expectedMetav1Condition).Match(actualMetav1Condition)
	if err != nil {
		return false, fmt.Errorf("condition matching failed: %w", err)
	}

	return success, nil
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

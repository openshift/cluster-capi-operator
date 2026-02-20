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
	"fmt"
	"reflect"

	"github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"
)

// HaveCondition returns a matcher that checks if a slice of condition structs
// contains a condition with the specified type. It uses reflection to work with
// any condition-like struct that has Type, Status, Reason, Message, and optionally
// LastTransitionTime fields.
//
// Supported condition types include:
//   - configv1.ClusterOperatorStatusCondition
//   - metav1.Condition
//   - clusterv1.Condition (v1beta1 and v1beta2)
//   - Any struct with Type, Status, Reason, Message fields
//
// Usage:
//
//	Expect(co.Status.Conditions).To(HaveCondition("Progressing"))
//
//	Expect(co.Status.Conditions).To(HaveCondition("Progressing").
//	    WithStatus(configv1.ConditionTrue).
//	    WithReason("EphemeralError"))
//
//	// With custom matchers:
//	Expect(co.Status.Conditions).To(HaveCondition("Progressing").
//	    WithStatus(configv1.ConditionTrue).
//	    WithLastTransitionTime(Equal(expectedTime)))
func HaveCondition[T ~string](conditionType T) *ConditionMatcher {
	return &ConditionMatcher{
		conditionType: string(conditionType),
	}
}

const (
	conditionFieldType               = "Type"
	conditionFieldStatus             = "Status"
	conditionFieldReason             = "Reason"
	conditionFieldMessage            = "Message"
	conditionFieldLastTransitionTime = "LastTransitionTime"
)

// ConditionMatcher is a Gomega matcher for condition slices.
// It uses reflection to work with any condition-like struct.
type ConditionMatcher struct {
	conditionType          string
	statusMatcher          types.GomegaMatcher
	reasonMatcher          types.GomegaMatcher
	messageMatcher         types.GomegaMatcher
	lastTransitionMatcher  types.GomegaMatcher
	failureField           string
	failureActual          interface{}
	failureExpectedMatcher types.GomegaMatcher
}

// WithStatus adds a status check to the matcher.
// Accepts either a status value (string or typed) or a types.GomegaMatcher.
func (m *ConditionMatcher) WithStatus(expected interface{}) *ConditionMatcher {
	m.statusMatcher = toMatcher(expected)
	return m
}

// WithReason adds a reason check to the matcher.
// Accepts either a string value or a types.GomegaMatcher.
func (m *ConditionMatcher) WithReason(expected interface{}) *ConditionMatcher {
	m.reasonMatcher = toMatcher(expected)
	return m
}

// WithMessage adds a message check to the matcher.
// Accepts either a string value or a types.GomegaMatcher.
func (m *ConditionMatcher) WithMessage(expected interface{}) *ConditionMatcher {
	m.messageMatcher = toMatcher(expected)
	return m
}

// WithLastTransitionTime adds a LastTransitionTime check to the matcher.
// Accepts either a time value or a types.GomegaMatcher (e.g., Equal, BeTemporally with WithTransform).
func (m *ConditionMatcher) WithLastTransitionTime(expected interface{}) *ConditionMatcher {
	m.lastTransitionMatcher = toMatcher(expected)
	return m
}

// derefValue dereferences pointer and interface reflect.Values to reach the
// underlying value. Returns the value unchanged if it is not a pointer or
// interface. Returns an invalid reflect.Value if a nil pointer is encountered.
func derefValue(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}
		}

		v = v.Elem()
	}

	return v
}

// findCondition searches for a condition with the given type in the slice.
// Returns the condition's reflect.Value and true if found, or an invalid Value and false if not found.
// Returns an error if the slice elements are not valid condition structs.
// Slice elements may be structs or pointers to structs.
func findCondition(conditionSlice reflect.Value, conditionType string) (reflect.Value, bool, error) {
	for i := 0; i < conditionSlice.Len(); i++ {
		elem := derefValue(conditionSlice.Index(i))
		if elem.Kind() != reflect.Struct {
			return reflect.Value{}, false, fmt.Errorf("condition element at index %d is not a struct", i)
		}

		typeField := elem.FieldByName(conditionFieldType)
		if !typeField.IsValid() {
			return reflect.Value{}, false, fmt.Errorf("condition element at index %d does not have a %s field", i, conditionFieldType)
		}

		if getStringValue(typeField) == conditionType {
			return elem, true, nil
		}
	}

	return reflect.Value{}, false, nil
}

// Match implements types.GomegaMatcher.
func (m *ConditionMatcher) Match(actual interface{}) (bool, error) {
	// Verify actual is a slice
	actualValue := reflect.ValueOf(actual)
	if actualValue.Kind() != reflect.Slice {
		return false, fmt.Errorf("HaveCondition expects a slice of conditions, got %T", actual)
	}

	condition, found, err := findCondition(actualValue, m.conditionType)
	if err != nil {
		return false, err
	}

	if !found {
		m.failureField = conditionFieldType
		m.failureActual = nil

		return false, nil
	}

	for _, matchField := range []struct {
		matcher types.GomegaMatcher
		name    string
	}{
		{matcher: m.statusMatcher, name: conditionFieldStatus},
		{matcher: m.reasonMatcher, name: conditionFieldReason},
		{matcher: m.messageMatcher, name: conditionFieldMessage},
		{matcher: m.lastTransitionMatcher, name: conditionFieldLastTransitionTime},
	} {
		if matchField.matcher != nil {
			field := condition.FieldByName(matchField.name)
			if !field.IsValid() {
				return false, fmt.Errorf("condition does not have a %s field", matchField.name)
			}

			fieldVal := derefValue(field)
			if !fieldVal.IsValid() {
				return false, fmt.Errorf("condition field %s is nil", matchField.name)
			}

			fieldValue := fieldVal.Interface()

			ok, err := matchField.matcher.Match(fieldValue)
			if err != nil {
				return false, err
			}

			if !ok {
				m.failureField = matchField.name
				m.failureActual = fieldValue
				m.failureExpectedMatcher = matchField.matcher

				return false, nil
			}
		}
	}

	return true, nil
}

// FailureMessage implements types.GomegaMatcher.
func (m *ConditionMatcher) FailureMessage(actual interface{}) string {
	if m.failureField == conditionFieldType {
		return fmt.Sprintf("Expected conditions to contain a condition with Type %q, but it was not found.\nConditions: %s",
			m.conditionType, format.Object(actual, 1))
	}

	return fmt.Sprintf("Conditions: %s\nCondition %q field %s mismatch:\n%s",
		format.Object(actual, 1),
		m.conditionType,
		m.failureField,
		m.failureExpectedMatcher.FailureMessage(m.failureActual))
}

// NegatedFailureMessage implements types.GomegaMatcher.
func (m *ConditionMatcher) NegatedFailureMessage(actual interface{}) string {
	if m.failureField == conditionFieldType {
		return fmt.Sprintf("Expected conditions to NOT contain a condition with Type %q, but it was found.\nConditions: %s",
			m.conditionType, format.Object(actual, 1))
	}

	return fmt.Sprintf("Conditions: %s\nCondition %q field %s matched when it should not have:\n%s",
		format.Object(actual, 1),
		m.conditionType,
		m.failureField,
		m.failureExpectedMatcher.NegatedFailureMessage(m.failureActual))
}

// toMatcher converts a value to a GomegaMatcher.
// If the value is already a GomegaMatcher, it returns it as-is.
// Otherwise, it wraps the value in gomega.Equal().
func toMatcher(v interface{}) types.GomegaMatcher {
	if matcher, ok := v.(types.GomegaMatcher); ok {
		return matcher
	}

	return gomega.Equal(v)
}

// getStringValue converts a reflect.Value to its string representation.
// This handles plain strings, string-based types (like configv1.ClusterStatusConditionType),
// and pointers to these types.
func getStringValue(v reflect.Value) string {
	v = derefValue(v)

	if v.Kind() == reflect.String {
		return v.String()
	}
	// For other types, try to convert to string via interface
	if s, ok := v.Interface().(string); ok {
		return s
	}
	// Try fmt.Stringer interface
	if stringer, ok := v.Interface().(fmt.Stringer); ok {
		return stringer.String()
	}

	return fmt.Sprintf("%v", v.Interface())
}

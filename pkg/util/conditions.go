/*
Copyright 2024 Red Hat, Inc.

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
	"time"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	errStatusNotMap                 = errors.New("unable to assert status structure to map")
	errStatusConditionsNotInterface = errors.New("unable to assert status.condition structure to interface")
)

// GetCondition retrieves a specific condition from a client.Object.
func GetCondition(obj client.Object, conditionType string) (*metav1.Condition, error) {
	// Convert client.Object to unstructured.Unstructured
	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert object to unstructured: %w", err)
	}

	unstructuredObj := &unstructured.Unstructured{Object: unstructuredMap}

	data := unstructuredObj.UnstructuredContent()

	status, ok := data["status"].(map[string]interface{})
	if !ok {
		return nil, errStatusNotMap
	}

	conditions, ok := status["conditions"].([]interface{})
	if !ok {
		return nil, errStatusConditionsNotInterface
	}

	for _, c := range conditions {
		condMap, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		status, ok := condMap["status"].(string)
		if !ok {
			continue
		}

		if condMap["type"] == conditionType {
			return &metav1.Condition{
				Type:               conditionType,
				Status:             metav1.ConditionStatus(status),
				Reason:             getString(condMap, "reason"),
				Message:            getString(condMap, "message"),
				LastTransitionTime: getTime(condMap, "lastTransitionTime"),
			}, nil
		}
	}

	return nil, nil //nolint:nilnil
}

// GetConditionStatus returns the status for the condition.
func GetConditionStatus(obj client.Object, conditionType string) (corev1.ConditionStatus, error) {
	cond, err := GetCondition(obj, conditionType)
	if err != nil {
		return corev1.ConditionUnknown, fmt.Errorf("unable to get condition %q for the object: %w", conditionType, err)
	}

	if cond == nil {
		return corev1.ConditionUnknown, nil
	}

	return corev1.ConditionStatus(cond.Status), nil
}

func getString(data map[string]interface{}, key string) string {
	if val, ok := data[key].(string); ok {
		return val
	}

	return ""
}

func getTime(data map[string]interface{}, key string) metav1.Time {
	if val, ok := data[key].(string); ok {
		parsedTime, err := time.Parse(time.RFC3339, val)
		if err == nil {
			return metav1.Time{Time: parsedTime}
		}
	}

	return metav1.Time{}
}

func GetMAPIMachineSetCondition(conditions []machinev1beta1.Condition, conditionType string) *machinev1beta1.Condition {
	for _, c := range conditions {
		if string(c.Type) == conditionType {
			return &c
		}
	}

	return nil
}

func SetMAPIMachineSetCondition(conditions []machinev1beta1.Condition, condition *machinev1beta1.Condition) []machinev1beta1.Condition {
	for i, c := range conditions {
		if string(c.Type) == string(condition.Type) {
			conditions[i] = *condition
			return conditions
		}
	}

	return append(conditions, *condition)
}

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
	"testing"
	"time"

	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//nolint:funlen
func TestSetMAPICondition(t *testing.T) {
	t1 := time.Now().UTC().Truncate(time.Second).Add(-1 * time.Hour)

	tests := []struct {
		name               string
		existingConditions []mapiv1beta1.Condition
		newCondition       *mapiv1beta1.Condition
		expectedConditions []mapiv1beta1.Condition
	}{
		{
			name:               "add new condition to empty list",
			existingConditions: []mapiv1beta1.Condition{},
			newCondition: &mapiv1beta1.Condition{
				Type:    "Ready",
				Status:  corev1.ConditionTrue,
				Reason:  "AllGood",
				Message: "Everything is working",
			},
			expectedConditions: []mapiv1beta1.Condition{
				{
					Type:    "Ready",
					Status:  corev1.ConditionTrue,
					Reason:  "AllGood",
					Message: "Everything is working",
				},
			},
		},
		{
			name: "add new condition to existing list",
			existingConditions: []mapiv1beta1.Condition{
				{
					Type:               "Available",
					Status:             corev1.ConditionTrue,
					Reason:             "Available",
					Message:            "Service is available",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &mapiv1beta1.Condition{
				Type:    "Ready",
				Status:  corev1.ConditionTrue,
				Reason:  "AllGood",
				Message: "Everything is working",
			},
			expectedConditions: []mapiv1beta1.Condition{
				{
					Type:               "Available",
					Status:             corev1.ConditionTrue,
					Reason:             "Available",
					Message:            "Service is available",
					LastTransitionTime: metav1.NewTime(t1),
				},
				{
					Type:    "Ready",
					Status:  corev1.ConditionTrue,
					Reason:  "AllGood",
					Message: "Everything is working",
				},
			},
		},
		{
			name: "update existing condition with status change",
			existingConditions: []mapiv1beta1.Condition{
				{
					Type:               "Ready",
					Status:             corev1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &mapiv1beta1.Condition{
				Type:    "Ready",
				Status:  corev1.ConditionFalse,
				Reason:  "Error",
				Message: "Something went wrong",
			},
			expectedConditions: []mapiv1beta1.Condition{
				{
					Type:    "Ready",
					Status:  corev1.ConditionFalse,
					Reason:  "Error",
					Message: "Something went wrong",
				},
			},
		},
		{
			name: "update existing condition with reason change",
			existingConditions: []mapiv1beta1.Condition{
				{
					Type:               "Ready",
					Status:             corev1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &mapiv1beta1.Condition{
				Type:    "Ready",
				Status:  corev1.ConditionTrue,
				Reason:  "Updated",
				Message: "Everything is working",
			},
			expectedConditions: []mapiv1beta1.Condition{
				{
					Type:    "Ready",
					Status:  corev1.ConditionTrue,
					Reason:  "Updated",
					Message: "Everything is working",
				},
			},
		},
		{
			name: "update existing condition with message change",
			existingConditions: []mapiv1beta1.Condition{
				{
					Type:               "Ready",
					Status:             corev1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &mapiv1beta1.Condition{
				Type:    "Ready",
				Status:  corev1.ConditionTrue,
				Reason:  "AllGood",
				Message: "Everything is still working",
			},
			expectedConditions: []mapiv1beta1.Condition{
				{
					Type:    "Ready",
					Status:  corev1.ConditionTrue,
					Reason:  "AllGood",
					Message: "Everything is still working",
				},
			},
		},
		{
			name: "update existing condition with no state change preserves time",
			existingConditions: []mapiv1beta1.Condition{
				{
					Type:               "Ready",
					Status:             corev1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &mapiv1beta1.Condition{
				Type:    "Ready",
				Status:  corev1.ConditionTrue,
				Reason:  "AllGood",
				Message: "Everything is working",
			},
			expectedConditions: []mapiv1beta1.Condition{
				{
					Type:               "Ready",
					Status:             corev1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
		},
		{
			name: "update existing condition with provided LastTransitionTime",
			existingConditions: []mapiv1beta1.Condition{
				{
					Type:               "Ready",
					Status:             corev1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &mapiv1beta1.Condition{
				Type:               "Ready",
				Status:             corev1.ConditionFalse,
				Reason:             "Error",
				Message:            "Something went wrong",
				LastTransitionTime: metav1.NewTime(t1),
			},
			expectedConditions: []mapiv1beta1.Condition{
				{
					Type:               "Ready",
					Status:             corev1.ConditionFalse,
					Reason:             "Error",
					Message:            "Something went wrong",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			before := metav1.NewTime(time.Now().UTC().Truncate(time.Second).Add(-1 * time.Second))
			result := SetMAPICondition(tt.existingConditions, tt.newCondition)

			g.Expect(result).To(HaveLen(len(tt.expectedConditions)))

			for i, expected := range tt.expectedConditions {
				actual := result[i]

				g.Expect(actual.Type).To(Equal(expected.Type))
				g.Expect(actual.Status).To(Equal(expected.Status))
				g.Expect(actual.Reason).To(Equal(expected.Reason))
				g.Expect(actual.Message).To(Equal(expected.Message))

				if !expected.LastTransitionTime.IsZero() {
					g.Expect(actual.LastTransitionTime).To(Equal(expected.LastTransitionTime))
				} else {
					g.Expect(actual.LastTransitionTime.After(before.Time)).To(BeTrue(), "LastTransitionTime should be set to something newer than before running the function")
				}

				g.Expect(actual.LastTransitionTime.Time.Nanosecond()).To(Equal(0), "LastTransitionTime should always be truncated to seconds")
			}
		})
	}
}

//nolint:funlen
func TestSetMAPIProviderCondition(t *testing.T) {
	t1 := time.Now().UTC().Truncate(time.Second).Add(-1 * time.Hour)

	tests := []struct {
		name               string
		existingConditions []metav1.Condition
		newCondition       *metav1.Condition
		expectedConditions []metav1.Condition
	}{
		{
			name:               "add new condition to empty list",
			existingConditions: []metav1.Condition{},
			newCondition: &metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionTrue,
				Reason:  "AllGood",
				Message: "Everything is working",
			},
			expectedConditions: []metav1.Condition{
				{
					Type:    "Ready",
					Status:  metav1.ConditionTrue,
					Reason:  "AllGood",
					Message: "Everything is working",
				},
			},
		},
		{
			name: "add new condition to existing list",
			existingConditions: []metav1.Condition{
				{
					Type:               "Available",
					Status:             metav1.ConditionTrue,
					Reason:             "Available",
					Message:            "Service is available",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionTrue,
				Reason:  "AllGood",
				Message: "Everything is working",
			},
			expectedConditions: []metav1.Condition{
				{
					Type:               "Available",
					Status:             metav1.ConditionTrue,
					Reason:             "Available",
					Message:            "Service is available",
					LastTransitionTime: metav1.NewTime(t1),
				},
				{
					Type:    "Ready",
					Status:  metav1.ConditionTrue,
					Reason:  "AllGood",
					Message: "Everything is working",
				},
			},
		},
		{
			name: "update existing condition with status change",
			existingConditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "Error",
				Message: "Something went wrong",
			},
			expectedConditions: []metav1.Condition{
				{
					Type:    "Ready",
					Status:  metav1.ConditionFalse,
					Reason:  "Error",
					Message: "Something went wrong",
				},
			},
		},
		{
			name: "update existing condition with reason change",
			existingConditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionTrue,
				Reason:  "Updated",
				Message: "Everything is working",
			},
			expectedConditions: []metav1.Condition{
				{
					Type:    "Ready",
					Status:  metav1.ConditionTrue,
					Reason:  "Updated",
					Message: "Everything is working",
				},
			},
		},
		{
			name: "update existing condition with message change",
			existingConditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionTrue,
				Reason:  "AllGood",
				Message: "Everything is still working",
			},
			expectedConditions: []metav1.Condition{
				{
					Type:    "Ready",
					Status:  metav1.ConditionTrue,
					Reason:  "AllGood",
					Message: "Everything is still working",
				},
			},
		},
		{
			name: "update existing condition with no state change preserves time",
			existingConditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionTrue,
				Reason:  "AllGood",
				Message: "Everything is working",
			},
			expectedConditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
		},
		{
			name: "update existing condition with provided LastTransitionTime",
			existingConditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "AllGood",
					Message:            "Everything is working",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
			newCondition: &metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "Error",
				Message:            "Something went wrong",
				LastTransitionTime: metav1.NewTime(t1),
			},
			expectedConditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionFalse,
					Reason:             "Error",
					Message:            "Something went wrong",
					LastTransitionTime: metav1.NewTime(t1),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			before := metav1.NewTime(time.Now().UTC().Truncate(time.Second).Add(-1 * time.Second))
			result := SetMAPIProviderCondition(tt.existingConditions, tt.newCondition)

			g.Expect(result).To(HaveLen(len(tt.expectedConditions)))

			for i, expected := range tt.expectedConditions {
				actual := result[i]

				g.Expect(actual.Type).To(Equal(expected.Type))
				g.Expect(actual.Status).To(Equal(expected.Status))
				g.Expect(actual.Reason).To(Equal(expected.Reason))
				g.Expect(actual.Message).To(Equal(expected.Message))

				if !expected.LastTransitionTime.IsZero() {
					g.Expect(actual.LastTransitionTime).To(Equal(expected.LastTransitionTime))
				} else {
					g.Expect(actual.LastTransitionTime.After(before.Time)).To(BeTrue(), "LastTransitionTime should be set to something newer than before running the function")
				}

				g.Expect(actual.LastTransitionTime.Time.Nanosecond()).To(Equal(0), "LastTransitionTime should always be truncated to seconds")
			}
		})
	}
}

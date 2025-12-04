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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

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

func TestGetCondition(t *testing.T) {
	tests := []struct {
		name          string
		obj           *unstructured.Unstructured
		conditionType string
		version       []string
		wantStatus    corev1.ConditionStatus
		wantErr       bool
	}{
		{
			name: "root condition found",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{"type": "Ready", "status": "True"},
						},
					},
				},
			},
			conditionType: "Ready",
			version:       nil,
			wantStatus:    corev1.ConditionTrue,
		},
		{
			name: "empty version defaults to root conditions",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{"type": "Ready", "status": "False"},
						},
					},
				},
			},
			conditionType: "Ready",
			version:       []string{},
			wantStatus:    corev1.ConditionFalse,
		},
		{
			name: "versioned v1beta2 condition found",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"v1beta2": map[string]interface{}{
							"conditions": []interface{}{
								map[string]interface{}{"type": "Available", "status": "False"},
							},
						},
					},
				},
			},
			conditionType: "Available",
			version:       []string{"v1beta2"},
			wantStatus:    corev1.ConditionFalse,
		},
		{
			name: "arbitrary future version condition found",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"v2": map[string]interface{}{
							"conditions": []interface{}{
								map[string]interface{}{"type": "Reconciled", "status": "True"},
							},
						},
					},
				},
			},
			conditionType: "Reconciled",
			version:       []string{"v2"},
			wantStatus:    corev1.ConditionTrue,
		},
		{
			name: "root conditions missing entirely",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{},
				},
			},
			conditionType: "Ready",
			version:       nil,
			wantErr:       true,
		},
		{
			name: "versioned path missing entirely",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{},
				},
			},
			conditionType: "Ready",
			version:       []string{"v1beta2"},
			wantErr:       true,
		},
		{
			name: "condition type not found in list",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{"type": "Progressing", "status": "True"},
						},
					},
				},
			},
			conditionType: "Ready",
			version:       nil,
			wantStatus:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			cond, err := GetCondition(tt.obj, tt.conditionType, tt.version...)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())

			if tt.wantStatus != "" {
				g.Expect(cond).NotTo(BeNil())
				g.Expect(corev1.ConditionStatus(cond.Status)).To(Equal(tt.wantStatus))
			}
		})
	}
}

func TestGetConditionStatus(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"v1beta2": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{"type": "Ready", "status": "True"},
					},
				},
			},
		},
	}

	t.Run("versioned condition found", func(t *testing.T) {
		g := NewWithT(t)
		status, err := GetConditionStatus(obj, "Ready", "v1beta2")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status).To(Equal(corev1.ConditionTrue))
	})

	t.Run("missing condition returns Unknown", func(t *testing.T) {
		g := NewWithT(t)
		status, err := GetConditionStatus(obj, "MissingCondition", "v1beta2")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(status).To(Equal(corev1.ConditionUnknown))
	})

	t.Run("non-existent version returns error", func(t *testing.T) {
		g := NewWithT(t)
		status, err := GetConditionStatus(obj, "Ready", "non-existent-version")
		g.Expect(err).To(HaveOccurred())
		g.Expect(status).To(Equal(corev1.ConditionUnknown))
	})
}

func TestIsKubeVersion(t *testing.T) {
	t.Run("valid kube versions", func(t *testing.T) {
		g := NewWithT(t)
		for _, v := range []string{"v1", "v2", "v1alpha1", "v1beta1", "v1beta2", "v1beta3", "v2beta1", "v10alpha3"} {
			g.Expect(isKubeVersion(v)).To(BeTrue(), "expected %q to be a valid kube version", v)
		}
	})

	t.Run("invalid kube versions", func(t *testing.T) {
		g := NewWithT(t)
		for _, v := range []string{"", "foo", "conditions", "v", "beta1", "v1gamma1", "1beta1", "v1beta", "v1BETA1"} {
			g.Expect(isKubeVersion(v)).To(BeFalse(), "expected %q to NOT be a valid kube version", v)
		}
	})
}

func TestFindVersionedConditionPath(t *testing.T) {
	tests := []struct {
		name      string
		statusMap map[string]interface{}
		want      string
	}{
		{
			name: "finds v1beta2",
			statusMap: map[string]interface{}{
				"v1beta2": map[string]interface{}{
					"conditions": []interface{}{},
				},
			},
			want: "v1beta2",
		},
		{
			name: "finds v1 GA version",
			statusMap: map[string]interface{}{
				"v1": map[string]interface{}{
					"conditions": []interface{}{},
				},
			},
			want: "v1",
		},
		{
			name: "ignores non-version keys",
			statusMap: map[string]interface{}{
				"observedGeneration": int64(1),
				"foo": map[string]interface{}{
					"conditions": []interface{}{},
				},
			},
			want: "",
		},
		{
			name: "ignores version key without conditions",
			statusMap: map[string]interface{}{
				"v1beta2": map[string]interface{}{
					"ready": true,
				},
			},
			want: "",
		},
		{
			name:      "empty status map",
			statusMap: map[string]interface{}{},
			want:      "",
		},
		{
			name: "ignores root conditions (not a versioned sub-map)",
			statusMap: map[string]interface{}{
				"conditions": []interface{}{},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(findVersionedConditionPath(tt.statusMap)).To(Equal(tt.want))
		})
	}
}

func TestGetConditionStatusFromInfraObject(t *testing.T) {
	tests := []struct {
		name       string
		obj        *unstructured.Unstructured
		condType   string
		wantStatus corev1.ConditionStatus
	}{
		{
			name: "finds versioned condition",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"v1beta2": map[string]interface{}{
							"conditions": []interface{}{
								map[string]interface{}{"type": "Ready", "status": "True"},
							},
						},
					},
				},
			},
			condType:   "Ready",
			wantStatus: corev1.ConditionTrue,
		},
		{
			name: "falls back to root conditions",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{
						"conditions": []interface{}{
							map[string]interface{}{"type": "Ready", "status": "True"},
						},
					},
				},
			},
			condType:   "Ready",
			wantStatus: corev1.ConditionTrue,
		},
		{
			name: "empty status returns Unknown",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"status": map[string]interface{}{},
				},
			},
			condType:   "Ready",
			wantStatus: corev1.ConditionUnknown,
		},
		{
			name: "no status at all returns Unknown",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{},
			},
			condType:   "Ready",
			wantStatus: corev1.ConditionUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			status, err := GetConditionStatusFromInfraObject(tt.obj, tt.condType)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(status).To(Equal(tt.wantStatus))
		})
	}
}

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

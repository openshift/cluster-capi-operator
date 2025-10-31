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
package machinesetsync

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

//nolint:funlen
func TestCAPIMachineSetStatusEqual(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(1 * time.Hour))

	tests := []struct {
		name        string
		a           clusterv1.MachineSetStatus
		b           clusterv1.MachineSetStatus
		want        string
		wantChanges bool
	}{
		{
			name:        "no diff",
			a:           clusterv1.MachineSetStatus{},
			b:           clusterv1.MachineSetStatus{},
			want:        "",
			wantChanges: false,
		},
		{
			name: "diff in ReadyReplicas",
			a: clusterv1.MachineSetStatus{
				ReadyReplicas: 3,
			},
			b: clusterv1.MachineSetStatus{
				ReadyReplicas: 5,
			},
			want:        ".[status].[readyReplicas]: 3 != 5",
			wantChanges: true,
		},
		{
			name: "diff in AvailableReplicas",
			a: clusterv1.MachineSetStatus{
				AvailableReplicas: 2,
			},
			b: clusterv1.MachineSetStatus{
				AvailableReplicas: 4,
			},
			want:        ".[status].[availableReplicas]: 2 != 4",
			wantChanges: true,
		},
		{
			name: "diff in ReadyReplicas and AvailableReplicas",
			a: clusterv1.MachineSetStatus{
				ReadyReplicas:     3,
				AvailableReplicas: 2,
			},
			b: clusterv1.MachineSetStatus{
				ReadyReplicas:     5,
				AvailableReplicas: 4,
			},
			want:        ".[status].[availableReplicas]: 2 != 4, .[status].[readyReplicas]: 3 != 5",
			wantChanges: true,
		},
		{
			name: "same v1beta1 conditions",
			a: clusterv1.MachineSetStatus{
				Conditions: []clusterv1.Condition{
					{
						Type:               clusterv1.ReadyCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: now,
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Conditions: []clusterv1.Condition{
					{
						Type:               clusterv1.ReadyCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: now,
					},
				},
			},
			want:        "",
			wantChanges: false,
		},
		{
			name: "changed v1beta1 condition Status",
			a: clusterv1.MachineSetStatus{
				Conditions: []clusterv1.Condition{
					{
						Type:               clusterv1.ReadyCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: now,
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Conditions: []clusterv1.Condition{
					{
						Type:               clusterv1.ReadyCondition,
						Status:             corev1.ConditionFalse,
						LastTransitionTime: now,
					},
				},
			},
			want:        ".[status].[conditions][0].[status]: True != False",
			wantChanges: true,
		},
		{
			name: "v1beta1 condition LastTransitionTime ignored",
			a: clusterv1.MachineSetStatus{
				Conditions: []clusterv1.Condition{
					{
						Type:               clusterv1.ReadyCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: now,
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Conditions: []clusterv1.Condition{
					{
						Type:               clusterv1.ReadyCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: later,
					},
				},
			},
			want:        "",
			wantChanges: false,
		},
		{
			name: "multiple v1beta1 conditions with one changed",
			a: clusterv1.MachineSetStatus{
				Conditions: []clusterv1.Condition{
					{
						Type:               clusterv1.ReadyCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: now,
					},
					{
						Type:               clusterv1.MachinesReadyCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: now,
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Conditions: []clusterv1.Condition{
					{
						Type:               clusterv1.ReadyCondition,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: now,
					},
					{
						Type:               clusterv1.MachinesReadyCondition,
						Status:             corev1.ConditionFalse,
						LastTransitionTime: now,
					},
				},
			},
			want:        ".[status].[conditions][1].[status]: True != False",
			wantChanges: true,
		},
		{
			name: "same v1beta2 conditions",
			a: clusterv1.MachineSetStatus{
				V1Beta2: &clusterv1.MachineSetV1Beta2Status{
					Conditions: []metav1.Condition{
						{
							Type:               "Ready",
							Status:             metav1.ConditionTrue,
							LastTransitionTime: now,
							Reason:             "AllReady",
							Message:            "All machines are ready",
						},
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				V1Beta2: &clusterv1.MachineSetV1Beta2Status{
					Conditions: []metav1.Condition{
						{
							Type:               "Ready",
							Status:             metav1.ConditionTrue,
							LastTransitionTime: now,
							Reason:             "AllReady",
							Message:            "All machines are ready",
						},
					},
				},
			},
			want:        "",
			wantChanges: false,
		},
		{
			name: "changed v1beta2 condition Status",
			a: clusterv1.MachineSetStatus{
				V1Beta2: &clusterv1.MachineSetV1Beta2Status{
					Conditions: []metav1.Condition{
						{
							Type:               "Ready",
							Status:             metav1.ConditionTrue,
							LastTransitionTime: now,
						},
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				V1Beta2: &clusterv1.MachineSetV1Beta2Status{
					Conditions: []metav1.Condition{
						{
							Type:               "Ready",
							Status:             metav1.ConditionFalse,
							LastTransitionTime: now,
						},
					},
				},
			},
			want:        ".[status].[v1beta2].[conditions][0].[status]: True != False",
			wantChanges: true,
		},
		{
			name: "v1beta2 condition LastTransitionTime ignored",
			a: clusterv1.MachineSetStatus{
				V1Beta2: &clusterv1.MachineSetV1Beta2Status{
					Conditions: []metav1.Condition{
						{
							Type:               "Ready",
							Status:             metav1.ConditionTrue,
							LastTransitionTime: now,
							Reason:             "AllReady",
							Message:            "All machines are ready",
						},
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				V1Beta2: &clusterv1.MachineSetV1Beta2Status{
					Conditions: []metav1.Condition{
						{
							Type:               "Ready",
							Status:             metav1.ConditionTrue,
							LastTransitionTime: later,
							Reason:             "AllReady",
							Message:            "All machines are ready",
						},
					},
				},
			},
			want:        "",
			wantChanges: false,
		},
		{
			name: "multiple v1beta2 conditions with one changed",
			a: clusterv1.MachineSetStatus{
				V1Beta2: &clusterv1.MachineSetV1Beta2Status{
					Conditions: []metav1.Condition{
						{
							Type:               "Ready",
							Status:             metav1.ConditionTrue,
							LastTransitionTime: now,
						},
						{
							Type:               "Available",
							Status:             metav1.ConditionTrue,
							LastTransitionTime: now,
						},
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				V1Beta2: &clusterv1.MachineSetV1Beta2Status{
					Conditions: []metav1.Condition{
						{
							Type:               "Ready",
							Status:             metav1.ConditionTrue,
							LastTransitionTime: now,
						},
						{
							Type:               "Available",
							Status:             metav1.ConditionFalse,
							LastTransitionTime: now,
						},
					},
				},
			},
			want:        ".[status].[v1beta2].[conditions][1].[status]: True != False",
			wantChanges: true,
		},
		{
			name: "v1beta2 nil vs non-nil",
			a: clusterv1.MachineSetStatus{
				V1Beta2: nil,
			},
			b: clusterv1.MachineSetStatus{
				V1Beta2: &clusterv1.MachineSetV1Beta2Status{
					ReadyReplicas: ptr.To[int32](3),
				},
			},
			want:        ".[status].[v1beta2]: <does not have key> != [readyReplicas:3]",
			wantChanges: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			a := &clusterv1.MachineSet{
				Status: tt.a,
			}
			b := &clusterv1.MachineSet{
				Status: tt.b,
			}

			got, err := compareCAPIMachineSets(a, b)
			g.Expect(err).ToNot(HaveOccurred())

			gotChanges := got.HasChanges()

			g.Expect(gotChanges).To(Equal(tt.wantChanges))

			if tt.wantChanges {
				gotString := got.String()
				g.Expect(gotString).To(Equal(tt.want))
			}
		})
	}
}

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("Unit tests for CAPIMachineSetStatusEqual", func() {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(1 * time.Hour))

	type testInput struct {
		a           clusterv1.MachineSetStatus
		b           clusterv1.MachineSetStatus
		want        string
		wantChanges bool
	}

	DescribeTable("when comparing Cluster API MachineSets",
		func(tt testInput) {
			a := &clusterv1.MachineSet{
				Status: tt.a,
			}
			b := &clusterv1.MachineSet{
				Status: tt.b,
			}

			got, err := compareCAPIMachineSets(a, b)
			Expect(err).ToNot(HaveOccurred())

			Expect(got.HasChanges()).To(Equal(tt.wantChanges))

			if tt.wantChanges {
				Expect(got.String()).To(BeEquivalentTo(tt.want))
			}
		},
		Entry("no diff", testInput{
			a:           clusterv1.MachineSetStatus{},
			b:           clusterv1.MachineSetStatus{},
			want:        "",
			wantChanges: false,
		}),
		Entry("diff in ReadyReplicas", testInput{
			a: clusterv1.MachineSetStatus{
				ReadyReplicas: ptr.To[int32](3),
			},
			b: clusterv1.MachineSetStatus{
				ReadyReplicas: ptr.To[int32](5),
			},
			want:        ".[status].[readyReplicas]: 3 != 5",
			wantChanges: true,
		}),
		Entry("diff in AvailableReplicas", testInput{
			a: clusterv1.MachineSetStatus{
				AvailableReplicas: ptr.To[int32](2),
			},
			b: clusterv1.MachineSetStatus{
				AvailableReplicas: ptr.To[int32](4),
			},
			want:        ".[status].[availableReplicas]: 2 != 4",
			wantChanges: true,
		}),
		Entry("diff in ReadyReplicas and AvailableReplicas", testInput{
			a: clusterv1.MachineSetStatus{
				ReadyReplicas:     ptr.To[int32](3),
				AvailableReplicas: ptr.To[int32](2),
			},
			b: clusterv1.MachineSetStatus{
				ReadyReplicas:     ptr.To[int32](5),
				AvailableReplicas: ptr.To[int32](4),
			},
			want:        ".[status].[availableReplicas]: 2 != 4, .[status].[readyReplicas]: 3 != 5",
			wantChanges: true,
		}),
		Entry("same v1beta1 conditions", testInput{
			a: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			want:        "",
			wantChanges: false,
		}),
		Entry("changed v1beta1 condition Status", testInput{
			a: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			want:        ".[status].[deprecated].[v1beta1].[conditions].[type=Ready].[status]: True != False",
			wantChanges: true,
		}),
		Entry("v1beta1 condition LastTransitionTime ignored", testInput{
			a: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: now,
							},
						},
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						Conditions: []clusterv1.Condition{
							{
								Type:               clusterv1.ReadyCondition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: later,
							},
						},
					},
				},
			},
			want:        "",
			wantChanges: false,
		}),
		Entry("multiple v1beta1 conditions with one changed", testInput{
			a: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
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
				},
			},
			b: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
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
				},
			},
			want:        ".[status].[deprecated].[v1beta1].[conditions].[type=MachinesReady].[status]: True != False",
			wantChanges: true,
		}),
		Entry("same v1beta2 conditions", testInput{
			a: clusterv1.MachineSetStatus{
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
			b: clusterv1.MachineSetStatus{
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
			want:        "",
			wantChanges: false,
		}),
		Entry("changed v1beta2 condition Status", testInput{
			a: clusterv1.MachineSetStatus{
				Conditions: []metav1.Condition{
					{
						Type:               "Ready",
						Status:             metav1.ConditionTrue,
						LastTransitionTime: now,
					},
				},
			},
			b: clusterv1.MachineSetStatus{
				Conditions: []metav1.Condition{
					{
						Type:               "Ready",
						Status:             metav1.ConditionFalse,
						LastTransitionTime: now,
					},
				},
			},
			want:        ".[status].[conditions].[type=Ready].[status]: True != False",
			wantChanges: true,
		}),
		Entry("v1beta2 condition LastTransitionTime ignored", testInput{
			a: clusterv1.MachineSetStatus{
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
			b: clusterv1.MachineSetStatus{
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
			want:        "",
			wantChanges: false,
		}),
		Entry("multiple v1beta2 conditions with one changed", testInput{
			a: clusterv1.MachineSetStatus{
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
			b: clusterv1.MachineSetStatus{
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
			want:        ".[status].[conditions].[type=Available].[status]: True != False",
			wantChanges: true,
		}),
		Entry("Deprecated.v1beta1 nil vs non-nil", testInput{
			a: clusterv1.MachineSetStatus{
				Deprecated: nil,
				Replicas:   ptr.To[int32](3),
			},
			b: clusterv1.MachineSetStatus{
				Deprecated: &clusterv1.MachineSetDeprecatedStatus{
					V1Beta1: &clusterv1.MachineSetV1Beta1DeprecatedStatus{
						ReadyReplicas: 3,
					},
				},
				Replicas: ptr.To[int32](3),
			},
			want:        ".[status].[deprecated]: <does not have key> != [v1beta1:[availableReplicas:0 fullyLabeledReplicas:0 readyReplicas:3]]",
			wantChanges: true,
		}),
	)
})

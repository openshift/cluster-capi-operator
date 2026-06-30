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

package synccommon

import (
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Sync status helpers", func() {
	DescribeTable("MigrationDirection should determine the current and desired authorities", func(statusAuthority mapiv1beta1.MachineAuthority, synchronizedAPI mapiv1beta1.SynchronizedAPI, specAuthority mapiv1beta1.MachineAuthority, expectedCurrent mapiv1beta1.MachineAuthority, expectedDesired mapiv1beta1.MachineAuthority, expectedMigrating bool, expectedErrMessage string, expectedErr error) {
		currentAuthority, desiredAuthority, isMigrating, err := MigrationDirection(statusAuthority, synchronizedAPI, specAuthority)

		Expect(currentAuthority).To(Equal(expectedCurrent))
		Expect(desiredAuthority).To(Equal(expectedDesired))
		Expect(isMigrating).To(Equal(expectedMigrating))

		if expectedErr == nil {
			Expect(err).NotTo(HaveOccurred())
			return
		}

		Expect(err).To(MatchError(expectedErrMessage))
		Expect(errors.Is(err, expectedErr)).To(BeTrue())
	},
		Entry("stable Machine API authority", mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.SynchronizedAPI(""), mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityClusterAPI, false, "", nil),
		Entry("stable empty authority", mapiv1beta1.MachineAuthority(""), mapiv1beta1.SynchronizedAPI(""), mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthority(""), mapiv1beta1.MachineAuthorityMachineAPI, false, "", nil),
		Entry("migrating from Machine API", mapiv1beta1.MachineAuthorityMigrating, mapiv1beta1.MachineAPISynchronized, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityClusterAPI, true, "", nil),
		Entry("migrating from Cluster API", mapiv1beta1.MachineAuthorityMigrating, mapiv1beta1.ClusterAPISynchronized, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityMachineAPI, true, "", nil),
		Entry("rollback while migrating", mapiv1beta1.MachineAuthorityMigrating, mapiv1beta1.MachineAPISynchronized, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAuthorityMachineAPI, true, "", nil),
		Entry("migrating without a synchronized API yet", mapiv1beta1.MachineAuthorityMigrating, mapiv1beta1.SynchronizedAPI(""), mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthority(""), mapiv1beta1.MachineAuthorityClusterAPI, true, "missing synchronizedAPI value while authoritativeAPI is Migrating", ErrMissingSynchronizedAPI),
		Entry("migrating with an invalid synchronized API", mapiv1beta1.MachineAuthorityMigrating, mapiv1beta1.SynchronizedAPI("BogusAPI"), mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthority(""), mapiv1beta1.MachineAuthorityClusterAPI, true, "invalid synchronizedAPI value: BogusAPI", ErrInvalidSynchronizedAPI),
	)

	Describe("newSyncStatusApplyConfiguration", func() {
		type testCase struct {
			object              client.Object
			status              corev1.ConditionStatus
			generation          *int64
			synchronizedAPI     *mapiv1beta1.SynchronizedAPI
			expectedGeneration  int64
			expectedSeverity    mapiv1beta1.ConditionSeverity
			expectedResourceVer string
		}

		DescribeTable("should build apply configurations for supported Machine API objects", func(tc testCase) {
			const (
				reason  = "SyncComplete"
				message = "Machine API object is synchronized"
			)

			var (
				resourceVersion        *string
				synchronizedGeneration *int64
				condition              machinev1applyconfigs.ConditionApplyConfiguration
				synchronizedAPI        *mapiv1beta1.SynchronizedAPI
			)

			switch o := tc.object.(type) {
			case *mapiv1beta1.Machine:
				objAC, statusAC, err := newSyncStatusApplyConfiguration[*machinev1applyconfigs.MachineStatusApplyConfiguration](machinev1applyconfigs.Machine, o, tc.status, reason, message, tc.generation, tc.synchronizedAPI)
				Expect(err).ToNot(HaveOccurred())
				Expect(objAC.ObjectMetaApplyConfiguration).ToNot(BeNil())
				resourceVersion = objAC.ResourceVersion
				synchronizedGeneration = statusAC.SynchronizedGeneration
				Expect(statusAC.Conditions).To(HaveLen(1))
				condition = statusAC.Conditions[0]
				synchronizedAPI = statusAC.SynchronizedAPI
			case *mapiv1beta1.MachineSet:
				objAC, statusAC, err := newSyncStatusApplyConfiguration[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](machinev1applyconfigs.MachineSet, o, tc.status, reason, message, tc.generation, tc.synchronizedAPI)
				Expect(err).ToNot(HaveOccurred())
				Expect(objAC.ObjectMetaApplyConfiguration).ToNot(BeNil())
				resourceVersion = objAC.ResourceVersion
				synchronizedGeneration = statusAC.SynchronizedGeneration
				Expect(statusAC.Conditions).To(HaveLen(1))
				condition = statusAC.Conditions[0]
				synchronizedAPI = statusAC.SynchronizedAPI
			default:
				Fail(fmt.Sprintf("unsupported object type %T", tc.object))
			}

			Expect(resourceVersion).ToNot(BeNil())
			Expect(*resourceVersion).To(Equal(tc.expectedResourceVer))

			Expect(synchronizedGeneration).ToNot(BeNil())
			Expect(*synchronizedGeneration).To(Equal(tc.expectedGeneration))

			Expect(condition.Type).ToNot(BeNil())
			Expect(*condition.Type).To(Equal(controllers.SynchronizedCondition))
			Expect(condition.Status).ToNot(BeNil())
			Expect(*condition.Status).To(Equal(tc.status))
			Expect(condition.Reason).ToNot(BeNil())
			Expect(*condition.Reason).To(Equal(reason))
			Expect(condition.Message).ToNot(BeNil())
			Expect(*condition.Message).To(Equal(message))
			Expect(condition.Severity).ToNot(BeNil())
			Expect(*condition.Severity).To(Equal(tc.expectedSeverity))
			Expect(condition.LastTransitionTime).ToNot(BeNil())

			if tc.synchronizedAPI == nil {
				Expect(synchronizedAPI).To(BeNil())
			} else {
				Expect(synchronizedAPI).ToNot(BeNil())
				Expect(*synchronizedAPI).To(Equal(*tc.synchronizedAPI))
			}
		},
			Entry("a Machine while preserving the previous synchronized generation", testCase{
				object: &mapiv1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "machine-1",
						Namespace:       "openshift-machine-api",
						ResourceVersion: "7",
					},
					Status: mapiv1beta1.MachineStatus{
						SynchronizedGeneration: 5,
					},
				},
				status:              corev1.ConditionTrue,
				synchronizedAPI:     ptr.To(mapiv1beta1.MachineAPISynchronized),
				expectedGeneration:  5,
				expectedSeverity:    mapiv1beta1.ConditionSeverityNone,
				expectedResourceVer: "7",
			}),
			Entry("a MachineSet while overriding the synchronized generation", testCase{
				object: &mapiv1beta1.MachineSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "machineset-1",
						Namespace:       "openshift-machine-api",
						ResourceVersion: "11",
					},
					Status: mapiv1beta1.MachineSetStatus{
						SynchronizedGeneration: 2,
					},
				},
				status:              corev1.ConditionFalse,
				generation:          ptr.To(int64(19)),
				synchronizedAPI:     ptr.To(mapiv1beta1.ClusterAPISynchronized),
				expectedGeneration:  19,
				expectedSeverity:    mapiv1beta1.ConditionSeverityError,
				expectedResourceVer: "11",
			}),
			Entry("a Machine with unknown sync status and no synchronized API", testCase{
				object: &mapiv1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "machine-2",
						Namespace:       "openshift-machine-api",
						ResourceVersion: "13",
					},
					Status: mapiv1beta1.MachineStatus{
						SynchronizedGeneration: 3,
					},
				},
				status:              corev1.ConditionUnknown,
				expectedGeneration:  3,
				expectedSeverity:    mapiv1beta1.ConditionSeverityInfo,
				expectedResourceVer: "13",
			}),
		)

		It("should preserve the previous last transition time when the synchronized condition state is unchanged", func() {
			lastTransitionTime := metav1.NewTime(time.Unix(1710000000, 0))
			mapiMachine := &mapiv1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "machine-3",
					Namespace:       "openshift-machine-api",
					ResourceVersion: "17",
				},
				Status: mapiv1beta1.MachineStatus{
					Conditions: []mapiv1beta1.Condition{{
						Type:               controllers.SynchronizedCondition,
						Status:             corev1.ConditionTrue,
						Reason:             "SyncComplete",
						Message:            "Machine API object is synchronized",
						Severity:           mapiv1beta1.ConditionSeverityNone,
						LastTransitionTime: lastTransitionTime,
					}},
				},
			}

			_, statusAC, err := newSyncStatusApplyConfiguration[*machinev1applyconfigs.MachineStatusApplyConfiguration](
				machinev1applyconfigs.Machine,
				mapiMachine,
				corev1.ConditionTrue,
				"SyncComplete",
				"Machine API object is synchronized",
				nil,
				ptr.To(mapiv1beta1.MachineAPISynchronized),
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(statusAC.Conditions).To(HaveLen(1))
			Expect(statusAC.Conditions[0].LastTransitionTime).ToNot(BeNil())
			Expect(statusAC.Conditions[0].LastTransitionTime.Time).To(Equal(lastTransitionTime.Time))
		})

		It("should reject unrecognized condition statuses", func() {
			mapiMachine := &mapiv1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "machine-4",
					Namespace: "openshift-machine-api",
				},
			}

			_, _, err := newSyncStatusApplyConfiguration[*machinev1applyconfigs.MachineStatusApplyConfiguration](
				machinev1applyconfigs.Machine,
				mapiMachine,
				corev1.ConditionStatus("NotAConditionStatus"),
				"SyncFailed",
				"status was invalid",
				nil,
				nil,
			)
			Expect(err).To(MatchError("error unrecognized condition status: NotAConditionStatus"))
			Expect(errors.Is(err, errUnrecognizedConditionStatus)).To(BeTrue())
		})

		It("should reject unsupported Machine API object types", func() {
			_, _, err := newSyncStatusApplyConfiguration[*machinev1applyconfigs.MachineStatusApplyConfiguration](
				machinev1applyconfigs.Machine,
				&corev1.ConfigMap{},
				corev1.ConditionTrue,
				"SyncComplete",
				"status was synchronized",
				nil,
				nil,
			)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, errUnsupportedSyncStatusType)).To(BeTrue())
			Expect(err).To(MatchError(ContainSubstring("type does not support setting sync status")))
		})
	})
})

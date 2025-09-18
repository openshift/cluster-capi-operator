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

package mapi2capi

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	testutils "github.com/openshift/cluster-api-actuator-pkg/testutils"
	configbuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"

	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("mapi2capi MachineSet conversion", func() {
	var (
		awsBaseProviderSpec = machinebuilder.AWSProviderSpec().WithLoadBalancers(nil).WithRegion("eu-west-2")
		mapiMachineSetBase  = machinebuilder.MachineSet().WithProviderSpecBuilder(awsBaseProviderSpec)
		infraBase           = configbuilder.Infrastructure().AsAWS("test", "eu-west-2")
	)

	type mapi2CAPIMachinesetConversionInput struct {
		machineSetBuilder machinebuilder.MachineSetBuilder
		infraBuilder      configbuilder.InfrastructureBuilder
		expectedErrors    []string
		expectedWarnings  []string
	}

	var _ = DescribeTable("mapi2capi convert MAPI MachineSet to CAPI MachineSet",
		func(in mapi2CAPIMachinesetConversionInput) {
			_, _, warns, err := FromAWSMachineSetAndInfra(
				in.machineSetBuilder.Build(),
				in.infraBuilder.Build(),
			).ToMachineSetAndMachineTemplate()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting MAPI MachineSet to CAPI MachineSet")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting MAPI MachineSet to CAPI MachineSet")
		},

		// Base Case
		Entry("With a Base configuration", mapi2CAPIMachinesetConversionInput{
			machineSetBuilder: mapiMachineSetBase,
			infraBuilder:      infraBase,
			expectedErrors:    []string{},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported spec.metadata.generateName set", mapi2CAPIMachinesetConversionInput{
			machineSetBuilder: mapiMachineSetBase.WithMachineSpecObjectMeta(mapiv1.ObjectMeta{
				GenerateName: "test-generate-",
			}),
			infraBuilder:     infraBase,
			expectedErrors:   []string{"spec.metadata.generateName: Invalid value: \"test-generate-\": generateName is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.metadata.name set", mapi2CAPIMachinesetConversionInput{
			machineSetBuilder: mapiMachineSetBase.WithMachineSpecObjectMeta(mapiv1.ObjectMeta{
				Name: "test-name",
			}),
			infraBuilder:     infraBase,
			expectedErrors:   []string{"spec.metadata.name: Invalid value: \"test-name\": name is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.metadata.namespace set", mapi2CAPIMachinesetConversionInput{
			infraBuilder: infraBase,
			machineSetBuilder: mapiMachineSetBase.WithMachineSpecObjectMeta(mapiv1.ObjectMeta{
				Namespace: "test-namespace",
			}),
			expectedErrors:   []string{"spec.metadata.namespace: Invalid value: \"test-namespace\": namespace is not supported"},
			expectedWarnings: []string{},
		}),
	)
})

var _ = Describe("mapi2capi MachineSet Status Conversion", func() {
	Context("when converting MAPI MachineSet status to CAPI", func() {
		It("should set all CAPI MachineSet status fields and conditions to the expected values", func() {
			// Build a MAPI MachineSet with a "Available" condition and some other status fields set.
			mapiMachineSet := machinebuilder.MachineSet().
				WithReplicas(5).
				WithReplicasStatus(5).
				WithFullyLabeledReplicas(5).
				WithReadyReplicas(4).
				WithAvailableReplicas(3).
				WithErrorReason(mapiv1.MachineSetStatusError("InvalidConfiguration")).
				WithErrorMessage("Test error message").
				Build()
			// Add a MAPI "Available" condition to the status.
			mapiMachineSet.Status.Conditions = []mapiv1.Condition{
				{
					Type:     "Available",
					Status:   corev1.ConditionTrue,
					Severity: mapiv1.ConditionSeverityNone,
					Reason:   "MachineSetAvailable",
					Message:  "MachineSet is available",
				},
			}

			capiStatus := convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet, metav1.LabelSelector{})

			Expect(capiStatus.Replicas).To(Equal(int32(5)))
			Expect(capiStatus.FullyLabeledReplicas).To(Equal(int32(5)))
			Expect(capiStatus.ReadyReplicas).To(Equal(int32(4)))
			Expect(capiStatus.AvailableReplicas).To(Equal(int32(3)))
			Expect(capiStatus.FailureReason).To(HaveValue(BeEquivalentTo(mapiv1.MachineSetStatusError("InvalidConfiguration"))))
			Expect(capiStatus.FailureMessage).To(HaveValue(BeEquivalentTo("Test error message")))
			Expect(capiStatus.Conditions).To(SatisfyAll(
				ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
					// The Ready condition is computed based on the ReadyReplicas and the Replicas.
					// In this case they differ, so the condition is false.
					Type:   clusterv1.ReadyCondition,
					Status: corev1.ConditionFalse,
				})),
				ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
					// The Resized condition is computed based on the .Spec.Replicas vs .Status.Replicas.
					// In this case they are equal, so the condition is true.
					Type:   clusterv1.ResizedCondition,
					Status: corev1.ConditionTrue,
				})),
				ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
					// The MachinesCreated condition is computed based on the .Spec.Replicas vs .Status.Replicas.
					// In this case they are equal, so the condition is true.
					Type:   clusterv1.MachinesCreatedCondition,
					Status: corev1.ConditionTrue,
				})),
				ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
					// The MachinesReady condition is computed based on the ReadyReplicas and the Replicas.
					// In this case they differ, so the condition is false.
					Type:   clusterv1.MachinesReadyCondition,
					Status: corev1.ConditionFalse,
				})),
				Not(ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
					// The Available condition is not copied from MAPI.
					Type:   "Available",
					Status: corev1.ConditionTrue,
				}))),
			))
			Expect(capiStatus.V1Beta2.Conditions).To(SatisfyAll(
				ContainElement(testutils.MatchCondition(metav1.Condition{
					// The Deleting condition is computed based on the .Spec.Replicas vs .Status.Replicas.
					// In this case they are equal, so the condition is false.
					Type:   clusterv1.MachineSetDeletingV1Beta2Condition,
					Status: metav1.ConditionFalse,
					Reason: clusterv1.MachineSetNotDeletingV1Beta2Reason,
				})),
				ContainElement(testutils.MatchCondition(metav1.Condition{
					// The ScalingUp condition is computed based on the .Spec.Replicas vs .Status.Replicas.
					// In this case they are equal, so the condition is false.
					Type:   clusterv1.MachineSetScalingUpV1Beta2Condition,
					Status: metav1.ConditionFalse,
					Reason: clusterv1.MachineSetNotScalingUpV1Beta2Reason,
				})),
				ContainElement(testutils.MatchCondition(metav1.Condition{
					// The ScalingDown condition is computed based on the .Spec.Replicas vs .Status.Replicas.
					// In this case they are equal, so the condition is false.
					Type:   clusterv1.MachineSetScalingDownV1Beta2Condition,
					Status: metav1.ConditionFalse,
					Reason: clusterv1.MachineSetNotScalingDownV1Beta2Reason,
				})),
				ContainElement(testutils.MatchCondition(metav1.Condition{
					// The MachinesReady condition is computed based on the ReadyReplicas and the Replicas.
					// In this case they differ, so the condition is false.
					Type:   clusterv1.MachineSetMachinesReadyV1Beta2Condition,
					Status: metav1.ConditionFalse,
					Reason: clusterv1.MachineSetMachinesNotReadyV1Beta2Reason,
				})),
				ContainElement(testutils.MatchCondition(metav1.Condition{
					// The MachinesUpToDate condition is computed based on the .Spec.Replicas vs .Status.FullyLabeledReplicas.
					// In this case they are equal, so the condition is true.
					Type:   clusterv1.MachineSetMachinesUpToDateV1Beta2Condition,
					Status: metav1.ConditionTrue,
					Reason: clusterv1.MachineSetMachinesUpToDateV1Beta2Reason,
				})),
				Not(ContainElement(testutils.MatchCondition(metav1.Condition{
					// The Available condition is not copied from MAPI.
					Type:   "Available",
					Status: metav1.ConditionTrue,
				}))),
			))
		})

		It("should set all CAPI MachineSet status fields to empty and conditions to false when MAPI MachineSet status is empty", func() {
			mapiMachineSet := machinebuilder.MachineSet().Build() // No mutations for status or spec set

			capiStatus := convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet, metav1.LabelSelector{})

			Expect(capiStatus.Replicas).To(Equal(int32(0)))
			Expect(capiStatus.FullyLabeledReplicas).To(Equal(int32(0)))
			Expect(capiStatus.ReadyReplicas).To(Equal(int32(0)))
			Expect(capiStatus.AvailableReplicas).To(Equal(int32(0)))
			Expect(capiStatus.ObservedGeneration).To(Equal(int64(0)))
			Expect(capiStatus.FailureReason).To(BeNil())
			Expect(capiStatus.FailureMessage).To(BeNil())
			// All four conditions are expected to be false because the MAPI MachineSet status is empty,
			// so there are no replicas, no ready replicas, and no created machines.
			Expect(capiStatus.Conditions).To(ConsistOf(
				matchers.MatchCAPICondition(clusterv1.Condition{
					// The Ready condition is false because ReadyReplicas != Replicas (both are zero).
					Type:   clusterv1.ReadyCondition,
					Status: corev1.ConditionFalse,
				}),
				matchers.MatchCAPICondition(clusterv1.Condition{
					// The MachinesReady condition is false because ReadyReplicas != Replicas (both are zero).
					Type:   clusterv1.MachinesReadyCondition,
					Status: corev1.ConditionFalse,
				}),
				matchers.MatchCAPICondition(clusterv1.Condition{
					// The Resized condition is false because Status.Replicas != Spec.Replicas (both are zero).
					Type:   clusterv1.ResizedCondition,
					Status: corev1.ConditionFalse,
				}),
				matchers.MatchCAPICondition(clusterv1.Condition{
					// The MachinesCreated condition is false because Status.Replicas != Spec.Replicas (both are zero).
					Type:   clusterv1.MachinesCreatedCondition,
					Status: corev1.ConditionFalse,
				}),
			))
		})

		It("should set CAPI MachineSet Ready and MachinesReady conditions to true when ReadyReplicas == Replicas", func() {
			// Build a MAPI MachineSet with ReadyReplicas == Replicas.
			mapiMachineSet := machinebuilder.MachineSet().
				WithReplicas(3).
				WithReplicasStatus(3).
				WithFullyLabeledReplicas(3).
				WithReadyReplicas(3).
				WithAvailableReplicas(3).
				Build()

			capiStatus := convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet, metav1.LabelSelector{})

			Expect(capiStatus.ReadyReplicas).To(Equal(int32(3)))
			Expect(capiStatus.Replicas).To(Equal(int32(3)))
			Expect(capiStatus.Conditions).To(ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
				// The Ready condition is computed based on the ReadyReplicas and the Replicas.
				// In this case they are equal, so the condition is true.
				Type:   clusterv1.ReadyCondition,
				Status: corev1.ConditionTrue,
			})))
			Expect(capiStatus.Conditions).To(ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
				// The MachinesReady condition is computed based on the ReadyReplicas and the Replicas.
				// In this case they are equal, so the condition is true.
				Type:   clusterv1.MachinesReadyCondition,
				Status: corev1.ConditionTrue,
			})))
		})

		It("should set CAPI MachineSet Resized and MachinesCreated conditions to false when Status.Replicas != Spec.Replicas", func() {
			// Build a MAPI MachineSet with Status.Replicas != Spec.Replicas.
			mapiMachineSet := machinebuilder.MachineSet().
				WithReplicas(2).
				WithReplicasStatus(1).
				WithFullyLabeledReplicas(1).
				WithReadyReplicas(1).
				WithAvailableReplicas(1).
				Build()

			capiStatus := convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet, metav1.LabelSelector{})

			Expect(capiStatus.Replicas).To(Equal(int32(1)))
			Expect(capiStatus.Conditions).To(ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
				// The Resized condition is computed based on the .Spec.Replicas vs .Status.Replicas.
				// In this case they are not equal, so the condition is false.
				Type:   clusterv1.ResizedCondition,
				Status: corev1.ConditionFalse,
			})))
			Expect(capiStatus.Conditions).To(ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
				// The MachinesCreated condition is computed based on the .Spec.Replicas vs .Status.Replicas.
				// In this case they are not equal, so the condition is false.
				Type:   clusterv1.MachinesCreatedCondition,
				Status: corev1.ConditionFalse,
			})))
		})

		It("should set CAPI MachineSet ScalingUp condition to true when Spec.Replicas > Status.Replicas", func() {
			// Build a MAPI MachineSet with Spec.Replicas > Status.Replicas (scaling up).
			mapiMachineSet := machinebuilder.MachineSet().
				WithReplicas(4).
				WithReplicasStatus(2).
				WithFullyLabeledReplicas(2).
				WithReadyReplicas(2).
				WithAvailableReplicas(2).
				Build()

			capiStatus := convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet, metav1.LabelSelector{})
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				// The ScalingUp condition is computed based on the .Spec.Replicas > .Status.Replicas.
				// In this case, scaling up is true.
				Type:   clusterv1.MachineSetScalingUpV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: clusterv1.MachineSetScalingUpV1Beta2Reason,
			})))
		})

		It("should set CAPI MachineSet ScalingDown condition to true when Spec.Replicas < Status.Replicas", func() {
			// Build a MAPI MachineSet with Spec.Replicas < Status.Replicas (scaling down).
			mapiMachineSet := machinebuilder.MachineSet().
				WithReplicas(1).
				WithReplicasStatus(3).
				WithFullyLabeledReplicas(3).
				WithReadyReplicas(1).
				WithAvailableReplicas(1).
				Build()

			capiStatus := convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet, metav1.LabelSelector{})
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				// The ScalingDown condition is computed based on the .Spec.Replicas < .Status.Replicas.
				// In this case, scaling down is true.
				Type:   clusterv1.MachineSetScalingDownV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: clusterv1.MachineSetScalingDownV1Beta2Reason,
			})))
		})

		It("should set CAPI MachineSet MachinesUpToDate condition to true when FullyLabeledReplicas == Spec.Replicas", func() {
			// Build a MAPI MachineSet with FullyLabeledReplicas == Spec.Replicas.
			mapiMachineSet := machinebuilder.MachineSet().
				WithReplicas(2).
				WithReplicasStatus(2).
				WithFullyLabeledReplicas(2).
				WithReadyReplicas(2).
				WithAvailableReplicas(2).
				Build()

			capiStatus := convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet, metav1.LabelSelector{})
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				// The MachinesUpToDate condition is computed based on the .Spec.Replicas == .Status.FullyLabeledReplicas.
				// In this case they are equal, so the condition is true.
				Type:   clusterv1.MachineSetMachinesUpToDateV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: clusterv1.MachineSetMachinesUpToDateV1Beta2Reason,
			})))
		})

		It("should set CAPI MachineSet MachinesUpToDate condition to false when FullyLabeledReplicas != Spec.Replicas", func() {
			// Build a MAPI MachineSet with FullyLabeledReplicas != Spec.Replicas.
			mapiMachineSet := machinebuilder.MachineSet().
				WithReplicas(3).
				WithReplicasStatus(3).
				WithFullyLabeledReplicas(2).
				WithReadyReplicas(2).
				WithAvailableReplicas(2).
				Build()

			capiStatus := convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet, metav1.LabelSelector{})
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				// The MachinesUpToDate condition is computed based on the .Spec.Replicas == .Status.FullyLabeledReplicas.
				// In this case they are not equal, so the condition is false.
				Type:   clusterv1.MachineSetMachinesUpToDateV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1.MachineSetMachinesNotUpToDateV1Beta2Reason,
			})))
		})

		It("should set CAPI MachineSet Selector to the expected value when Spec.Selector is set", func() {
			// Build a MAPI MachineSet with Spec.Selector set.
			mapiMachineSet := machinebuilder.MachineSet().
				WithMachineSetSpecSelector(metav1.LabelSelector{
					MatchLabels: map[string]string{"foo": "bar"},
				}).
				Build()

			capiStatus := convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet, mapiMachineSet.Spec.Selector)
			Expect(capiStatus.Selector).To(Equal("foo=bar"))
		})

		It("should set CAPI MachineSet Selector to empty when Spec.Selector is not set", func() {
			// Build a MAPI MachineSet with Spec.Selector not set.
			mapiMachineSet := machinebuilder.MachineSet().Build()

			capiStatus := convertMAPIMachineSetToCAPIMachineSetStatus(mapiMachineSet, metav1.LabelSelector{})
			Expect(capiStatus.Selector).To(BeEmpty())
		})
	})
})

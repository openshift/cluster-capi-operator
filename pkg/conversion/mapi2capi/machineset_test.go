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
	configbuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"

	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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
	Describe("convertMAPIMachineSetStatusToCAPI", func() {
		It("should convert MAPI MachineSet status to CAPI correctly", func() {
			mapiStatus := mapiv1.MachineSetStatus{
				Replicas:             5,
				FullyLabeledReplicas: 5,
				ReadyReplicas:        4,
				AvailableReplicas:    3,
				ErrorReason:          ptr.To(mapiv1.MachineSetStatusError("InvalidConfiguration")),
				ErrorMessage:         ptr.To("Test error message"),
				Conditions: []mapiv1.Condition{
					{
						Type:               "Available",
						Status:             corev1.ConditionTrue,
						Severity:           mapiv1.ConditionSeverityNone,
						LastTransitionTime: metav1.Now(),
						Reason:             "MachineSetAvailable",
						Message:            "MachineSet is available",
					},
				},
			}

			capiStatus := convertMAPIMachineSetStatusToCAPI(mapiStatus, 1)

			Expect(capiStatus.Selector).To(Equal(""))
			Expect(capiStatus.Replicas).To(Equal(int32(5)))
			Expect(capiStatus.FullyLabeledReplicas).To(Equal(int32(5)))
			Expect(capiStatus.ReadyReplicas).To(Equal(int32(4)))
			Expect(capiStatus.AvailableReplicas).To(Equal(int32(3)))
			Expect(capiStatus.FailureReason).To(HaveValue(BeEquivalentTo(mapiv1.MachineSetStatusError("InvalidConfiguration"))))
			Expect(capiStatus.FailureMessage).To(HaveValue(BeEquivalentTo("Test error message")))
			Expect(capiStatus.Conditions).To(ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
				Type:    "Available",
				Status:  corev1.ConditionTrue,
				Reason:  "MachineSetAvailable",
				Message: "MachineSet is available",
			})))
		})

		It("should handle empty MAPI MachineSet Status", func() {
			mapiStatus := mapiv1.MachineSetStatus{}

			capiStatus := convertMAPIMachineSetStatusToCAPI(mapiStatus, 0)

			Expect(capiStatus.Selector).To(Equal(""))
			Expect(capiStatus.Replicas).To(Equal(int32(0)))
			Expect(capiStatus.FullyLabeledReplicas).To(Equal(int32(0)))
			Expect(capiStatus.ReadyReplicas).To(Equal(int32(0)))
			Expect(capiStatus.AvailableReplicas).To(Equal(int32(0)))
			Expect(capiStatus.ObservedGeneration).To(Equal(int64(0)))
			Expect(capiStatus.FailureReason).To(BeNil())
			Expect(capiStatus.FailureMessage).To(BeNil())
			Expect(capiStatus.Conditions).To(BeNil())
		})
	})

	Describe("convertMAPIConditionsToCAPI", func() {
		It("should convert MAPI conditions to CAPI conditions", func() {
			mapiConditions := []mapiv1.Condition{
				{
					Type:   "Available",
					Status: corev1.ConditionTrue,
					// Severity must only be set when the condition is not True.
					LastTransitionTime: metav1.Now(),
					Reason:             "MachineSetAvailable",
					Message:            "MachineSet is available",
				},
				{
					Type:               "Progressing",
					Status:             corev1.ConditionFalse,
					Severity:           mapiv1.ConditionSeverityError,
					LastTransitionTime: metav1.Now(),
					Reason:             "MachineSetNotProgressing",
					Message:            "MachineSet is not progressing",
				},
			}

			capiConditions := convertMAPIMachineSetConditionsToCAPIMachineSetConditions(mapiConditions)

			Expect(capiConditions).To(SatisfyAll(
				HaveLen(2),
				ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
					Type:    "Available",
					Status:  corev1.ConditionTrue,
					Reason:  "MachineSetAvailable",
					Message: "MachineSet is available",
				})),
				ContainElement(matchers.MatchCAPICondition(clusterv1.Condition{
					Type:     "Progressing",
					Status:   corev1.ConditionFalse,
					Severity: clusterv1.ConditionSeverityError,
					Reason:   "MachineSetNotProgressing",
					Message:  "MachineSet is not progressing",
				})),
			))
		})

		It("should return nil for empty conditions", func() {
			var mapiConditions []mapiv1.Condition
			capiConditions := convertMAPIMachineSetConditionsToCAPIMachineSetConditions(mapiConditions)
			Expect(capiConditions).To(BeNil())
		})
	})
})

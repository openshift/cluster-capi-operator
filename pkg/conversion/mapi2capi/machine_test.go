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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	testutils "github.com/openshift/cluster-api-actuator-pkg/testutils"
	configbuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("mapi2capi Machine conversion", func() {
	var (
		awsBaseProviderSpec = machinebuilder.AWSProviderSpec().WithLoadBalancers(nil).WithRegion("eu-west-2")
		mapiMachineBase     = machinebuilder.Machine().WithProviderSpecBuilder(awsBaseProviderSpec)
		infraBase           = configbuilder.Infrastructure().AsAWS("test", "eu-west-2")
	)

	type mapi2CAPIMachineConversionInput struct {
		machineBuilder   machinebuilder.MachineBuilder
		infraBuilder     configbuilder.InfrastructureBuilder
		expectedErrors   []string
		expectedWarnings []string
		assertion        func(machine *mapiv1beta1.Machine)
	}
	var _ = DescribeTable("mapi2capi convert MAPI Machine to a CAPI Machine",
		func(in mapi2CAPIMachineConversionInput) {
			_, _, warns, err := FromAWSMachineAndInfra(
				in.machineBuilder.Build(),
				in.infraBuilder.Build(),
			).ToMachineAndInfrastructureMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting MAPI Machine to CAPI Machine")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting MAPI Machine to CAPI Machine")
		},

		// Base Case.
		Entry("With a Base configuration", mapi2CAPIMachineConversionInput{
			machineBuilder:   mapiMachineBase,
			infraBuilder:     infraBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported CAPI managed labels", mapi2CAPIMachineConversionInput{
			infraBuilder: infraBase,
			machineBuilder: mapiMachineBase.WithMachineSpecObjectMeta(mapiv1beta1.ObjectMeta{
				Labels: map[string]string{
					"node-role.kubernetes.io/worker": "true",
				},
			}),
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With machineSet owner reference", mapi2CAPIMachineConversionInput{
			infraBuilder: infraBase,
			machineBuilder: mapiMachineBase.WithOwnerReferences([]metav1.OwnerReference{{
				APIVersion:         "machine.openshift.io/v1beta1",
				Kind:               "MachineSet",
				Name:               "test-machineset",
				UID:                "test-uid",
				Controller:         ptr.To(true),
				BlockOwnerDeletion: ptr.To(true),
			}}),
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.metadata.generateName set", mapi2CAPIMachineConversionInput{
			machineBuilder: mapiMachineBase.WithMachineSpecObjectMeta(mapiv1beta1.ObjectMeta{
				GenerateName: "test-generate-",
			}),
			infraBuilder:     infraBase,
			expectedErrors:   []string{"spec.metadata.generateName: Invalid value: \"test-generate-\": generateName is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.metadata.name set", mapi2CAPIMachineConversionInput{
			machineBuilder: mapiMachineBase.WithMachineSpecObjectMeta(mapiv1beta1.ObjectMeta{
				Name: "test-name",
			}),
			infraBuilder:     infraBase,
			expectedErrors:   []string{"spec.metadata.name: Invalid value: \"test-name\": name is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.metadata.namespace set", mapi2CAPIMachineConversionInput{
			infraBuilder: infraBase,
			machineBuilder: mapiMachineBase.WithMachineSpecObjectMeta(mapiv1beta1.ObjectMeta{
				Namespace: "test-namespace",
			}),
			expectedErrors:   []string{"spec.metadata.namespace: Invalid value: \"test-namespace\": namespace is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.taints set", mapi2CAPIMachineConversionInput{
			infraBuilder: infraBase,
			machineBuilder: mapiMachineBase.WithTaints([]corev1.Taint{{
				Key:    "key1",
				Value:  "value1",
				Effect: corev1.TaintEffectNoSchedule,
			}}),
			expectedErrors:   []string{"spec.taints: Invalid value: []v1.Taint{v1.Taint{Key:\"key1\", Value:\"value1\", Effect:\"NoSchedule\", TimeAdded:<nil>}}: taints are not currently supported"},
			expectedWarnings: []string{},
		}),

		Entry("With delete-machine annotation", mapi2CAPIMachineConversionInput{
			infraBuilder:     infraBase,
			machineBuilder:   mapiMachineBase.WithAnnotations(map[string]string{util.MapiDeleteMachineAnnotation: "true"}),
			expectedErrors:   []string{},
			expectedWarnings: []string{},
			assertion: func(machine *mapiv1beta1.Machine) {
				Expect(machine.Annotations).To(HaveKeyWithValue(clusterv1beta1.DeleteMachineAnnotation, "true"))
				Expect(machine.Annotations).ToNot(HaveKey(util.MapiDeleteMachineAnnotation))
			},
		}),
	)
})

var _ = Describe("mapi2capi Machine Status Conversion", func() {
	Context("when converting MAPI Machine status to CAPI", func() {
		It("should set all CAPI Machine status fields and conditions to the expected values", func() {
			// Set MAPI machine status fields
			nodeRef := corev1.ObjectReference{
				Kind:      "Node",
				Name:      "test-node",
				Namespace: "",
			}
			lastUpdated := metav1.Time{Time: time.Now()}
			condition := mapiv1beta1.Condition{
				Type:     "Available",
				Status:   corev1.ConditionTrue,
				Severity: mapiv1beta1.ConditionSeverityNone,
				Reason:   "MachineAvailable",
				Message:  "Machine is available",
			}

			// Build a MAPI Machine with status fields set.
			mapiMachine := machinebuilder.Machine().
				WithName("test-machine").
				WithNamespace("test-namespace").
				WithNodeRef(nodeRef).
				WithLastUpdated(lastUpdated).
				WithPhase("Running").
				WithErrorReason(mapiv1beta1.MachineStatusError("InvalidConfiguration")).
				WithErrorMessage("Test error message").
				WithAddresses([]corev1.NodeAddress{
					{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
					{Type: corev1.NodeExternalIP, Address: "203.0.113.1"},
				}).
				WithConditions([]mapiv1beta1.Condition{condition}).
				Build()

			// Convert MAPI Machine to CAPI Machine status.
			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())

			// Check CAPI Machine status fields are matching the expected values.
			Expect(capiStatus.NodeRef).To(HaveValue(Equal(nodeRef)))
			Expect(capiStatus.LastUpdated).To(HaveValue(Equal(lastUpdated)))
			Expect(capiStatus.Addresses).To(SatisfyAll(
				HaveLen(2),
				ContainElement(SatisfyAll(HaveField("Type", BeEquivalentTo(corev1.NodeInternalIP)), HaveField("Address", Equal("10.0.0.1")))),
				ContainElement(SatisfyAll(HaveField("Type", BeEquivalentTo(corev1.NodeExternalIP)), HaveField("Address", Equal("203.0.113.1")))),
			))
			Expect(capiStatus.Phase).To(BeEquivalentTo(clusterv1beta1.MachinePhaseRunning))
			Expect(capiStatus.FailureReason).To(HaveValue(BeEquivalentTo(mapiv1beta1.MachineStatusError("InvalidConfiguration"))))
			Expect(capiStatus.FailureMessage).To(HaveValue(BeEquivalentTo("Test error message")))
			Expect(capiStatus.Conditions).To(SatisfyAll(
				ContainElement(matchers.MatchCAPICondition(clusterv1beta1.Condition{
					// The Ready condition is computed based on the phase.
					// In this case they are equal, so the condition is true.
					Type:   clusterv1beta1.ReadyCondition,
					Status: corev1.ConditionTrue,
				})),
				ContainElement(matchers.MatchCAPICondition(clusterv1beta1.Condition{
					// The BootstrapReady condition is computed based on the phase.
					// In this case they are equal, so the condition is true.
					Type:   clusterv1beta1.BootstrapReadyCondition,
					Status: corev1.ConditionTrue,
				})),
				ContainElement(matchers.MatchCAPICondition(clusterv1beta1.Condition{
					// The InfrastructureReady condition is computed based on the phase.
					// In this case they are equal, so the condition is true.
					Type:   clusterv1beta1.InfrastructureReadyCondition,
					Status: corev1.ConditionTrue,
				})),
				Not(ContainElement(matchers.MatchCAPICondition(clusterv1beta1.Condition{
					// The Available condition is not copied from MAPI.
					Type:   "Available",
					Status: corev1.ConditionTrue,
				}))),
			))
			Expect(capiStatus.V1Beta2.Conditions).To(SatisfyAll(
				ContainElement(testutils.MatchCondition(metav1.Condition{
					// The Available condition is not copied from MAPI.
					Type:   clusterv1beta1.AvailableV1Beta2Condition,
					Status: metav1.ConditionTrue,
					Reason: clusterv1beta1.MachineAvailableV1Beta2Reason,
				})),
				ContainElement(testutils.MatchCondition(metav1.Condition{
					// The Ready condition is computed based on the phase.
					Type:   clusterv1beta1.ReadyV1Beta2Condition,
					Status: metav1.ConditionTrue,
					Reason: clusterv1beta1.MachineReadyV1Beta2Reason,
				})),
				ContainElement(testutils.MatchCondition(metav1.Condition{
					// The BootstrapConfigReady condition is computed based on the phase.
					Type:   clusterv1beta1.BootstrapConfigReadyV1Beta2Condition,
					Status: metav1.ConditionTrue,
					Reason: clusterv1beta1.MachineBootstrapConfigReadyV1Beta2Reason,
				})),
				ContainElement(testutils.MatchCondition(metav1.Condition{
					// The InfrastructureReady condition is computed based on the phase.
					Type:   clusterv1beta1.InfrastructureReadyV1Beta2Condition,
					Status: metav1.ConditionTrue,
					Reason: clusterv1beta1.MachineInfrastructureReadyV1Beta2Reason,
				})),
				ContainElement(testutils.MatchCondition(metav1.Condition{
					// The NodeReady condition is computed based on the phase.
					Type:   clusterv1beta1.MachineNodeReadyV1Beta2Condition,
					Status: metav1.ConditionTrue,
					Reason: clusterv1beta1.MachineNodeReadyV1Beta2Reason,
				})),
				ContainElement(testutils.MatchCondition(metav1.Condition{
					// The Deleting condition is computed based on the phase.
					Type:   clusterv1beta1.MachineDeletingV1Beta2Condition,
					Status: metav1.ConditionFalse,
					Reason: clusterv1beta1.MachineNotDeletingV1Beta2Reason,
				})),
			))
		})

		// v1beta1 conditions

		It("should set CAPI Machine v1beta1 Ready condition to true when MAPI Machine phase is Running", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Running").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.Conditions).To(ContainElement(matchers.MatchCAPICondition(clusterv1beta1.Condition{
				Type:   clusterv1beta1.ReadyCondition,
				Status: corev1.ConditionTrue,
			})))
		})

		It("should set CAPI Machine v1beta1 Ready condition to false when MAPI Machine phase is Provisioning", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Provisioning").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.Conditions).To(ContainElement(matchers.MatchCAPICondition(clusterv1beta1.Condition{
				Type:   clusterv1beta1.ReadyCondition,
				Status: corev1.ConditionFalse,
			})))
		})

		It("should set CAPI Machine v1beta1 BootstrapReady condition to true when MAPI Machine phase is Running", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Running").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.Conditions).To(ContainElement(matchers.MatchCAPICondition(clusterv1beta1.Condition{
				Type:   clusterv1beta1.BootstrapReadyCondition,
				Status: corev1.ConditionTrue,
			})))
		})

		It("should set CAPI Machine v1beta1 BootstrapReady condition to false when MAPI Machine phase is Provisioning", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Provisioning").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.Conditions).To(ContainElement(matchers.MatchCAPICondition(clusterv1beta1.Condition{
				Type:   clusterv1beta1.BootstrapReadyCondition,
				Status: corev1.ConditionFalse,
			})))
		})

		It("should set CAPI Machine v1beta1 InfrastructureReady condition to true when MAPI Machine phase is Running", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Running").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.Conditions).To(ContainElement(matchers.MatchCAPICondition(clusterv1beta1.Condition{
				Type:   clusterv1beta1.InfrastructureReadyCondition,
				Status: corev1.ConditionTrue,
			})))
		})

		It("should set CAPI Machine v1beta1 InfrastructureReady condition to false when MAPI Machine phase is Provisioning", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Provisioning").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.Conditions).To(ContainElement(matchers.MatchCAPICondition(clusterv1beta1.Condition{
				Type:     clusterv1beta1.InfrastructureReadyCondition,
				Reason:   clusterv1beta1.WaitingForInfrastructureFallbackReason,
				Status:   corev1.ConditionFalse,
				Severity: clusterv1beta1.ConditionSeverityInfo,
			})))
		})

		// v1beta2 conditions

		It("should set CAPI Machine v1beta2 Available condition to false when MAPI Machine phase is not Running", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Provisioning").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.AvailableV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1beta1.NotAvailableV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 Available condition to true when MAPI Machine phase is Running", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Running").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.AvailableV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: clusterv1beta1.MachineAvailableV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 Ready condition to false when MAPI Machine phase is not Running", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Provisioning").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.ReadyV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1beta1.MachineNotReadyV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 Ready condition to true when MAPI Machine phase is Running", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Running").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.ReadyV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: clusterv1beta1.MachineReadyV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 BootstrapConfigReady condition to true when MAPI Machine phase is Running", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Running").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.BootstrapConfigReadyV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: clusterv1beta1.MachineBootstrapConfigReadyV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 BootstrapConfigReady condition to false when MAPI Machine phase is Provisioning", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Provisioning").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.BootstrapConfigReadyV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1beta1.MachineBootstrapConfigNotReadyV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 InfrastructureReady condition to true when MAPI Machine phase is Running", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Running").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.InfrastructureReadyV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: clusterv1beta1.MachineInfrastructureReadyV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 InfrastructureReady condition to false when MAPI Machine phase is Provisioning", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Provisioning").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.InfrastructureReadyV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1beta1.MachineInfrastructureNotReadyV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 NodeReady condition to true when MAPI Machine phase is Running", func() {
			nodeRef := corev1.ObjectReference{
				Kind:      "Node",
				Name:      "test-node",
				Namespace: "",
			}

			mapiMachine := machinebuilder.Machine().
				WithPhase("Running").
				WithNodeRef(nodeRef).
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.MachineNodeReadyV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: clusterv1beta1.MachineNodeReadyV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 NodeReady condition to false when MAPI Machine phase is Running but NodeRef is not set", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Running").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.MachineNodeReadyV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1beta1.MachineNodeNotReadyV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 NodeReady condition to false when MAPI Machine phase is Provisioning", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Provisioning").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.MachineNodeReadyV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1beta1.MachineNodeNotReadyV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 Deleting condition to true when MAPI Machine phase is Deleting", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Deleting").
				WithDeletionTimestamp(ptr.To(metav1.Now())).
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.MachineDeletingV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: clusterv1beta1.MachineDeletingV1Beta2Reason,
			})))
		})

		It("should set CAPI Machine v1beta2 Deleting condition to false when MAPI Machine phase is not Deleting", func() {
			mapiMachine := machinebuilder.Machine().
				WithPhase("Provisioning").
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)
			Expect(errs).To(BeEmpty())
			Expect(capiStatus.V1Beta2.Conditions).To(ContainElement(testutils.MatchCondition(metav1.Condition{
				Type:   clusterv1beta1.MachineDeletingV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: clusterv1beta1.MachineNotDeletingV1Beta2Reason,
			})))
		})

		It("should return error and ignore unrecognized address types when addresses contain and invalid type", func() {
			// Create a machine with both valid and invalid address types
			mapiMachine := machinebuilder.Machine().
				WithAddresses([]corev1.NodeAddress{
					{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
					{Type: corev1.NodeAddressType("UnrecognizedType"), Address: "192.168.1.1"},
					{Type: corev1.NodeExternalIP, Address: "203.0.113.1"},
				}).
				Build()

			capiStatus, errs := convertMAPIMachineToCAPIMachineStatus(mapiMachine)

			// Should have only one error, and it should be for the unrecognized address type.
			Expect(errs).To(ConsistOf(field.ErrorList{
				&field.Error{
					Type:     field.ErrorTypeInvalid,
					Field:    "status.addresses",
					BadValue: "UnrecognizedType",
					Detail:   "UnrecognizedType unrecognized address type",
				},
			}))

			// Should only contain the valid addresses (ignoring the unrecognized one)
			Expect(capiStatus.Addresses).To(SatisfyAll(
				HaveLen(2),
				ContainElement(SatisfyAll(HaveField("Type", BeEquivalentTo(clusterv1beta1.MachineInternalIP)), HaveField("Address", Equal("10.0.0.1")))),
				ContainElement(SatisfyAll(HaveField("Type", BeEquivalentTo(clusterv1beta1.MachineExternalIP)), HaveField("Address", Equal("203.0.113.1")))),
				Not(ContainElement(HaveField("Address", Equal("192.168.1.1")))),
			))
		})
	})
})

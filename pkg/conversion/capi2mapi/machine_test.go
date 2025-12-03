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
package capi2mapi

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	capibuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	capabuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capierrors "sigs.k8s.io/cluster-api/errors"
)

var _ = Describe("capi2mapi Machine conversion", func() {
	var (
		capiMachineBase = capibuilder.Machine()
	)

	type capi2MAPIMachineConversionInput struct {
		machineBuilder   capibuilder.MachineBuilder
		expectedErrors   []string
		expectedWarnings []string
		assertion        func(machine *mapiv1beta1.Machine)
	}

	var _ = DescribeTable("capi2mapi convert CAPI Machine/InfraMachine/InfraCluster to a MAPI Machine",
		func(in capi2MAPIMachineConversionInput) {
			m := FromMachineAndAWSMachineAndAWSCluster(
				in.machineBuilder.Build(),
				capabuilder.AWSMachine().Build(),
				capabuilder.AWSCluster().Build(),
			)
			machine, warns, err := m.ToMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting CAPI resources to MAPI Machine")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting CAPI resources to MAPI Machine")
			if in.assertion != nil {
				in.assertion(machine)
			}
		},

		// Base Case.
		Entry("With a Base configuration", capi2MAPIMachineConversionInput{
			machineBuilder:   capiMachineBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
		Entry("With unsupported Version", capi2MAPIMachineConversionInput{
			machineBuilder:   capiMachineBase.WithVersion(ptr.To("v1.1.1")),
			expectedErrors:   []string{"spec.version: Invalid value: \"v1.1.1\": version is not supported"},
			expectedWarnings: []string{},
		}),
		Entry("With unsupported NodeDrainTimeout", capi2MAPIMachineConversionInput{
			machineBuilder:   capiMachineBase.WithNodeDrainTimeout(ptr.To(metav1.Duration{Duration: 1 * time.Second})),
			expectedErrors:   []string{"spec.nodeDrainTimeout: Invalid value: v1.Duration{Duration:1000000000}: nodeDrainTimeout is not supported"},
			expectedWarnings: []string{},
		}),
		Entry("With unsupported NodeVolumeDetachTimeout", capi2MAPIMachineConversionInput{
			machineBuilder:   capiMachineBase.WithNodeVolumeDetachTimeout(ptr.To(metav1.Duration{Duration: 1 * time.Second})),
			expectedErrors:   []string{"spec.nodeVolumeDetachTimeout: Invalid value: v1.Duration{Duration:1000000000}: nodeVolumeDetachTimeout is not supported"},
			expectedWarnings: []string{},
		}),
		Entry("With unsupported NodeDeletionTimeout", capi2MAPIMachineConversionInput{
			machineBuilder:   capiMachineBase.WithNodeDeletionTimeout(ptr.To(metav1.Duration{Duration: 1 * time.Second})),
			expectedErrors:   []string{"spec.nodeDeletionTimeout: Invalid value: v1.Duration{Duration:1000000000}: nodeDeletionTimeout is not supported"},
			expectedWarnings: []string{},
		}),
		Entry("With delete-machine annotation", capi2MAPIMachineConversionInput{
			machineBuilder:   capiMachineBase.WithAnnotations(map[string]string{clusterv1beta1.DeleteMachineAnnotation: "true"}),
			expectedErrors:   []string{},
			expectedWarnings: []string{},
			assertion: func(machine *mapiv1beta1.Machine) {
				Expect(machine.Annotations).To(HaveKeyWithValue(util.MapiDeleteMachineAnnotation, "true"))
				Expect(machine.Annotations).ToNot(HaveKey(clusterv1beta1.DeleteMachineAnnotation))
			},
		}),
	)
})

var _ = Describe("capi2mapi Machine Status Conversion", func() {
	Context("when converting CAPI Machine status to MAPI", func() {
		It("should set all MAPI Machine status fields and conditions to the expected values", func() {
			// Set CAPI machine status fields
			nodeRef := &corev1.ObjectReference{
				Kind:      "Node",
				Name:      "test-node",
				Namespace: "",
			}
			lastUpdated := &metav1.Time{Time: time.Now()}
			condition := clusterv1beta1.Condition{
				Type: "Available", Status: corev1.ConditionTrue,
				Severity: clusterv1beta1.ConditionSeverityNone,
				Reason:   "MachineAvailable", Message: "Machine is available",
			}

			capiMachine := capibuilder.Machine().
				WithName("test-machine").
				WithNamespace("test-namespace").
				WithNodeRef(nodeRef).
				WithLastUpdated(lastUpdated).
				WithAddresses(clusterv1beta1.MachineAddresses{
					{Type: clusterv1beta1.MachineAddressType(corev1.NodeInternalIP), Address: "10.0.0.1"},
					{Type: clusterv1beta1.MachineAddressType(corev1.NodeExternalIP), Address: "203.0.113.1"},
				}).
				WithPhase("Running").
				WithFailureReason(ptr.To(capierrors.MachineStatusError("InvalidConfiguration"))).
				WithFailureMessage(ptr.To(string("Test failure message"))).
				WithConditions([]clusterv1beta1.Condition{condition}).
				Build()

			mapiStatus, errs := convertCAPIMachineStatusToMAPI(capiMachine.Status)
			Expect(errs).To(BeEmpty())

			Expect(mapiStatus.NodeRef).To(Equal(nodeRef))
			Expect(mapiStatus.LastUpdated).To(Equal(lastUpdated))
			Expect(mapiStatus.Addresses).To(ConsistOf(
				SatisfyAll(HaveField("Type", corev1.NodeInternalIP), HaveField("Address", "10.0.0.1")),
				SatisfyAll(HaveField("Type", corev1.NodeExternalIP), HaveField("Address", "203.0.113.1")),
			))

			Expect(mapiStatus.Phase).To(HaveValue(BeEquivalentTo(mapiv1beta1.PhaseRunning)))
			Expect(mapiStatus.ErrorReason).To(HaveValue(BeEquivalentTo(mapiv1beta1.MachineStatusError("InvalidConfiguration"))))
			Expect(mapiStatus.ErrorMessage).To(HaveValue(BeEquivalentTo("Test failure message")))

			// We do not convert these conditions to MAPI conditions as they are not a 1:1 mapping conversion between CAPI and MAPI.
			Expect(mapiStatus.Conditions).To(BeNil())
		})

		It("should set all MAPI Machine status fields to empty when CAPI MachineStatus is empty", func() {
			capiMachine := capibuilder.Machine().
				WithName("test-machine").
				WithNamespace("test-namespace").
				Build()

			mapiStatus, errs := convertCAPIMachineStatusToMAPI(capiMachine.Status)
			Expect(errs).To(BeEmpty())

			Expect(mapiStatus.NodeRef).To(BeNil())
			Expect(mapiStatus.LastUpdated).To(BeNil())
			Expect(mapiStatus.Addresses).To(BeEmpty())
			Expect(mapiStatus.Phase).To(BeNil())
			Expect(mapiStatus.ErrorReason).To(BeNil())
			Expect(mapiStatus.ErrorMessage).To(BeNil())
			Expect(mapiStatus.Conditions).To(BeEmpty())
		})
	})
})

/*
Copyright 2026 Red Hat, Inc.

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

package mapi2capi_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/consts"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/api/govmomi/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("vSphere status bridge characterization", func() {
	infra := &configv1.Infrastructure{Status: configv1.InfrastructureStatus{InfrastructureName: "test-cluster"}}
	infraCluster := &vspherev1.VSphereCluster{Spec: vspherev1.VSphereClusterSpec{Server: "vcenter.example.com"}}

	DescribeTable("should round-trip the existing synthetic readiness bridge", func(ready bool, observedState, instanceState string) {
		machine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "openshift-cluster-api", Annotations: map[string]string{consts.MAPIMachineMetadataAnnotationInstanceState: observedState}},
			Spec:       clusterv1.MachineSpec{ClusterName: "test-cluster", Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("worker-user-data")}},
		}
		infraMachine := &vspherev1.VSphereMachine{
			Spec: vspherev1.VSphereMachineSpec{VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
				Template: "worker-template", Server: "vcenter.example.com", Datacenter: "dc", CloneMode: vspherev1.FullClone,
			}},
			Status: vspherev1.VSphereMachineStatus{Ready: ready},
		}
		originalMachine, originalInfraMachine := machine.DeepCopy(), infraMachine.DeepCopy()

		By("Converting CAPV readiness to the current synthetic Machine API state")

		converted, warnings, err := capi2mapi.FromMachineAndVSphereMachineAndVSphereCluster(machine, infraMachine, infraCluster).ToMachine()
		Expect(err).NotTo(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		providerStatus, err := mapi2capi.VSphereProviderStatusFromRawExtension(converted.Status.ProviderStatus)
		Expect(err).NotTo(HaveOccurred())
		Expect(providerStatus.InstanceState).To(HaveValue(Equal(instanceState)))

		By("Converting back without using the shared fuzz comparison")

		_, returnedObject, warnings, err := mapi2capi.FromVSphereMachineAndInfra(converted, infra).ToMachineAndInfrastructureMachine()
		Expect(err).NotTo(HaveOccurred())
		Expect(warnings).To(BeEmpty())
		Expect(returnedObject).To(BeAssignableToTypeOf(&vspherev1.VSphereMachine{}))
		// Reverse readiness still recognizes only the synthetic ready state, not power observations.
		Expect(returnedObject.(*vspherev1.VSphereMachine).Status.Ready).To(Equal(instanceState == "ready"))
		Expect(machine).To(Equal(originalMachine))
		Expect(infraMachine).To(Equal(originalInfraMachine))
	},
		Entry("ready becomes ready and returns true", true, "", "ready"),
		Entry("not ready becomes empty state and returns false", false, "", ""),
		Entry("powered-off snapshot does not override ready", true, "poweredOff", "poweredOff"),
		Entry("powered-on snapshot does not imply ready", false, "poweredOn", "poweredOn"),
		Entry("suspended snapshot does not override ready", true, "suspended", "suspended"),
		Entry("unknown annotation retains the ready fallback", true, "unknown", "ready"),
	)

	DescribeTable("should preserve observed power state and provisioned readiness independently", func(powerState, annotationState, expectedState string) {
		status := mapiv1beta1.VSphereMachineProviderStatus{
			InstanceState: ptr.To(powerState),
			Conditions:    []metav1.Condition{{Type: string(mapiv1beta1.MachineCreation), Status: metav1.ConditionTrue, Reason: mapiv1beta1.MachineCreationSucceededConditionReason}},
		}
		raw, err := json.Marshal(status)
		Expect(err).NotTo(HaveOccurred())

		machine := machinebuilder.Machine().
			WithProviderSpecBuilder(machinebuilder.VSphereProviderSpec()).
			WithProviderID(ptr.To("vsphere://42100000-0000-0000-0000-000000000001")).
			WithPhase("Running").
			WithAddresses([]corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.0.2.10"}}).
			WithProviderStatus(runtime.RawExtension{Raw: raw}).Build()
		machine.Annotations = map[string]string{consts.MAPIMachineMetadataAnnotationInstanceState: annotationState}
		original := machine.DeepCopy()

		By("Deriving provisioning readiness from the Running phase, independently of power")

		coreMachine, infraObject, warnings, err := mapi2capi.FromVSphereMachineAndInfra(machine, infra).ToMachineAndInfrastructureMachine()
		Expect(err).NotTo(HaveOccurred())
		Expect(warnings).To(BeEmpty())
		Expect(infraObject).To(BeAssignableToTypeOf(&vspherev1.VSphereMachine{}))

		infraMachine := infraObject.(*vspherev1.VSphereMachine)

		Expect(coreMachine.Annotations).To(HaveKeyWithValue(consts.MAPIMachineMetadataAnnotationInstanceState, expectedState))
		Expect(infraMachine.Annotations).To(HaveKeyWithValue(consts.MAPIMachineMetadataAnnotationInstanceState, expectedState))
		Expect(infraMachine.Status.Ready).To(BeTrue())
		Expect(infraMachine.Status.Addresses).NotTo(BeEmpty())

		By("Preserving both the power observation and creation-success condition")

		returned, warnings, err := capi2mapi.FromMachineAndVSphereMachineAndVSphereCluster(coreMachine, infraMachine, infraCluster).ToMachine()
		Expect(err).NotTo(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		returnedStatus, err := mapi2capi.VSphereProviderStatusFromRawExtension(returned.Status.ProviderStatus)
		Expect(err).NotTo(HaveOccurred())
		Expect(returnedStatus.InstanceState).To(HaveValue(Equal(expectedState)))
		Expect(returned.Annotations).To(HaveKeyWithValue(consts.MAPIMachineMetadataAnnotationInstanceState, expectedState))
		Expect(returnedStatus.Conditions).To(ContainElement(SatisfyAll(
			HaveField("Type", Equal(string(mapiv1beta1.MachineCreation))),
			HaveField("Status", Equal(metav1.ConditionTrue)),
		)))
		Expect(machine).To(Equal(original))
	},
		Entry("powered-on Machine without annotation", "poweredOn", "", "poweredOn"),
		Entry("powered-off Machine without annotation", "poweredOff", "", "poweredOff"),
		Entry("suspended Machine without annotation", "suspended", "", "suspended"),
		Entry("matching annotation", "poweredOn", "poweredOn", "poweredOn"),
		Entry("provider status wins over conflicting annotation", "poweredOn", "poweredOff", "poweredOn"),
		Entry("annotation-only power observation", "", "poweredOff", "poweredOff"),
	)
})

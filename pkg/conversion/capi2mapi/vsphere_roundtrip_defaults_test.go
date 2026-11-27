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

package capi2mapi_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/api/govmomi/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("vSphere explicit defaults", func() {
	DescribeTable("should compare supported round-trip specs independently", func(mode vspherev1.CloneMode, powerOffMode vspherev1.VirtualMachinePowerOpMode, addresses []string, pools []corev1.TypedLocalObjectReference) {
		machine := &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "openshift-cluster-api"}, Spec: clusterv1.MachineSpec{ClusterName: "test-cluster", Bootstrap: clusterv1.Bootstrap{DataSecretName: ptr.To("worker-user-data")}}}
		vm := &vspherev1.VSphereMachine{Spec: vspherev1.VSphereMachineSpec{VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{Template: "worker-template", Server: "vcenter.example.com", Datacenter: "dc", CloneMode: mode, Network: vspherev1.NetworkSpec{Devices: []vspherev1.NetworkDeviceSpec{{NetworkName: "worker-network", IPAddrs: addresses, AddressesFromPools: pools}}}}, PowerOffMode: powerOffMode}}
		original := vm.DeepCopy()
		converted, warnings, err := capi2mapi.FromMachineAndVSphereMachineAndVSphereCluster(machine, vm, &vspherev1.VSphereCluster{}).ToMachine()
		Expect(err).NotTo(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		_, object, warnings, err := mapi2capi.FromVSphereMachineAndInfra(converted, &configv1.Infrastructure{Status: configv1.InfrastructureStatus{InfrastructureName: "test-cluster"}}).ToMachineAndInfrastructureMachine()
		Expect(err).NotTo(HaveOccurred())
		Expect(warnings).To(BeEmpty())

		expected := original.DeepCopy()
		expected.Spec.PowerOffMode = vspherev1.VirtualMachinePowerOpModeHard
		expected.Spec.Network.Devices[0].DHCP4 = len(addresses) == 0 && len(pools) == 0
		Expect(object).To(HaveField("Spec", Equal(expected.Spec)))
		Expect(vm).To(Equal(original))

		machineSet := &clusterv1.MachineSet{ObjectMeta: machine.ObjectMeta, Spec: clusterv1.MachineSetSpec{Template: clusterv1.MachineTemplateSpec{Spec: machine.Spec}}}
		template := &vspherev1.VSphereMachineTemplate{Spec: vspherev1.VSphereMachineTemplateSpec{Template: vspherev1.VSphereMachineTemplateResource{Spec: original.Spec}}}
		originalTemplate := template.DeepCopy()
		convertedSet, warnings, err := capi2mapi.FromMachineSetAndVSphereMachineTemplateAndVSphereCluster(machineSet, template, &vspherev1.VSphereCluster{}).ToMachineSet()
		Expect(err).NotTo(HaveOccurred())
		Expect(warnings).To(BeEmpty())
		_, returnedTemplate, warnings, err := mapi2capi.FromVSphereMachineSetAndInfra(convertedSet, &configv1.Infrastructure{Status: configv1.InfrastructureStatus{InfrastructureName: "test-cluster"}}).ToMachineSetAndMachineTemplate()
		Expect(err).NotTo(HaveOccurred())
		Expect(warnings).To(BeEmpty())
		Expect(returnedTemplate).To(HaveField("Spec.Template.Spec", Equal(expected.Spec)))
		Expect(template).To(Equal(originalTemplate))

		By("Keeping the Machine API omitted-clone-mode default as explicit fullClone")

		var providerSpec mapiv1beta1.VSphereMachineProviderSpec
		Expect(json.Unmarshal(converted.Spec.ProviderSpec.Value.Raw, &providerSpec)).To(Succeed())
		providerSpec.CloneMode = ""
		converted.Spec.ProviderSpec.Value.Raw, err = json.Marshal(providerSpec)
		Expect(err).NotTo(HaveOccurred())
		_, defaulted, _, err := mapi2capi.FromVSphereMachineAndInfra(converted, &configv1.Infrastructure{Status: configv1.InfrastructureStatus{InfrastructureName: "test-cluster"}}).ToMachineAndInfrastructureMachine()
		Expect(err).NotTo(HaveOccurred())
		Expect(defaulted).To(HaveField("Spec.CloneMode", Equal(vspherev1.FullClone)))
	},
		Entry("full clone with omitted power-off mode and DHCP default", vspherev1.FullClone, vspherev1.VirtualMachinePowerOpMode(""), []string(nil), []corev1.TypedLocalObjectReference(nil)),
		Entry("linked clone with hard power-off and static address", vspherev1.LinkedClone, vspherev1.VirtualMachinePowerOpModeHard, []string{"192.0.2.10"}, []corev1.TypedLocalObjectReference(nil)),
		Entry("IPAM pool disables DHCP", vspherev1.FullClone, vspherev1.VirtualMachinePowerOpModeHard, []string(nil), []corev1.TypedLocalObjectReference{{APIGroup: ptr.To("ipam.cluster.x-k8s.io"), Kind: "InClusterIPPool", Name: "worker-pool"}}),
	)

	It("should reject omitted CAPV clone mode rather than guess snapshot availability", func() {
		_, _, err := capi2mapi.FromMachineAndVSphereMachineAndVSphereCluster(&clusterv1.Machine{}, &vspherev1.VSphereMachine{}, &vspherev1.VSphereCluster{}).ToMachine()
		Expect(err).To(MatchError(ContainSubstring("cloneMode must be explicitly set")))
		_, _, err = capi2mapi.FromMachineSetAndVSphereMachineTemplateAndVSphereCluster(&clusterv1.MachineSet{}, &vspherev1.VSphereMachineTemplate{}, &vspherev1.VSphereCluster{}).ToMachineSet()
		Expect(err).To(MatchError(ContainSubstring("cloneMode must be explicitly set")))
	})
})

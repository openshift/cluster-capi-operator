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
package capi2mapi

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
	"k8s.io/utils/ptr"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("capi2mapi vSphere conversion", func() {
	var (
		vsphereCAPIMachineBase = &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "test-namespace",
			},
			Spec: clusterv1.MachineSpec{
				Bootstrap: clusterv1.Bootstrap{
					DataSecretName: ptr.To("test-bootstrap-secret"),
				},
			},
		}

		vsphereCAPIVSphereMachineBase = &vspherev1.VSphereMachine{
			Spec: vspherev1.VSphereMachineSpec{
				VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
					Template:   "test-template",
					Server:     "vcenter.example.com",
					Datacenter: "test-datacenter",
					Folder:     "test-folder",
					Datastore:  "test-datastore",
					NumCPUs:    4,
					MemoryMiB:  8192,
					DiskGiB:    120,
				},
			},
		}

		vsphereCAPIVSphereClusterBase = &vspherev1.VSphereCluster{
			Spec: vspherev1.VSphereClusterSpec{
				Server: "vcenter.example.com",
			},
		}
	)

	type vsphereCAPI2MAPIMachineConversionInput struct {
		machine          *clusterv1.Machine
		vsphereMachine   *vspherev1.VSphereMachine
		vsphereCluster   *vspherev1.VSphereCluster
		expectedErrors   []string
		expectedWarnings []string
	}

	type vsphereCAPI2MAPIMachinesetConversionInput struct {
		machineSet             *clusterv1.MachineSet
		vsphereMachineTemplate *vspherev1.VSphereMachineTemplate
		vsphereCluster         *vspherev1.VSphereCluster
		expectedErrors         []string
		expectedWarnings       []string
	}

	var _ = DescribeTable("capi2mapi vSphere convert CAPI Machine/VSphereMachine/VSphereCluster to a MAPI Machine",
		func(in vsphereCAPI2MAPIMachineConversionInput) {
			_, warns, err := FromMachineAndVSphereMachineAndVSphereCluster(
				in.machine,
				in.vsphereMachine,
				in.vsphereCluster,
			).ToMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting vSphere CAPI resources to MAPI Machine")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting vSphere CAPI resources to MAPI Machine")
		},

		// Base Case.
		Entry("With a Base configuration", vsphereCAPI2MAPIMachineConversionInput{
			machine:          vsphereCAPIMachineBase,
			vsphereMachine:   vsphereCAPIVSphereMachineBase,
			vsphereCluster:   vsphereCAPIVSphereClusterBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		// Clone Mode Tests.
		Entry("With full clone mode", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template:  "test-template",
						CloneMode: vspherev1.FullClone,
					},
				},
			},
			vsphereCluster:   vsphereCAPIVSphereClusterBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With linked clone mode", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template:  "test-template",
						CloneMode: vspherev1.LinkedClone,
					},
				},
			},
			vsphereCluster:   vsphereCAPIVSphereClusterBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported clone mode", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template:  "test-template",
						CloneMode: "unsupported-mode",
					},
				},
			},
			vsphereCluster: vsphereCAPIVSphereClusterBase,
			expectedErrors: []string{
				"spec.cloneMode: Invalid value: \"unsupported-mode\": unable to convert clone mode, unknown value",
			},
			expectedWarnings: []string{},
		}),

		// Data Disk Tests.
		Entry("With data disk - thin provisioning", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template: "test-template",
						DataDisks: []vspherev1.VSphereDisk{
							{
								Name:             "disk1",
								SizeGiB:          100,
								ProvisioningMode: vspherev1.ThinProvisioningMode,
							},
						},
					},
				},
			},
			vsphereCluster:   vsphereCAPIVSphereClusterBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With data disk - thick provisioning", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template: "test-template",
						DataDisks: []vspherev1.VSphereDisk{
							{
								Name:             "disk1",
								SizeGiB:          100,
								ProvisioningMode: vspherev1.ThickProvisioningMode,
							},
						},
					},
				},
			},
			vsphereCluster:   vsphereCAPIVSphereClusterBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With data disk - eagerly zeroed provisioning", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template: "test-template",
						DataDisks: []vspherev1.VSphereDisk{
							{
								Name:             "disk1",
								SizeGiB:          100,
								ProvisioningMode: vspherev1.EagerlyZeroedProvisioningMode,
							},
						},
					},
				},
			},
			vsphereCluster:   vsphereCAPIVSphereClusterBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported provisioning mode in data disk", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template: "test-template",
						DataDisks: []vspherev1.VSphereDisk{
							{
								Name:             "disk1",
								SizeGiB:          100,
								ProvisioningMode: "invalid-mode",
							},
						},
					},
				},
			},
			vsphereCluster: vsphereCAPIVSphereClusterBase,
			expectedErrors: []string{
				"spec.dataDisks[0].provisioningMode: Invalid value: \"invalid-mode\": unable to convert provisioning mode, unknown value",
			},
			expectedWarnings: []string{},
		}),

		Entry("With multiple data disks", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template: "test-template",
						DataDisks: []vspherev1.VSphereDisk{
							{Name: "disk1", SizeGiB: 100, ProvisioningMode: vspherev1.ThinProvisioningMode},
							{Name: "disk2", SizeGiB: 200, ProvisioningMode: vspherev1.ThickProvisioningMode},
							{Name: "disk3", SizeGiB: 50},
						},
					},
				},
			},
			vsphereCluster:   vsphereCAPIVSphereClusterBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		// Network Tests.
		Entry("With network devices", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template: "test-template",
						Network: vspherev1.NetworkSpec{
							Devices: []vspherev1.NetworkDeviceSpec{
								{
									NetworkName: "VM Network",
									Gateway4:    "192.168.1.1",
									IPAddrs:     []string{"192.168.1.100/24"},
									Nameservers: []string{"8.8.8.8"},
								},
							},
						},
					},
				},
			},
			vsphereCluster:   vsphereCAPIVSphereClusterBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With DHCP network configuration", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template: "test-template",
						Network: vspherev1.NetworkSpec{
							Devices: []vspherev1.NetworkDeviceSpec{
								{
									NetworkName: "VM Network",
									DHCP4:       true,
								},
							},
						},
					},
				},
			},
			vsphereCluster:   vsphereCAPIVSphereClusterBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With both DHCP and static IPs (warning case)", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template: "test-template",
						Network: vspherev1.NetworkSpec{
							Devices: []vspherev1.NetworkDeviceSpec{
								{
									NetworkName: "VM Network",
									DHCP4:       true,
									IPAddrs:     []string{"192.168.1.100/24"},
								},
							},
						},
					},
				},
			},
			vsphereCluster: vsphereCAPIVSphereClusterBase,
			expectedErrors: []string{},
			expectedWarnings: []string{
				"device 0 has both DHCP and static IPs configured, MAPI will use static IPs",
			},
		}),

		// Tags Test.
		Entry("With tags", vsphereCAPI2MAPIMachineConversionInput{
			machine: vsphereCAPIMachineBase,
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
						Template: "test-template",
						TagIDs: []string{
							"urn:vmomi:InventoryServiceTag:5736bf56-49f5-4667-b38c-b97e09dc9578:GLOBAL",
						},
					},
				},
			},
			vsphereCluster:   vsphereCAPIVSphereClusterBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
	)

	var _ = DescribeTable("capi2mapi vSphere convert CAPI MachineSet/VSphereMachineTemplate/VSphereCluster to a MAPI MachineSet",
		func(in vsphereCAPI2MAPIMachinesetConversionInput) {
			_, warns, err := FromMachineSetAndVSphereMachineTemplateAndVSphereCluster(
				in.machineSet,
				in.vsphereMachineTemplate,
				in.vsphereCluster,
			).ToMachineSet()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting vSphere CAPI resources to MAPI MachineSet")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting vSphere CAPI resources to MAPI MachineSet")
		},

		// Base Case.
		Entry("With a Base configuration", vsphereCAPI2MAPIMachinesetConversionInput{
			machineSet: &clusterv1.MachineSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-machineset",
					Namespace: "test-namespace",
				},
				Spec: clusterv1.MachineSetSpec{
					Replicas: ptr.To(int32(3)),
					Template: clusterv1.MachineTemplateSpec{
						Spec: clusterv1.MachineSpec{
							Bootstrap: clusterv1.Bootstrap{
								DataSecretName: ptr.To("test-bootstrap-secret"),
							},
						},
					},
				},
			},
			vsphereMachineTemplate: &vspherev1.VSphereMachineTemplate{
				Spec: vspherev1.VSphereMachineTemplateSpec{
					Template: vspherev1.VSphereMachineTemplateResource{
						Spec: vspherev1.VSphereMachineSpec{
							VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
								Template:   "test-template",
								Server:     "vcenter.example.com",
								Datacenter: "test-datacenter",
								NumCPUs:    4,
								MemoryMiB:  8192,
								DiskGiB:    120,
							},
						},
					},
				},
			},
			vsphereCluster:   vsphereCAPIVSphereClusterBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
	)
})

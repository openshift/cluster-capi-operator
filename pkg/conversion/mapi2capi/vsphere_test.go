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
package mapi2capi

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("mapi2capi VSphere conversion", func() {
	var (
		vsphereBaseProviderSpec   = machinebuilder.VSphereProviderSpec()
		vsphereMAPIMachineBase    = machinebuilder.Machine().WithProviderSpecBuilder(vsphereBaseProviderSpec)
		vsphereMAPIMachineSetBase = machinebuilder.MachineSet().WithProviderSpecBuilder(vsphereBaseProviderSpec)

		infra = &configv1.Infrastructure{
			Spec:   configv1.InfrastructureSpec{},
			Status: configv1.InfrastructureStatus{InfrastructureName: "sample-cluster-name"},
		}
	)

	type vsphereMAPI2CAPIConversionInput struct {
		machineBuilder   machinebuilder.MachineBuilder
		infra            *configv1.Infrastructure
		expectedErrors   []string
		expectedWarnings []string
	}

	type vsphereMAPI2CAPIMachinesetConversionInput struct {
		machineSetBuilder machinebuilder.MachineSetBuilder
		infra             *configv1.Infrastructure
		expectedErrors    []string
		expectedWarnings  []string
	}

	var mustConvertVSphereProviderSpecToRawExtension = func(spec *mapiv1beta1.VSphereMachineProviderSpec) *runtime.RawExtension {
		if spec == nil {
			return &runtime.RawExtension{}
		}

		rawBytes, err := json.Marshal(spec)
		if err != nil {
			panic(fmt.Sprintf("unable to convert (marshal) test VSphereMachineProviderSpec to runtime.RawExtension: %v", err))
		}

		return &runtime.RawExtension{
			Raw: rawBytes,
		}
	}

	var _ = DescribeTable("mapi2capi VSphere convert MAPI Machine",
		func(in vsphereMAPI2CAPIConversionInput) {
			_, _, warns, err := FromVSphereMachineAndInfra(in.machineBuilder.Build(), in.infra).ToMachineAndInfrastructureMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors), "should match expected errors while converting a VSphere MAPI Machine to CAPI")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings), "should match expected warnings while converting a VSphere MAPI Machine to CAPI")
		},

		// Base Case.
		Entry("With a Base configuration", vsphereMAPI2CAPIConversionInput{
			machineBuilder:   vsphereMAPIMachineBase,
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With tags", vsphereMAPI2CAPIConversionInput{
			machineBuilder: vsphereMAPIMachineBase.WithProviderSpecBuilder(
				vsphereBaseProviderSpec.WithTags([]string{
					"urn:vmomi:InventoryServiceTag:5736bf56-49f5-4667-b38c-b97e09dc9578:GLOBAL",
				}),
			),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With IP pool", vsphereMAPI2CAPIConversionInput{
			machineBuilder: vsphereMAPIMachineBase.WithProviderSpecBuilder(
				vsphereBaseProviderSpec.WithIPPool(),
			),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		// Error Cases.
		Entry("With empty infrastructure name", vsphereMAPI2CAPIConversionInput{
			machineBuilder: vsphereMAPIMachineBase,
			infra: &configv1.Infrastructure{
				Status: configv1.InfrastructureStatus{InfrastructureName: ""},
			},
			expectedErrors: []string{
				"infrastructure.status.infrastructureName: Invalid value: \"\": infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty",
			},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported provisioning mode in data disk", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					DataDisks: []mapiv1beta1.VSphereDisk{
						{
							Name:             "disk1",
							SizeGiB:          100,
							ProvisioningMode: "invalid-mode",
						},
					},
				}),
			}),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.dataDisks[0].provisioningMode: Invalid value: \"invalid-mode\": unsupported provisioning mode",
			},
			expectedWarnings: []string{},
		}),

		// Data Disk Tests.
		Entry("With data disk - thin provisioning", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					DataDisks: []mapiv1beta1.VSphereDisk{
						{
							Name:             "disk1",
							SizeGiB:          100,
							ProvisioningMode: mapiv1beta1.ProvisioningModeThin,
						},
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With data disk - thick provisioning", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					DataDisks: []mapiv1beta1.VSphereDisk{
						{
							Name:             "disk1",
							SizeGiB:          100,
							ProvisioningMode: mapiv1beta1.ProvisioningModeThick,
						},
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With data disk - eagerly zeroed provisioning", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					DataDisks: []mapiv1beta1.VSphereDisk{
						{
							Name:             "disk1",
							SizeGiB:          100,
							ProvisioningMode: mapiv1beta1.ProvisioningModeEagerlyZeroed,
						},
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With data disk - no provisioning mode specified", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					DataDisks: []mapiv1beta1.VSphereDisk{
						{
							Name:    "disk1",
							SizeGiB: 100,
						},
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With multiple data disks", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					DataDisks: []mapiv1beta1.VSphereDisk{
						{Name: "disk1", SizeGiB: 100, ProvisioningMode: mapiv1beta1.ProvisioningModeThin},
						{Name: "disk2", SizeGiB: 200, ProvisioningMode: mapiv1beta1.ProvisioningModeThick},
						{Name: "disk3", SizeGiB: 50},
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		// Clone Mode Tests.
		Entry("With full clone mode", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template:  "test-template",
					CloneMode: mapiv1beta1.FullClone,
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With linked clone mode", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template:  "test-template",
					CloneMode: mapiv1beta1.LinkedClone,
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With linked clone mode and snapshot", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template:  "test-template",
					CloneMode: mapiv1beta1.LinkedClone,
					Snapshot:  "snapshot-1",
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		// Workspace Tests.
		Entry("With workspace configuration", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					Workspace: &mapiv1beta1.Workspace{
						Server:       "vcenter.example.com",
						Datacenter:   "dc1",
						Folder:       "/vm/folder",
						Datastore:    "datastore1",
						ResourcePool: "pool1",
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		// VM Configuration Tests.
		Entry("With custom VM configuration", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template:          "test-template",
					NumCPUs:           8,
					NumCoresPerSocket: 4,
					MemoryMiB:         16384,
					DiskGiB:           120,
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		// Secret Tests.
		Entry("With custom credentials secret", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					CredentialsSecret: &corev1.LocalObjectReference{
						Name: "custom-vsphere-credentials",
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With user data secret", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					UserDataSecret: &corev1.LocalObjectReference{
						Name: "worker-user-data",
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		// Network Configuration Tests.
		Entry("With network devices", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					Network: mapiv1beta1.NetworkSpec{
						Devices: []mapiv1beta1.NetworkDeviceSpec{
							{NetworkName: "network1"},
						},
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With multiple network devices", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					Network: mapiv1beta1.NetworkSpec{
						Devices: []mapiv1beta1.NetworkDeviceSpec{
							{NetworkName: "network1"},
							{NetworkName: "network2"},
						},
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		// Comprehensive Test.
		Entry("With comprehensive configuration", vsphereMAPI2CAPIConversionInput{
			machineBuilder: machinebuilder.Machine().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template:          "test-template",
					CloneMode:         mapiv1beta1.FullClone,
					Snapshot:          "snapshot-1",
					NumCPUs:           8,
					NumCoresPerSocket: 4,
					MemoryMiB:         16384,
					DiskGiB:           120,
					TagIDs: []string{
						"urn:vmomi:InventoryServiceTag:5736bf56-49f5-4667-b38c-b97e09dc9578:GLOBAL",
					},
					Workspace: &mapiv1beta1.Workspace{
						Server:       "vcenter.example.com",
						Datacenter:   "dc1",
						Folder:       "/vm/folder",
						Datastore:    "datastore1",
						ResourcePool: "pool1",
					},
					Network: mapiv1beta1.NetworkSpec{
						Devices: []mapiv1beta1.NetworkDeviceSpec{
							{NetworkName: "network1"},
						},
					},
					DataDisks: []mapiv1beta1.VSphereDisk{
						{Name: "disk1", SizeGiB: 100, ProvisioningMode: mapiv1beta1.ProvisioningModeThin},
					},
					UserDataSecret: &corev1.LocalObjectReference{
						Name: "worker-user-data",
					},
					CredentialsSecret: &corev1.LocalObjectReference{
						Name: "vsphere-cloud-credentials",
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
	)

	var _ = DescribeTable("mapi2capi VSphere convert MAPI MachineSet",
		func(in vsphereMAPI2CAPIMachinesetConversionInput) {
			_, _, warns, err := FromVSphereMachineSetAndInfra(in.machineSetBuilder.Build(), in.infra).ToMachineSetAndMachineTemplate()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors), "should match expected errors while converting a VSphere MAPI MachineSet to CAPI")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings), "should match expected warnings while converting a VSphere MAPI MachineSet to CAPI")
		},

		Entry("With a Base configuration", vsphereMAPI2CAPIMachinesetConversionInput{
			machineSetBuilder: vsphereMAPIMachineSetBase,
			infra:             infra,
			expectedErrors:    []string{},
			expectedWarnings:  []string{},
		}),

		Entry("With tags", vsphereMAPI2CAPIMachinesetConversionInput{
			machineSetBuilder: vsphereMAPIMachineSetBase.WithProviderSpecBuilder(
				vsphereBaseProviderSpec.WithTags([]string{
					"urn:vmomi:InventoryServiceTag:5736bf56-49f5-4667-b38c-b97e09dc9578:GLOBAL",
				}),
			),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With data disks", vsphereMAPI2CAPIMachinesetConversionInput{
			machineSetBuilder: machinebuilder.MachineSet().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					DataDisks: []mapiv1beta1.VSphereDisk{
						{Name: "disk1", SizeGiB: 100, ProvisioningMode: mapiv1beta1.ProvisioningModeThin},
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With workspace configuration", vsphereMAPI2CAPIMachinesetConversionInput{
			machineSetBuilder: machinebuilder.MachineSet().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template: "test-template",
					Workspace: &mapiv1beta1.Workspace{
						Server:       "vcenter.example.com",
						Datacenter:   "dc1",
						Folder:       "/vm/folder",
						Datastore:    "datastore1",
						ResourcePool: "pool1",
					},
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With custom VM configuration", vsphereMAPI2CAPIMachinesetConversionInput{
			machineSetBuilder: machinebuilder.MachineSet().WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertVSphereProviderSpecToRawExtension(&mapiv1beta1.VSphereMachineProviderSpec{
					Template:          "test-template",
					NumCPUs:           8,
					NumCoresPerSocket: 4,
					MemoryMiB:         16384,
					DiskGiB:           120,
				}),
			}),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With empty infrastructure name", vsphereMAPI2CAPIMachinesetConversionInput{
			machineSetBuilder: vsphereMAPIMachineSetBase,
			infra: &configv1.Infrastructure{
				Status: configv1.InfrastructureStatus{InfrastructureName: ""},
			},
			expectedErrors: []string{
				// Two errors: one from Machine conversion, one from MachineSet conversion
				"infrastructure.status.infrastructureName: Invalid value: \"\": infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty",
				"infrastructure.status.infrastructureName: Invalid value: \"\": infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty",
			},
			expectedWarnings: []string{},
		}),
	)

})

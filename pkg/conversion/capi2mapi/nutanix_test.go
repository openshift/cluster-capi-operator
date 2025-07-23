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
	"github.com/google/uuid"
	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	clusterv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	nutanixv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

var _ = Describe("capi2mapi Nutanix conversion", func() {
	name := "nutanix-test"
	deviceID1 := int64(12345)
	bootType := nutanixv1.NutanixBootTypeUEFI
	invalidBootType := nutanixv1.NutanixBootType("invalidBootType")
	var (
		nutanixCAPIMachineBase        = clusterv1resourcebuilder.Machine()
		nutanixCAPINutanixMachineBase = nutanixv1resourcebuilder.NutanixMachine().WithGPUs(
			[]nutanixv1.NutanixGPU{
				{
					Type:     nutanixv1.NutanixGPUIdentifierDeviceID,
					DeviceID: &deviceID1,
				},
				{
					Type: nutanixv1.NutanixGPUIdentifierName,
					Name: &name,
				},
			},
		).WithDataDisks(
			[]nutanixv1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("10Gi"),
					DeviceProperties: &nutanixv1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  nutanixv1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: nutanixv1.NutanixMachineDiskAdapterTypeSCSI,
						DeviceIndex: 1,
					},
					StorageConfig: &nutanixv1.NutanixMachineVMStorageConfig{
						DiskMode: nutanixv1.NutanixMachineDiskModeFlash,
						StorageContainer: &nutanixv1.NutanixResourceIdentifier{
							Type: nutanixv1.NutanixIdentifierUUID,
							UUID: ptr.To(uuid.NewString()),
						},
					},
					DataSource: &nutanixv1.NutanixResourceIdentifier{
						Type: nutanixv1.NutanixIdentifierUUID,
						UUID: ptr.To(uuid.NewString()),
					},
				},
			},
		).WithBootType(
			&bootType,
		).WithSubnets(
			[]nutanixv1.NutanixResourceIdentifier{
				{
					Type: nutanixv1.NutanixIdentifierUUID,
					UUID: ptr.To(uuid.NewString()),
				},
				{
					Type: nutanixv1.NutanixIdentifierName,
					Name: &name,
				},
			},
		).WithCluster(&nutanixv1.NutanixResourceIdentifier{
			Type: nutanixv1.NutanixIdentifierUUID,
			UUID: ptr.To(uuid.NewString()),
		})
		nutanixCAPINutanixClusterBase = nutanixv1resourcebuilder.NutanixCluster()
	)

	type nutanixCAPI2MAPIMachineConversionInput struct {
		machineBuilder        clusterv1resourcebuilder.MachineBuilder
		nutanixMachineBuilder nutanixv1resourcebuilder.NutanixMachineBuilder
		nutanixClusterBuilder nutanixv1resourcebuilder.NutanixClusterBuilder
		expectedErrors        []string
		expectedWarnings      []string
	}

	type nutanixCAPI2MAPIMachinesetConversionInput struct {
		machineSetBuilder             clusterv1resourcebuilder.MachineSetBuilder
		nutanixMachineTemplateBuilder nutanixv1resourcebuilder.NutanixMachineTemplateBuilder
		nutanixClusterBuilder         nutanixv1resourcebuilder.NutanixClusterBuilder
		expectedErrors                []string
		expectedWarnings              []string
	}

	var _ = DescribeTable("capi2mapi Nutanix convert CAPI Machine/InfraMachine/InfraCluster to a MAPI Machine",
		func(in nutanixCAPI2MAPIMachineConversionInput) {
			_, warns, err := FromMachineAndNutanixMachineAndNutanixCluster(
				in.machineBuilder.Build(),
				in.nutanixMachineBuilder.Build(),
				in.nutanixClusterBuilder.Build(),
			).ToMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting Nutanix CAPI resources to MAPI Machine")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting Nutanix CAPI resources to MAPI Machine")
		},

		// Base Case
		Entry("passes with a base configuration", nutanixCAPI2MAPIMachineConversionInput{
			nutanixClusterBuilder: nutanixCAPINutanixClusterBase,
			nutanixMachineBuilder: nutanixCAPINutanixMachineBase,
			machineBuilder:        nutanixCAPIMachineBase,
			expectedErrors:        []string{},
			expectedWarnings:      []string{},
		}),

		// bootType Error
		Entry("fails with invalid bootType", nutanixCAPI2MAPIMachineConversionInput{
			nutanixClusterBuilder: nutanixCAPINutanixClusterBase,
			nutanixMachineBuilder: nutanixCAPINutanixMachineBase.WithBootType(&invalidBootType),
			machineBuilder:        nutanixCAPIMachineBase,
			expectedErrors: []string{
				"bootType: Invalid value: \"invalidBootType\": invalid boot type",
			},
			expectedWarnings: []string{},
		}),

		// NutanixResourceIdentifier Error
		Entry("fails with invalid subnets", nutanixCAPI2MAPIMachineConversionInput{
			nutanixClusterBuilder: nutanixCAPINutanixClusterBase,
			nutanixMachineBuilder: nutanixCAPINutanixMachineBase.WithSubnets(
				[]nutanixv1.NutanixResourceIdentifier{
					{
						Type: nutanixv1.NutanixIdentifierUUID,
					},
					{
						Type: nutanixv1.NutanixIdentifierName,
					},
				},
			),
			machineBuilder: nutanixCAPIMachineBase,
			expectedErrors: []string{
				"uuid: Required value: UUID must be set for UUID type identifier",
				"name: Required value: Name must be set for Name type identifier",
			},
			expectedWarnings: []string{},
		}),
	)

	var _ = DescribeTable("capi2mapi Nutanix convert CAPI MachineSet/InfraMachineTemplate/InfraCluster to MAPI MachineSet",
		func(in nutanixCAPI2MAPIMachinesetConversionInput) {
			_, warns, err := FromMachineSetAndNutanixMachineTemplateAndNutanixCluster(
				in.machineSetBuilder.Build(),
				in.nutanixMachineTemplateBuilder.Build(),
				in.nutanixClusterBuilder.Build(),
			).ToMachineSet()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting Nutanix CAPI resources to MAPI MachineSet")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting Nutanix CAPI resources to MAPI MachineSet")
		},

		// Base Case.
		Entry("passes with a base configuration", nutanixCAPI2MAPIMachinesetConversionInput{
			nutanixClusterBuilder: nutanixCAPINutanixClusterBase,
			nutanixMachineTemplateBuilder: nutanixv1resourcebuilder.NutanixMachineTemplate().WithGPUs(
				[]nutanixv1.NutanixGPU{
					{
						Type:     nutanixv1.NutanixGPUIdentifierDeviceID,
						DeviceID: &deviceID1,
					},
					{
						Type: nutanixv1.NutanixGPUIdentifierName,
						Name: &name,
					},
				},
			).WithDataDisks(
				[]nutanixv1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("10Gi"),
						DeviceProperties: &nutanixv1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  nutanixv1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: nutanixv1.NutanixMachineDiskAdapterTypeSCSI,
							DeviceIndex: 1,
						},
						StorageConfig: &nutanixv1.NutanixMachineVMStorageConfig{
							DiskMode: nutanixv1.NutanixMachineDiskModeFlash,
							StorageContainer: &nutanixv1.NutanixResourceIdentifier{
								Type: nutanixv1.NutanixIdentifierUUID,
								UUID: ptr.To(uuid.NewString()),
							},
						},
						DataSource: &nutanixv1.NutanixResourceIdentifier{
							Type: nutanixv1.NutanixIdentifierUUID,
							UUID: ptr.To(uuid.NewString()),
						},
					},
				},
			).WithBootType(
				&bootType,
			).WithSubnets(
				[]nutanixv1.NutanixResourceIdentifier{
					{
						Type: nutanixv1.NutanixIdentifierUUID,
						UUID: ptr.To(uuid.NewString()),
					},
					{
						Type: nutanixv1.NutanixIdentifierName,
						Name: &name,
					},
				},
			).WithCluster(&nutanixv1.NutanixResourceIdentifier{
				Type: nutanixv1.NutanixIdentifierUUID,
				UUID: ptr.To(uuid.NewString()),
			}),
			machineSetBuilder: clusterv1resourcebuilder.MachineSet(),
			expectedErrors:    []string{},
			expectedWarnings:  []string{},
		}),
	)
})

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
package mapi2capi_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"

	fuzz "github.com/google/gofuzz"
	"github.com/google/uuid"
	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	conversiontest "github.com/openshift/cluster-capi-operator/pkg/conversion/test/fuzz"
)

const (
	nutanixProviderSpecKind = "NutanixProviderSpec"
)

var _ = Describe("Nutanix Fuzz (mapi2capi)", func() {
	infra := &configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	infraCluster := &nutanixv1.NutanixCluster{
		Spec: nutanixv1.NutanixClusterSpec{},
	}

	Context("NutanixMachine Conversion", func() {
		fromMachineAndNutanixMachineAndNutanixCluster := func(machine *clusterv1.Machine, infraMachine client.Object, infraCluster client.Object) capi2mapi.MachineAndInfrastructureMachine {
			nutanixMachine, ok := infraMachine.(*nutanixv1.NutanixMachine)
			Expect(ok).To(BeTrue(), "input infra machine should be of type %T, got %T", &nutanixv1.NutanixMachine{}, infraMachine)

			nutanixCluster, ok := infraCluster.(*nutanixv1.NutanixCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &nutanixv1.NutanixCluster{}, infraCluster)

			return capi2mapi.FromMachineAndNutanixMachineAndNutanixCluster(machine, nutanixMachine, nutanixCluster)
		}

		conversiontest.MAPI2CAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			mapi2capi.FromNutanixMachineAndInfra,
			fromMachineAndNutanixMachineAndNutanixCluster,
			conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
			conversiontest.MAPIMachineFuzzerFuncs(&mapiv1.NutanixMachineProviderConfig{}, nutanixProviderIDFuzzer),
			nutanixProviderSpecFuzzerFuncs,
		)
	})

		Context("NutanixMachineSet Conversion", func() {
			fromMachineSetAndNutanixMachineTemplateAndNutanixCluster := func(machineSet *clusterv1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) capi2mapi.MachineSetAndMachineTemplate {
				nutanixMachineTemplate, ok := infraMachineTemplate.(*nutanixv1.NutanixMachineTemplate)
				Expect(ok).To(BeTrue(), "input infra machine template should be of type %T, got %T", &nutanixv1.NutanixMachineTemplate{}, infraMachineTemplate)

				nutanixCluster, ok := infraCluster.(*nutanixv1.NutanixCluster)
				Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &nutanixv1.NutanixCluster{}, infraCluster)

				return capi2mapi.FromMachineSetAndNutanixMachineTemplateAndNutanixCluster(machineSet, nutanixMachineTemplate, nutanixCluster)
			}

			conversiontest.MAPI2CAPIMachineSetRoundTripFuzzTest(
				scheme,
				infra,
				infraCluster,
				mapi2capi.FromNutanixMachineSetAndInfra,
				fromMachineSetAndNutanixMachineTemplateAndNutanixCluster,
				conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
				conversiontest.MAPIMachineFuzzerFuncs(&mapiv1.NutanixMachineProviderConfig{}, nutanixProviderIDFuzzer),
				conversiontest.MAPIMachineSetFuzzerFuncs(),
				nutanixProviderSpecFuzzerFuncs,
				nutanixMachineFuzzerFuncs,
			)
		})
})

func nutanixProviderIDFuzzer(c fuzz.Continue) string {
	return "nutanix://" + uuid.NewString()
}

// FuzzNutanixMachineProviderConfig generates valid fuzzed data for NutanixMachineProviderConfig
// It populates required fields with reasonable example data,
// and also covers GPU, DataDisks, Subnets, BootType, Cluster and Image fields.
func nutanixProviderSpecFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(n *mapiv1.NutanixMachineProviderConfig, c fuzz.Continue) {
			// Set API version & Kind consistent with the CRD
			n.APIVersion = "machine.openshift.io/v1"
			n.Kind = "NutanixMachineProviderConfig"

			// Metadata labels (must be valid DNS-1123 keys/values)
			n.ObjectMeta.Labels = map[string]string{
				"cluster.x-k8s.io/cluster-name": "sample-cluster-name",
			}

			// Required numeric fields with valid minimums
			n.Project = mapiv1.NutanixResourceIdentifier{
				Type: mapiv1.NutanixIdentifierUUID,
				UUID: ptr.To(uuid.NewString()),
			}
			n.VCPUsPerSocket = int32(2)                   // minimum 1
			n.VCPUSockets = int32(2)                      // minimum 1
			n.MemorySize = resource.MustParse("10Gi")     // minimum 2Gi per spec
			n.SystemDiskSize = resource.MustParse("20Gi") // minimum 20Gi per spec

			// Required fields with valid values
			n.BootType = mapiv1.NutanixUEFIBoot // valid enum: "", Legacy, UEFI, SecureBoot are allowed

			// Provide valid Image identifier (Name type, with non-nil Name)
			imageName := "example-image"
			n.Image = mapiv1.NutanixResourceIdentifier{
				Type: mapiv1.NutanixIdentifierName,
				Name: &imageName,
			}

			// Provide valid Cluster identifier (UUID type with non-nil UUID)
			clusterUUID := "01234567-89ab-cdef-0123-456789abcdef"
			n.Cluster = mapiv1.NutanixResourceIdentifier{
				Type: mapiv1.NutanixIdentifierUUID,
				UUID: &clusterUUID,
			}

			// Provide one valid subnet (UUID type)
			subnetUUID := "abcdefab-cdef-0123-4567-89abcdef0123"
			n.Subnets = []mapiv1.NutanixResourceIdentifier{{
				Type: mapiv1.NutanixIdentifierUUID,
				UUID: &subnetUUID,
			}}

			// Provide one valid data disk (min config)
			n.DataDisks = []mapiv1.NutanixVMDisk{
				{
					DiskSize: resource.MustParse("100Gi"),
					DeviceProperties: &mapiv1.NutanixVMDiskDeviceProperties{
						DeviceType:  mapiv1.NutanixDiskDeviceTypeDisk,
						AdapterType: mapiv1.NutanixDiskAdapterTypeSCSI,
						DeviceIndex: 0,
					},
					StorageConfig: &mapiv1.NutanixVMStorageConfig{
						DiskMode: mapiv1.NutanixDiskModeStandard,
						StorageContainer: &mapiv1.NutanixStorageResourceIdentifier{
							Type: mapiv1.NutanixIdentifierUUID,
							UUID: &subnetUUID,
						},
					},
				},
			}

			// Provide one GPU with DeviceID type and non-nil DeviceID
			deviceID := int32(1)
			n.GPUs = []mapiv1.NutanixGPU{
				{
					Type:     mapiv1.NutanixGPUIdentifierDeviceID,
					DeviceID: &deviceID,
				},
			}

			// UserDataSecret is optional; set to nil or default
			n.UserDataSecret = nil

			// CredentialsSecret is required; provide a non-empty name
			n.CredentialsSecret = &corev1.LocalObjectReference{
				Name: "nutanix-credentials-secret",
			}

			// Project is optional; include a valid identifier or leave zero
			projectUUID := uuid.NewString()
			n.Project = mapiv1.NutanixResourceIdentifier{
				Type: mapiv1.NutanixIdentifierUUID,
				UUID: &projectUUID,
			}

			// Categories is optional; provide empty or a valid list
			n.Categories = nil

			// FailureDomain is optional; fill minimally or leave nil
			domainName := "failure-domain-1"
			n.FailureDomain = &mapiv1.NutanixFailureDomainReference{
				Name: domainName,
			}
		},
	}
}

func nutanixMachineFuzzerFuncs(_ runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(m *nutanixv1.NutanixMachine, _ fuzz.Continue) {
			m.Spec = nutanixv1.NutanixMachineSpec{
				Cluster: nutanixv1.NutanixResourceIdentifier{
					Type: nutanixv1.NutanixIdentifierUUID,
					UUID: ptr.To(uuid.NewString()),
				},
				Image: &nutanixv1.NutanixResourceIdentifier{
					Type: nutanixv1.NutanixIdentifierUUID,
					UUID: ptr.To(uuid.NewString()),
				},
				BootType:       nutanixv1.NutanixBootTypeUEFI,
				VCPUsPerSocket: 2,
				VCPUSockets:    2,
				MemorySize:     resource.MustParse("8Gi"),
				SystemDiskSize: resource.MustParse("50Gi"),
				Subnets: []nutanixv1.NutanixResourceIdentifier{
					{
						Type: nutanixv1.NutanixIdentifierUUID,
						UUID: ptr.To(uuid.NewString()),
					},
				},
				Project: &nutanixv1.NutanixResourceIdentifier{
					Type: nutanixv1.NutanixIdentifierUUID,
					UUID: ptr.To(uuid.NewString()),
				},
				DataDisks: []nutanixv1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("10Gi"),
						DeviceProperties: &nutanixv1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  nutanixv1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: nutanixv1.NutanixMachineDiskAdapterTypeSCSI,
							DeviceIndex: 0,
						},
						StorageConfig: &nutanixv1.NutanixMachineVMStorageConfig{
							DiskMode: nutanixv1.NutanixMachineDiskModeStandard,
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
				GPUs: []nutanixv1.NutanixGPU{
					{
						Type:     nutanixv1.NutanixGPUIdentifierDeviceID,
						DeviceID: ptr.To(int64(1)),
					},
				},
			}
		},
	}
}

package capi2mapi_test

import (
	fuzz "github.com/google/gofuzz"
	"github.com/google/uuid"
	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	conversiontest "github.com/openshift/cluster-capi-operator/pkg/conversion/test/fuzz"
	"k8s.io/apimachinery/pkg/api/resource"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	nutanixMachineKind  = "NutanixMachine"
	nutanixTemplateKind = "NutanixMachineTemplate"
)

var _ = Describe("Nutanix Fuzz (capi2mapi)", func() {
	infra := &configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	infraCluster := &nutanixv1.NutanixCluster{
		Spec: nutanixv1.NutanixClusterSpec{},
	}

	Context("NutanixMachine Conversion (valid only)", func() {
		fromMachineAndNutanixMachineAndNutanixCluster := func(machine *clusterv1.Machine, infraMachine client.Object, infraCluster client.Object) capi2mapi.MachineAndInfrastructureMachine {
			nutanixMachine, ok := infraMachine.(*nutanixv1.NutanixMachine)
			Expect(ok).To(BeTrue(), "input infra machine should be of type %T, got %T", &nutanixv1.NutanixMachine{}, infraMachine)
			nutanixCluster, ok := infraCluster.(*nutanixv1.NutanixCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &nutanixv1.NutanixCluster{}, infraCluster)
			return capi2mapi.FromMachineAndNutanixMachineAndNutanixCluster(machine, nutanixMachine, nutanixCluster)
		}
		conversiontest.CAPI2MAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&nutanixv1.NutanixMachine{},
			mapi2capi.FromNutanixMachineAndInfra,
			fromMachineAndNutanixMachineAndNutanixCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(nutanixProviderIDFuzzer, nutanixMachineKind, nutanixv1.GroupVersion.Version, infra.Status.InfrastructureName),
			nutanixMachineFuzzerFuncs,
		)

		Context("NutanixMachineSet Conversion (valid only)", func() {
			fromMachineSetAndNutanixMachineTemplateAndNutanixCluster := func(machineSet *clusterv1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) capi2mapi.MachineSetAndMachineTemplate {
				nutanixMachineTemplate, ok := infraMachineTemplate.(*nutanixv1.NutanixMachineTemplate)
				Expect(ok).To(BeTrue(), "input infra machine template should be of type %T, got %T", &nutanixv1.NutanixMachineTemplate{}, infraMachineTemplate)
				nutanixCluster, ok := infraCluster.(*nutanixv1.NutanixCluster)
				Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &nutanixv1.NutanixCluster{}, infraCluster)
				return capi2mapi.FromMachineSetAndNutanixMachineTemplateAndNutanixCluster(machineSet, nutanixMachineTemplate, nutanixCluster)
			}

			conversiontest.CAPI2MAPIMachineSetRoundTripFuzzTest(
				scheme,
				infra,
				infraCluster,
				&nutanixv1.NutanixMachineTemplate{},
				mapi2capi.FromNutanixMachineSetAndInfra,
				fromMachineSetAndNutanixMachineTemplateAndNutanixCluster,
				conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
				conversiontest.CAPIMachineFuzzerFuncs(nutanixProviderIDFuzzer, nutanixTemplateKind, nutanixv1.SchemeBuilder.GroupVersion.Version, infra.Status.InfrastructureName),
				conversiontest.CAPIMachineSetFuzzerFuncs(nutanixTemplateKind, nutanixv1.SchemeBuilder.GroupVersion.Version, infra.Status.InfrastructureName),
				nutanixMachineFuzzerFuncs,
				nutanixMachineTemplateFuzzerFuncs,
			)
		})
	})
})

func nutanixProviderIDFuzzer(c fuzz.Continue) string {
	return "nutanix://" + uuid.NewString()
}

func nutanixMachineFuzzerFuncs(_ runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		// Only populate fields with valid values
		func(m *nutanixv1.NutanixMachine, _ fuzz.Continue) {
			m.TypeMeta.APIVersion = nutanixv1.SchemeBuilder.GroupVersion.Version
			m.TypeMeta.Kind = nutanixMachineKind
			m.Spec = nutanixv1.NutanixMachineSpec{
				// Only use NutanixIdentifierUUID for Cluster
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
						Name:     nil,
					},
				},
			}
		},
		func(spec *nutanixv1.NutanixMachineSpec, _ fuzz.Continue) {
			// Only valid values, don't change!
		},
	}
}

func nutanixMachineTemplateFuzzerFuncs(_ runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(m *nutanixv1.NutanixMachineTemplate, _ fuzz.Continue) {
			m.TypeMeta.APIVersion = nutanixv1.GroupVersion.Version
			m.TypeMeta.Kind = nutanixTemplateKind
			m.Spec.Template.Spec = nutanixv1.NutanixMachineSpec{
				Cluster: nutanixv1.NutanixResourceIdentifier{
					Type: nutanixv1.NutanixIdentifierUUID,
					UUID: ptr.To(uuid.NewString()),
				},
				Image: &nutanixv1.NutanixResourceIdentifier{
					Type: nutanixv1.NutanixIdentifierUUID,
					UUID: ptr.To(uuid.NewString()),
				},
				BootType:       nutanixv1.NutanixBootTypeUEFI,
				VCPUsPerSocket: 4,
				VCPUSockets:    1,
				MemorySize:     resource.MustParse("8Gi"),
				SystemDiskSize: resource.MustParse("50Gi"),
				DataDisks: []nutanixv1.NutanixMachineVMDisk{
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
				GPUs: []nutanixv1.NutanixGPU{
					{
						Type:     nutanixv1.NutanixGPUIdentifierName,
						Name:     ptr.To("valid-gpu"),
						DeviceID: nil,
					},
				},
			}
		},
		func(bootType *nutanixv1.NutanixBootType, _ fuzz.Continue) {
			*bootType = nutanixv1.NutanixBootTypeUEFI
		},
		func(image *nutanixv1.NutanixResourceIdentifier, _ fuzz.Continue) {
			image.Type = nutanixv1.NutanixIdentifierUUID
			image.UUID = ptr.To(uuid.NewString())
			image.Name = nil
		},
		// GPUS, subnets, disks also can be added as their own fuzzer logic if needed.
	}
}

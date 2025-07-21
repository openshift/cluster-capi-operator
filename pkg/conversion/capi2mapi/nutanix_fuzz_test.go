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

	Context("NutanixMachine Conversion", func() {
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
			conversiontest.CAPIMachineFuzzerFuncs(nutanixProviderIDFuzzer, nutanixMachineKind, nutanixv1.SchemeBuilder.GroupVersion.Version, infra.Status.InfrastructureName),
			nutanixMachineFuzzerFuncs,
		)
		Context("NutanixMachineSet Conversion", func() {
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

func nutanixMachineTemplateFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(m *nutanixv1.NutanixMachineTemplate, c fuzz.Continue) {
			c.FuzzNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = nutanixv1.GroupVersion.Version
			m.TypeMeta.Kind = nutanixTemplateKind
		},
		func(bootType *nutanixv1.NutanixBootType, c fuzz.Continue) {
			// Fuzz the boot type, but ensure it is a valid value.
			val := nutanixv1.NutanixBootTypeUEFI
			*bootType = val
		},
		func(image *nutanixv1.NutanixResourceIdentifier, c fuzz.Continue) {
			image.Name = ptr.To("test-image")
			image.Type = nutanixv1.NutanixIdentifierUUID
			image.UUID = ptr.To(uuid.NewString())
		},
		func(dataDisks *[]nutanixv1.NutanixMachineVMDisk, c fuzz.Continue) {
			// If you want the slice to be empty (to test empty cases):
			// *dataDisks = []nutanixv1.NutanixMachineVMDisk{}
			// If you want to fuzz with some sample disk(s), do:
			*dataDisks = []nutanixv1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("10Gi"),
					DeviceProperties: &nutanixv1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  nutanixv1.NutanixMachineDiskDeviceTypeCDRom,
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
				{
					DiskSize: resource.MustParse("10Gi"),
					DeviceProperties: &nutanixv1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  nutanixv1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: nutanixv1.NutanixMachineDiskAdapterTypeSPAPR,
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
				{
					DiskSize: resource.MustParse("10Gi"),
					DeviceProperties: &nutanixv1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  nutanixv1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: nutanixv1.NutanixMachineDiskAdapterTypeSCSI,
						DeviceIndex: 2,
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
			}
		},
		func(subnets *[]nutanixv1.NutanixResourceIdentifier, c fuzz.Continue) {
			*subnets = []nutanixv1.NutanixResourceIdentifier{
				{
					Type: nutanixv1.NutanixIdentifierUUID,
					UUID: ptr.To(uuid.NewString()),
				},
				{
					Type: nutanixv1.NutanixIdentifierName,
					Name: ptr.To("test-subnet"),
				},
			}
		},
		func(gpus *[]nutanixv1.NutanixGPU, c fuzz.Continue) {
			// Fuzz two example GPUs: one with device ID, one with name, one with both nil, etc.
			gpuName := "my-gpu"
			deviceID := c.Int63() // returns int64
			*gpus = []nutanixv1.NutanixGPU{
				{
					Type:     nutanixv1.NutanixGPUIdentifierDeviceID,
					DeviceID: &deviceID,
					Name:     nil,
				},
				{
					Type:     nutanixv1.NutanixGPUIdentifierName,
					DeviceID: nil,
					Name:     &gpuName,
				},
			}
		},
	}
}

func nutanixMachineFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(m *nutanixv1.NutanixMachine, c fuzz.Continue) {
			c.FuzzNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = nutanixv1.SchemeBuilder.GroupVersion.Version
			m.TypeMeta.Kind = nutanixMachineKind
		},

		func(spec *nutanixv1.NutanixMachineSpec, c fuzz.Continue) {
			c.FuzzNoCustom(spec)
		},
	}
}

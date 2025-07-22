package mapi2capi

import (
	"fmt"

	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	nutanixMachineKind         = "NutanixMachine"
	nutanixMachineTemplateKind = "NutanixMachineTemplate"
)

type nutanixMachineAndInfra struct {
	machine        *mapiv1beta1.Machine
	infrastructure *configv1.Infrastructure
}

type nutanixMachineSetAndInfra struct {
	machineSet     *mapiv1beta1.MachineSet
	infrastructure *configv1.Infrastructure
	*nutanixMachineAndInfra
}

// FromNutanixMachineAndInfra wraps a Machine API Machine for Nutanix and the OCP Infrastructure object into a mapi2capi NutanixProviderSpec.
func FromNutanixMachineAndInfra(m *mapiv1beta1.Machine, i *configv1.Infrastructure) Machine {
	return &nutanixMachineAndInfra{machine: m, infrastructure: i}
}

// FromNutanixMachineSetAndInfra wraps a Machine API MachineSet for OpenStack and the OCP Infrastructure object into a mapi2capi OpenstackProviderSpec.
func FromNutanixMachineSetAndInfra(m *mapiv1beta1.MachineSet, i *configv1.Infrastructure) MachineSet {
	return &nutanixMachineSetAndInfra{
		machineSet:     m,
		infrastructure: i,
		nutanixMachineAndInfra: &nutanixMachineAndInfra{
			machine: &mapiv1beta1.Machine{
				Spec: m.Spec.Template.Spec,
			},
			infrastructure: i,
		},
	}
}

func nutanixMachineToNutanixMachineTemplate(nutanixMachine *nutanixv1.NutanixMachine, name string, namespace string) *nutanixv1.NutanixMachineTemplate {
	return &nutanixv1.NutanixMachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: nutanixv1.GroupVersion.Version,
			Kind:       nutanixMachineTemplateKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: nutanixv1.NutanixMachineTemplateSpec{
			Template: nutanixv1.NutanixMachineTemplateResource{
				Spec: nutanixMachine.Spec,
			},
		},
	}
}

func (m *nutanixMachineSetAndInfra) ToMachineSetAndMachineTemplate() (*clusterv1.MachineSet, client.Object, []string, error) {
	var (
		errors   []error
		warnings []string
	)
	capiMachine, capxMachineObj, warns, err := m.toMachineAndInfrastructureMachine()
	if err != nil {
		errors = append(errors, err.ToAggregate().Errors()...)
	}
	warnings = append(warnings, warns...)

	capxMachine, ok := capxMachineObj.(*nutanixv1.NutanixMachine)
	if !ok {
		panic(fmt.Errorf("%w: %T", errUnexpectedObjectTypeForMachine, capxMachineObj))
	}

	capxMachineTemplate := nutanixMachineToNutanixMachineTemplate(capxMachine, m.machineSet.Name, capiNamespace)

	capiMachineSet, machineSetErrs := fromMAPIMachineSetToCAPIMachineSet(m.machineSet)
	if machineSetErrs != nil {
		errors = append(errors, machineSetErrs.Errors()...)
	}
	capiMachineSet.Spec.Template.Spec = capiMachine.Spec

	capiMachineSet.Spec.Template.ObjectMeta.Labels = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Labels, capiMachine.Labels)
	capiMachineSet.Spec.Template.ObjectMeta.Annotations = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Annotations, capiMachine.Annotations)

	// capiMachineSet.Spec.Template.Spec.InfrastructureRef.Kind = nutanixMachineTemplateKind
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = capxMachineTemplate.Name

	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errors = append(errors, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachineSet.Spec.Template.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachineSet.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachineSet.Spec.Template.Spec.InfrastructureRef.Kind = nutanixMachineTemplateKind
		capiMachineSet.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	if len(errors) > 0 {
		return nil, nil, warnings, utilerrors.NewAggregate(errors)
	}
	return capiMachineSet, capxMachineTemplate, warnings, nil
}

// ToMachineAndInfrastructureMachine is used to generate a CAPI Machine and the corresponding InfrastructureMachine
// from the stored MAPI Machine and Infrastructure objects.
func (m *nutanixMachineAndInfra) ToMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, error) {
	capiMachine, capxMachine, warnings, errors := m.toMachineAndInfrastructureMachine()

	if len(errors) > 0 {
		return nil, nil, warnings, errors.ToAggregate()
	}

	return capiMachine, capxMachine, warnings, nil
}

func (m *nutanixMachineAndInfra) nutanixProviderSpecFromRawExtension(rawExtension *runtime.RawExtension) (*mapiv1.NutanixMachineProviderConfig, error) {
	if rawExtension == nil {
		return &mapiv1.NutanixMachineProviderConfig{}, nil
	}

	spec := &mapiv1.NutanixMachineProviderConfig{}
	if err := yaml.Unmarshal(rawExtension.Raw, &spec); err != nil {
		return &mapiv1.NutanixMachineProviderConfig{}, fmt.Errorf("error unmarshalling providerSpec: %w", err)
	}

	return spec, nil
}

func (m *nutanixMachineAndInfra) toNutanixMachine(providerConfig *mapiv1.NutanixMachineProviderConfig) (*nutanixv1.NutanixMachine, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	var image nutanixv1.NutanixResourceIdentifier
	if providerConfig.Image.Type != "" {
		img, errs := convertNutanixResourceIdentifierToCAPX(&providerConfig.Image)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}
		image = *img
	}

	// Convert Subnets from []mapiv1.NutanixResourceIdentifier to []nutanixv1.NutanixResourceIdentifier
	var subnets []nutanixv1.NutanixResourceIdentifier
	for _, s := range providerConfig.Subnets {
		id, errs := convertNutanixResourceIdentifierToCAPX(&s)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}
		subnets = append(subnets, *id)
	}

	// Convert DataDisks from []mapiv1.NutanixVMDisk to []nutanixv1.NutanixMachineVMDisk
	var dataDisks []nutanixv1.NutanixMachineVMDisk
	for _, d := range providerConfig.DataDisks {
		disk, errs := convertNutanixVMDiskToCAPX(&d)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}
		dataDisks = append(dataDisks, *disk)
	}

	// Convert GPUs from []mapiv1.NutanixGPU to []nutanixv1.NutanixGPU
	gpus, errs := convertNutanixGPUToCAPX(&providerConfig.GPUs)
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	cluster, errs := convertNutanixResourceIdentifierToCAPX(&providerConfig.Cluster)
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	bootType, errs := convertNutanixBootTypeToCAPX(providerConfig.BootType)
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	spec := &nutanixv1.NutanixMachineSpec{
		VCPUsPerSocket: providerConfig.VCPUsPerSocket,
		VCPUSockets:    providerConfig.VCPUSockets,
		MemorySize:     providerConfig.MemorySize,
		Image:          &image,
		Cluster:        *cluster,
		BootType:       bootType,
		SystemDiskSize: providerConfig.SystemDiskSize,
		DataDisks:      dataDisks,
		GPUs:           *gpus,
		Subnets:        subnets,
	}

	return &nutanixv1.NutanixMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: nutanixv1.SchemeBuilder.GroupVersion.Version,
			Kind:       nutanixMachineKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.machine.Name,
			Namespace: capiNamespace,
		},
		Spec: *spec,
	}, warnings, errors

}

func (m *nutanixMachineAndInfra) toMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	nutanixProviderConfig, err := m.nutanixProviderSpecFromRawExtension(m.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, nil, nil, field.ErrorList{field.Invalid(field.NewPath("spec", "providerSpec", "value"), m.machine.Spec.ProviderSpec.Value, err.Error())}
	}
	capxMachine, warns, errs := m.toNutanixMachine(nutanixProviderConfig)
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	warnings = append(warnings, warns...)

	capiMachine, errs := fromMAPIMachineToCAPIMachine(m.machine, nutanixv1.SchemeBuilder.GroupVersion.Version, nutanixMachineKind)
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	if nutanixProviderConfig.FailureDomain != nil {
		capiMachine.Spec.FailureDomain = ptr.To(nutanixProviderConfig.FailureDomain.Name)
	}

	if nutanixProviderConfig.UserDataSecret != nil {
		capiMachine.Spec.Bootstrap = clusterv1.Bootstrap{
			DataSecretName: &nutanixProviderConfig.UserDataSecret.Name,
		}
	}

	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errors = append(errors, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachine.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachine.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	capiMachineAnnotations := capiMachine.GetAnnotations()
	if len(capiMachineAnnotations) > 0 {
		capxMachine.SetAnnotations(capiMachineAnnotations)
	}

	capiMachineLabels := capiMachine.GetLabels()
	if len(capiMachineLabels) > 0 {
		capxMachine.SetLabels(capiMachineLabels)
	}

	return capiMachine, capxMachine, warnings, errors
}

func convertNutanixVMDiskToCAPX(disk *mapiv1.NutanixVMDisk) (*nutanixv1.NutanixMachineVMDisk, field.ErrorList) {
	errors := field.ErrorList{}
	if disk == nil {
		return &nutanixv1.NutanixMachineVMDisk{}, errors
	}

	mapiDisk := &nutanixv1.NutanixMachineVMDisk{
		DiskSize: disk.DiskSize,
	}

	switch disk.DataSource {
	case nil:
	default:
		ds, errs := convertNutanixResourceIdentifierToCAPX(disk.DataSource)
		if len(errs) > 0 {
			errors = append(errors, field.Invalid(field.NewPath("dataSource"), disk.DataSource, "DataSource failed to convert"))
		}
		mapiDisk.DataSource = ds
	}

	switch dp := disk.DeviceProperties; {
	case dp == nil:
	default:
		ddp := &nutanixv1.NutanixMachineVMDiskDeviceProperties{}
		switch dp.DeviceType {
		case mapiv1.NutanixDiskDeviceTypeCDROM:
			ddp.DeviceType = nutanixv1.NutanixMachineDiskDeviceTypeCDRom
		case mapiv1.NutanixDiskDeviceTypeDisk:
			ddp.DeviceType = nutanixv1.NutanixMachineDiskDeviceTypeDisk
		default:
			errors = append(errors, field.Invalid(field.NewPath("DeviceType"), dp.DeviceType, "DeviceType should be CDRom or Disk"))
		}

		switch dp.AdapterType {
		case mapiv1.NutanixDiskAdapterTypeIDE:
			ddp.AdapterType = nutanixv1.NutanixMachineDiskAdapterTypeIDE
		case mapiv1.NutanixDiskAdapterTypePCI:
			ddp.AdapterType = nutanixv1.NutanixMachineDiskAdapterTypePCI
		case mapiv1.NutanixDiskAdapterTypeSATA:
			ddp.AdapterType = nutanixv1.NutanixMachineDiskAdapterTypeSATA
		case mapiv1.NutanixDiskAdapterTypeSCSI:
			ddp.AdapterType = nutanixv1.NutanixMachineDiskAdapterTypeSCSI
		case mapiv1.NutanixDiskAdapterTypeSPAPR:
			ddp.AdapterType = nutanixv1.NutanixMachineDiskAdapterTypeSPAPR
		default:
			errors = append(errors, field.Invalid(field.NewPath("AdapterType"), dp.AdapterType, "AdapterType can be SCSI, IDE, PCI, SATA or SPAPR"))
		}

		switch {
		case dp.DeviceIndex != 0:
			ddp.DeviceIndex = dp.DeviceIndex
		}
		// Remove if all zero/nil
		if ddp.DeviceType != "" || ddp.AdapterType != "" || ddp.DeviceIndex != 0 {
			mapiDisk.DeviceProperties = ddp
		}
	}

	switch sc := disk.StorageConfig; {
	case sc == nil:
	default:
		storage := &nutanixv1.NutanixMachineVMStorageConfig{}

		switch sc.DiskMode {
		case mapiv1.NutanixDiskModeFlash:
			storage.DiskMode = nutanixv1.NutanixMachineDiskModeFlash
		case mapiv1.NutanixDiskModeStandard:
			storage.DiskMode = nutanixv1.NutanixMachineDiskModeStandard
		default:
			errors = append(errors, field.Invalid(field.NewPath("DiskMode"), sc.DiskMode, "DiskMode can be Standard and Flash"))
		}

		switch {
		case sc.StorageContainer != nil:
			storageContainer, errs := convertNutanixResourceIdentifierToStorageCAPX(sc.StorageContainer)
			if len(errs) > 0 {
				errors = append(errors, errs...)
			}
			storage.StorageContainer = storageContainer
		}
		// Remove if all fields are zero/nil
		if storage.DiskMode != "" || (storage.StorageContainer != nil && storage.StorageContainer.Type != "" && storage.StorageContainer.UUID != nil) {
			mapiDisk.StorageConfig = storage
		}
	}

	return mapiDisk, errors
}

func convertNutanixResourceIdentifierToStorageCAPX(id *mapiv1.NutanixStorageResourceIdentifier) (*nutanixv1.NutanixResourceIdentifier, field.ErrorList) {
	errors := field.ErrorList{}
	if id == nil {
		return &nutanixv1.NutanixResourceIdentifier{}, errors
	}
	obj := &nutanixv1.NutanixResourceIdentifier{}
	switch id.Type {
	case mapiv1.NutanixIdentifierName:
		errors = append(errors, field.Invalid(field.NewPath("type"), id.Type, "invalid identifier type"))
	case mapiv1.NutanixIdentifierUUID:
		obj.Type = nutanixv1.NutanixIdentifierUUID
		if id.UUID == nil {
			errors = append(errors, field.Required(field.NewPath("uuid"), "UUID must be set for UUID type identifier"))
		}
	default:
		errors = append(errors, field.Invalid(field.NewPath("type"), id.Type, "invalid identifier type"))
	}
	return obj, errors
}

func convertNutanixResourceIdentifierToCAPX(identifier *mapiv1.NutanixResourceIdentifier) (*nutanixv1.NutanixResourceIdentifier, field.ErrorList) {
	errors := field.ErrorList{}
	if identifier == nil {
		return &nutanixv1.NutanixResourceIdentifier{}, errors
	}

	obj := nutanixv1.NutanixResourceIdentifier{}
	switch identifier.Type {
	case mapiv1.NutanixIdentifierName:
		obj.Type = nutanixv1.NutanixIdentifierName
		if identifier.Name == nil {
			errors = append(errors, field.Required(field.NewPath("name"), "Name must be set for Name type identifier"))
		}
		obj.Name = identifier.Name
	case mapiv1.NutanixIdentifierUUID:
		obj.Type = nutanixv1.NutanixIdentifierUUID
		if identifier.UUID == nil {
			errors = append(errors, field.Required(field.NewPath("uuid"), "UUID must be set for UUID type identifier"))
		}
		obj.UUID = identifier.UUID
	default:
		errors = append(errors, field.Invalid(field.NewPath("type"), identifier.Type, "invalid identifier type"))
	}
	return &obj, errors
}

func convertNutanixBootTypeToCAPX(bootType mapiv1.NutanixBootType) (nutanixv1.NutanixBootType, field.ErrorList) {
	var capxBootType nutanixv1.NutanixBootType
	errors := field.ErrorList{}
	switch bootType {
	case mapiv1.NutanixUEFIBoot:
		capxBootType = nutanixv1.NutanixBootTypeUEFI
	case mapiv1.NutanixLegacyBoot:
		capxBootType = nutanixv1.NutanixBootTypeLegacy
	default:
		errors = append(errors, field.Invalid(field.NewPath("bootType"), bootType, "invalid boot type"))
	}
	return capxBootType, errors
}

func convertNutanixGPUToCAPX(gpus *[]mapiv1.NutanixGPU) (*[]nutanixv1.NutanixGPU, field.ErrorList) {
	mapiGPUs := make([]nutanixv1.NutanixGPU, 0, len(*gpus))
	errors := field.ErrorList{}
	for _, g := range *gpus {
		obj := nutanixv1.NutanixGPU{}
		switch g.Type {
		case mapiv1.NutanixGPUIdentifierDeviceID:
			obj.Type = nutanixv1.NutanixGPUIdentifierDeviceID
			if g.DeviceID == nil {
				errors = append(errors, field.Required(field.NewPath("gpus").Index(len(mapiGPUs)), "DeviceID must be set for DeviceID type GPU"))
				continue
			}
			obj.DeviceID = ptr.To(int64(*g.DeviceID))

		case mapiv1.NutanixGPUIdentifierName:
			obj.Type = nutanixv1.NutanixGPUIdentifierName
			if g.Name == nil {
				errors = append(errors, field.Required(field.NewPath("gpus").Index(len(mapiGPUs)), "Name must be set for Name type GPU"))
				continue
			}
			obj.Name = ptr.To(*g.Name)

		default:
			errors = append(errors, field.Invalid(field.NewPath("gpus").Index(len(mapiGPUs)), g.Type, "invalid GPU identifier type"))
			continue
		}
		mapiGPUs = append(mapiGPUs, obj)
	}
	return &mapiGPUs, errors
}

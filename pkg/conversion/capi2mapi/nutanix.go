/*
Copyright 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0.html

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package capi2mapi

import (
	"encoding/json"
	"errors"
	"fmt"

	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mapiv1 "github.com/openshift/api/machine/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var (
	errCAPIMachineNutanixMachineNutanixClusterCannotBeNil            = errors.New("provided Machine, NutanixMachine and NutanixCluster can not be nil")
	errCAPIMachineSetNutanixMachineTemplateNutanixClusterCannotBeNil = errors.New("provided MachineSet, NutanixMachineTemplate and NutanixCluster can not be nil")
)

// machineAndNutanixMachineAndNutanixCluster stores the details of a Cluster API Machine and OpenStackMachine and OpenStackCluster.
type machineAndNutanixMachineAndNutanixCluster struct {
	machine        *clusterv1.Machine
	nutanixMachine *nutanixv1.NutanixMachine
	nutanixCluster *nutanixv1.NutanixCluster
}

// machineSetAndNutanixMachineTemplateAndNutanixCluster stores the details of a Cluster API MachineSet and NutanixMachineTemplate and NutanixCluster.
type machineSetAndNutanixMachineTemplateAndNutanixCluster struct {
	machineSet     *clusterv1.MachineSet
	template       *nutanixv1.NutanixMachineTemplate
	nutanixCluster *nutanixv1.NutanixCluster
	*machineAndNutanixMachineAndNutanixCluster
}

// FromMachineAndNutanixMachineAndNutanixCluster wraps a CAPI Machine and CAPO NutanixMachine and CAPO NutanixCluster into a capi2mapi MachineAndInfrastructureMachine.
func FromMachineAndNutanixMachineAndNutanixCluster(m *clusterv1.Machine, am *nutanixv1.NutanixMachine, ac *nutanixv1.NutanixCluster) MachineAndInfrastructureMachine {
	return &machineAndNutanixMachineAndNutanixCluster{machine: m, nutanixMachine: am, nutanixCluster: ac}
}

// FromMachineSetAndNutanixMachineTemplateAndNutanixCluster wraps a CAPI MachineSet and CAPX NutanixMachineTemplate and CAPX NutanixCluster into a capi2mapi MachineSetAndNutanixMachineTemplateAndNutanixCluster.
func FromMachineSetAndNutanixMachineTemplateAndNutanixCluster(ms *clusterv1.MachineSet, mts *nutanixv1.NutanixMachineTemplate, ac *nutanixv1.NutanixCluster) MachineSetAndMachineTemplate {
	return &machineSetAndNutanixMachineTemplateAndNutanixCluster{
		machineSet:     ms,
		template:       mts,
		nutanixCluster: ac,
		machineAndNutanixMachineAndNutanixCluster: &machineAndNutanixMachineAndNutanixCluster{
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      ms.Spec.Template.ObjectMeta.Labels,
					Annotations: ms.Spec.Template.ObjectMeta.Annotations,
				},
				Spec: ms.Spec.Template.Spec,
			},
			nutanixMachine: &nutanixv1.NutanixMachine{
				Spec: mts.Spec.Template.Spec,
			},
			nutanixCluster: ac,
		},
	}
}

// toProviderSpec converts a capi2mapi MachineAndNutanixMachineTemplateAndNutanixCluster into a MAPI NutanixMachineProviderConfig.
//
//nolint:funlen
func (m machineAndNutanixMachineAndNutanixCluster) toProviderSpec() (*mapiv1.NutanixMachineProviderConfig, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	var userData *corev1.LocalObjectReference
	if m.machine.Spec.Bootstrap.DataSecretName != nil {
		userData = &corev1.LocalObjectReference{
			Name: *m.machine.Spec.Bootstrap.DataSecretName,
		}
	}

	var image mapiv1.NutanixResourceIdentifier
	if m.nutanixMachine.Spec.Image != nil {
		img, errs := convertNutanixResourceIdentifierToMapi(m.nutanixMachine.Spec.Image)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}
		image = *img
	}

	var project mapiv1.NutanixResourceIdentifier
	if m.nutanixMachine.Spec.Project != nil {
		proj, errs := convertNutanixResourceIdentifierToMapi(m.nutanixMachine.Spec.Project)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}
		project = *proj
	}

	subnets := make([]mapiv1.NutanixResourceIdentifier, 0, len(m.nutanixMachine.Spec.Subnets))
	for _, s := range m.nutanixMachine.Spec.Subnets {
		id, errs := convertNutanixResourceIdentifierToMapi(&s)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}
		subnets = append(subnets, *id)
	}

	dataDisks := make([]mapiv1.NutanixVMDisk, 0, len(m.nutanixMachine.Spec.DataDisks))
	for _, d := range m.nutanixMachine.Spec.DataDisks {
		disk, errs := convertNutanixVMDiskToMapi(&d)
		if len(errs) > 0 {
			errors = append(errors, errs...)
		}
		dataDisks = append(dataDisks, *disk)
	}

	gpus, errs := convertNutanixGPUToMapi(&m.nutanixMachine.Spec.GPUs)
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	bootType, errs := convertNutanixBootTypeToMapi(m.nutanixMachine.Spec.BootType)
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	cluster, errs := convertNutanixResourceIdentifierToMapi(&m.nutanixMachine.Spec.Cluster)
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	var failureDomainRef *mapiv1.NutanixFailureDomainReference
	if m.machine.Spec.FailureDomain != nil {
		failureDomainRef = &mapiv1.NutanixFailureDomainReference{
			Name: *m.machine.Spec.FailureDomain,
		}
	}

	mapiProviderConfig := mapiv1.NutanixMachineProviderConfig{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NutanixMachineProviderConfig",
			APIVersion: "machine.openshift.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels:      m.machine.ObjectMeta.Labels,
			Annotations: m.machine.ObjectMeta.Annotations,
		},
		VCPUsPerSocket: m.nutanixMachine.Spec.VCPUsPerSocket,
		VCPUSockets:    m.nutanixMachine.Spec.VCPUSockets,
		MemorySize:     m.nutanixMachine.Spec.MemorySize,
		SystemDiskSize: m.nutanixMachine.Spec.SystemDiskSize,
		Image:          image,
		Cluster:        *cluster,
		Subnets:        subnets,
		Project:        project,
		BootType:       bootType,
		DataDisks:      dataDisks,
		GPUs:           *gpus,
		UserDataSecret: userData,
		FailureDomain:  failureDomainRef,
	}

	if len(errors) > 0 {
		return nil, warnings, errors
	}
	return &mapiProviderConfig, warnings, errors
}

func nutanixRawExtensionFromProviderSpec(spec *mapiv1.NutanixMachineProviderConfig) (*runtime.RawExtension, error) {
	if spec == nil {
		return &runtime.RawExtension{}, nil
	}

	rawBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("error marshalling providerSpec: %w", err)
	}

	return &runtime.RawExtension{
		Raw: rawBytes,
	}, nil
}

// ToMachine converts a capi2mapi MachineAndNutanixMachineTemplate into a MAPI Machine.
func (m machineAndNutanixMachineAndNutanixCluster) ToMachine() (*mapiv1beta1.Machine, []string, error) {
	if m.machine == nil || m.nutanixMachine == nil || m.nutanixCluster == nil {
		return nil, nil, errCAPIMachineNutanixMachineNutanixClusterCannotBeNil
	}

	var (
		errors   field.ErrorList
		warnings []string
	)

	mapiSpec, warns, errs := m.toProviderSpec()
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	nutanixRawExt, errRaw := nutanixRawExtensionFromProviderSpec(mapiSpec)
	if errRaw != nil {
		return nil, nil, fmt.Errorf("unable to convert Nutanix providerSpec to raw extension: %w", errRaw)
	}

	warnings = append(warnings, warns...)

	mapiMachine, fieldErrs := fromCAPIMachineToMAPIMachine(m.machine)
	if fieldErrs != nil {
		errors = append(errors, fieldErrs...)
	}

	mapiMachine.Spec.ProviderSpec.Value = nutanixRawExt

	if len(errors) > 0 {
		return nil, warnings, errors.ToAggregate()
	}

	return mapiMachine, warnings, nil
}

// ToMachineSet converts a capi2mapi MachineAndNutanixMachineTemplate into a MAPI MachineSet.
func (m machineSetAndNutanixMachineTemplateAndNutanixCluster) ToMachineSet() (*mapiv1beta1.MachineSet, []string, error) { //nolint:dupl
	if m.machineSet == nil || m.template == nil || m.nutanixCluster == nil || m.machineAndNutanixMachineAndNutanixCluster == nil {
		return nil, nil, errCAPIMachineSetNutanixMachineTemplateNutanixClusterCannotBeNil
	}

	var (
		errors   []error
		warnings []string
	)

	// Run the full ToMachine conversion so that we can check for
	// any Machine level conversion errors in the spec translation.
	mapiMachine, warns, err := m.ToMachine()
	if err != nil {
		errors = append(errors, err)
	}

	warnings = append(warnings, warns...)

	mapiMachineSet, err := fromCAPIMachineSetToMAPIMachineSet(m.machineSet)
	if err != nil {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errors)
	}

	mapiMachineSet.Spec.Template.Spec = mapiMachine.Spec
	mapiMachineSet.Spec.Template.Spec.ProviderSpec = mapiMachine.Spec.ProviderSpec

	mapiMachineSet.Spec.Template.ObjectMeta.Annotations = mapiMachine.ObjectMeta.Annotations
	mapiMachineSet.Spec.Template.ObjectMeta.Labels = mapiMachine.ObjectMeta.Labels

	return mapiMachineSet, warnings, nil
}

func convertNutanixResourceIdentifierToMapi(identifier *nutanixv1.NutanixResourceIdentifier) (*mapiv1.NutanixResourceIdentifier, field.ErrorList) {
	errors := field.ErrorList{}
	if identifier == nil {
		return &mapiv1.NutanixResourceIdentifier{}, errors
	}

	obj := mapiv1.NutanixResourceIdentifier{}
	switch identifier.Type {
	case nutanixv1.NutanixIdentifierName:
		obj.Type = mapiv1.NutanixIdentifierName
		if identifier.Name == nil {
			errors = append(errors, field.Required(field.NewPath("name"), "Name must be set for Name type identifier"))
		}
		obj.Name = ptr.To(*identifier.Name)
	case nutanixv1.NutanixIdentifierUUID:
		obj.Type = mapiv1.NutanixIdentifierUUID
		if identifier.UUID == nil {
			errors = append(errors, field.Required(field.NewPath("uuid"), "UUID must be set for UUID type identifier"))
		}
		obj.UUID = identifier.UUID
	default:
		errors = append(errors, field.Invalid(field.NewPath("type"), identifier.Type, "invalid identifier type"))
	}
	return &obj, errors
}

func convertNutanixResourceIdentifierToStorageMapi(id *nutanixv1.NutanixResourceIdentifier) (*mapiv1.NutanixStorageResourceIdentifier, field.ErrorList) {
	errors := field.ErrorList{}
	if id == nil {
		return &mapiv1.NutanixStorageResourceIdentifier{}, errors
	}
	out := &mapiv1.NutanixStorageResourceIdentifier{}
	switch id.Type {
	case nutanixv1.NutanixIdentifierUUID:
		out.Type = mapiv1.NutanixIdentifierUUID
		if id.UUID == nil {
			errors = append(errors, field.Required(field.NewPath("uuid"), "UUID must be set for UUID type identifier"))
		}
		out.UUID = id.UUID
	default:
		errors = append(errors, field.Invalid(field.NewPath("type"), id.Type, "invalid identifier type"))
	}
	return out, errors
}

func convertNutanixVMDiskToMapi(disk *nutanixv1.NutanixMachineVMDisk) (*mapiv1.NutanixVMDisk, field.ErrorList) {
	errors := field.ErrorList{}
	if disk == nil {
		return &mapiv1.NutanixVMDisk{}, errors
	}

	mapiDisk := &mapiv1.NutanixVMDisk{
		DiskSize: disk.DiskSize,
	}

	switch disk.DataSource {
	case nil:
	default:
		ds, errs := convertNutanixResourceIdentifierToMapi(disk.DataSource)
		if len(errs) > 0 {
			errors = append(errors, field.Invalid(field.NewPath("dataSource"), disk.DataSource, "DataSource failed to convert"))
		}
		mapiDisk.DataSource = ds
	}

	switch dp := disk.DeviceProperties; {
	case dp == nil:
		// leave nil
	default:
		ddp := &mapiv1.NutanixVMDiskDeviceProperties{}
		switch dp.DeviceType {
		case nutanixv1.NutanixMachineDiskDeviceTypeCDRom:
			ddp.DeviceType = mapiv1.NutanixDiskDeviceTypeCDROM
		case nutanixv1.NutanixMachineDiskDeviceTypeDisk:
			ddp.DeviceType = mapiv1.NutanixDiskDeviceTypeDisk
		default:
			errors = append(errors, field.Invalid(field.NewPath("DeviceType"), dp.DeviceType, "DeviceType should be CDRom or Disk"))
		}

		switch dp.AdapterType {
		case nutanixv1.NutanixMachineDiskAdapterTypeIDE:
			ddp.AdapterType = mapiv1.NutanixDiskAdapterTypeIDE
		case nutanixv1.NutanixMachineDiskAdapterTypePCI:
			ddp.AdapterType = mapiv1.NutanixDiskAdapterTypePCI
		case nutanixv1.NutanixMachineDiskAdapterTypeSATA:
			ddp.AdapterType = mapiv1.NutanixDiskAdapterTypeSATA
		case nutanixv1.NutanixMachineDiskAdapterTypeSCSI:
			ddp.AdapterType = mapiv1.NutanixDiskAdapterTypeSCSI
		case nutanixv1.NutanixMachineDiskAdapterTypeSPAPR:
			ddp.AdapterType = mapiv1.NutanixDiskAdapterTypeSPAPR
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

	// StorageConfig with switch
	switch sc := disk.StorageConfig; {
	case sc == nil:
		// leave nil
	default:
		storage := &mapiv1.NutanixVMStorageConfig{}
		switch sc.DiskMode {
		case nutanixv1.NutanixMachineDiskModeFlash:
			storage.DiskMode = mapiv1.NutanixDiskModeFlash
		case nutanixv1.NutanixMachineDiskModeStandard:
			storage.DiskMode = mapiv1.NutanixDiskModeStandard
		default:
			errors = append(errors, field.Invalid(field.NewPath("DiskMode"), sc.DiskMode, "DiskMode can be Standard and Flash"))
		}
		switch {
		case sc.StorageContainer != nil:
			storageContainer, errs := convertNutanixResourceIdentifierToStorageMapi(sc.StorageContainer)
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

func convertNutanixGPUToMapi(gpus *[]nutanixv1.NutanixGPU) (*[]mapiv1.NutanixGPU, field.ErrorList) {
	mapiGPUs := make([]mapiv1.NutanixGPU, 0, len(*gpus))
	errors := field.ErrorList{}
	for _, g := range *gpus {
		obj := mapiv1.NutanixGPU{}
		switch g.Type {
		case nutanixv1.NutanixGPUIdentifierDeviceID:
			obj.Type = mapiv1.NutanixGPUIdentifierDeviceID
			if g.DeviceID == nil {
				errors = append(errors, field.Required(field.NewPath("gpus").Index(len(mapiGPUs)), "DeviceID must be set for DeviceID type GPU"))
				continue
			}
			obj.DeviceID = ptr.To(int32(*g.DeviceID))

		case nutanixv1.NutanixGPUIdentifierName:
			obj.Type = mapiv1.NutanixGPUIdentifierName
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

func convertNutanixBootTypeToMapi(bootType nutanixv1.NutanixBootType) (mapiv1.NutanixBootType, field.ErrorList) {
	var mapiBootType mapiv1.NutanixBootType
	errors := field.ErrorList{}
	switch bootType {
	case nutanixv1.NutanixBootTypeUEFI:
		mapiBootType = mapiv1.NutanixUEFIBoot
	case nutanixv1.NutanixBootTypeLegacy:
		mapiBootType = mapiv1.NutanixLegacyBoot
	default:
		errors = append(errors, field.Invalid(field.NewPath("bootType"), bootType, "invalid boot type"))
	}
	return mapiBootType, errors
}

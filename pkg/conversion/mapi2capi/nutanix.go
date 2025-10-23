// Copyright 2025 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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

var (
	errMachineSetNil          = fmt.Errorf("machineSet cannot be nil")
	errInfraMachineObjNil     = fmt.Errorf("infrastructure machine object cannot be nil")
	errCAPIMachineNil         = fmt.Errorf("CAPI machine cannot be nil")
	errCAPXMachineTemplateNil = fmt.Errorf("CAPX machine template cannot be nil")
	errCAPIMachineSetNil      = fmt.Errorf("CAPI machineset cannot be nil")
	errNutanixMachineNil      = fmt.Errorf("nutanixMachine cannot be nil")
)

// nutanixMachineAndInfra wraps a Machine API Machine and Infrastructure object for Nutanix conversions.
type nutanixMachineAndInfra struct {
	machine        *mapiv1beta1.Machine
	infrastructure *configv1.Infrastructure
}

// nutanixMachineSetAndInfra wraps a Machine API MachineSet and Infrastructure object for Nutanix conversions.
type nutanixMachineSetAndInfra struct {
	machineSet     *mapiv1beta1.MachineSet
	infrastructure *configv1.Infrastructure
	*nutanixMachineAndInfra
}

// FromNutanixMachineAndInfra wraps a Machine API Machine for Nutanix and the OCP Infrastructure object into a mapi2capi NutanixProviderSpec.
func FromNutanixMachineAndInfra(m *mapiv1beta1.Machine, i *configv1.Infrastructure) Machine {
	return &nutanixMachineAndInfra{machine: m, infrastructure: i}
}

// FromNutanixMachineSetAndInfra wraps a Machine API MachineSet for Nutanix and the OCP Infrastructure object into a mapi2capi NutanixProviderSpec.
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

// ToMachineSetAndMachineTemplate converts a mapi2capi NutanixMachineSetAndInfra into a CAPI MachineSet and CAPX NutanixMachineTemplate.
func (m *nutanixMachineSetAndInfra) ToMachineSetAndMachineTemplate() (*clusterv1.MachineSet, client.Object, []string, error) {
	var (
		errors   []error
		warnings []string
	)

	if m == nil || m.machineSet == nil {
		errors = append(errors, errMachineSetNil)
		return nil, nil, warnings, utilerrors.NewAggregate(errors)
	}

	capiMachine, _, capxMachineTemplate, capiMachineSet, warns, errs := m.convertMachineSetComponents()
	warnings = append(warnings, warns...)
	errors = append(errors, errs...)

	// Continue processing even if there are errors to collect all validation issues
	if capiMachineSet != nil && capiMachine != nil && capxMachineTemplate != nil {
		m.configureMachineSetTemplate(capiMachineSet, capiMachine, capxMachineTemplate)
	}

	if err := m.setInfrastructureNameOnMachineSet(capiMachineSet); err != nil {
		errors = append(errors, err)
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

func (m *nutanixMachineSetAndInfra) convertMachineSetComponents() (*clusterv1.Machine, *nutanixv1.NutanixMachine, *nutanixv1.NutanixMachineTemplate, *clusterv1.MachineSet, []string, []error) {
	var (
		errors   []error
		warnings []string
	)

	capiMachine, capxMachineObj, warns, err := m.toMachineAndInfrastructureMachine()
	if err != nil {
		errors = append(errors, err.ToAggregate().Errors()...)
	}

	warnings = append(warnings, warns...)

	if capxMachineObj == nil {
		errors = append(errors, errInfraMachineObjNil)
		return nil, nil, nil, nil, warnings, errors
	}

	capxMachine, ok := capxMachineObj.(*nutanixv1.NutanixMachine)
	if !ok {
		errors = append(errors, fmt.Errorf("%w: %T", errUnexpectedObjectTypeForMachine, capxMachineObj))
		return nil, nil, nil, nil, warnings, errors
	}

	if capiMachine == nil {
		errors = append(errors, errCAPIMachineNil)
		return nil, nil, nil, nil, warnings, errors
	}

	capxMachineTemplate, templateErr := nutanixMachineToNutanixMachineTemplate(capxMachine, m.machineSet.Name, capiNamespace)
	if templateErr != nil {
		errors = append(errors, templateErr)
	}

	if capxMachineTemplate == nil {
		errors = append(errors, errCAPXMachineTemplateNil)
		return nil, nil, nil, nil, warnings, errors
	}

	capiMachineSet, machineSetErrs := fromMAPIMachineSetToCAPIMachineSet(m.machineSet)
	if machineSetErrs != nil {
		errors = append(errors, machineSetErrs.Errors()...)
	}

	if capiMachineSet == nil {
		errors = append(errors, errCAPIMachineSetNil)
		return nil, nil, nil, nil, warnings, errors
	}

	return capiMachine, capxMachine, capxMachineTemplate, capiMachineSet, warnings, errors
}

func (m *nutanixMachineSetAndInfra) configureMachineSetTemplate(capiMachineSet *clusterv1.MachineSet, capiMachine *clusterv1.Machine, capxMachineTemplate *nutanixv1.NutanixMachineTemplate) {
	capiMachineSet.Spec.Template.Spec = capiMachine.Spec

	// We have to merge these two maps so that labels and annotations added to the template objectmeta are persisted
	// along with the labels and annotations from the machine objectmeta.
	capiMachineSet.Spec.Template.ObjectMeta.Labels = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Labels, capiMachine.Labels)
	capiMachineSet.Spec.Template.ObjectMeta.Annotations = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Annotations, capiMachine.Annotations)

	// Override the reference so that it matches the NutanixMachineTemplate.
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Kind = nutanixMachineTemplateKind
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = capxMachineTemplate.Name
}

func (m *nutanixMachineSetAndInfra) setInfrastructureNameOnMachineSet(capiMachineSet *clusterv1.MachineSet) *field.Error {
	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		var infraName string
		if m.infrastructure != nil {
			infraName = m.infrastructure.Status.InfrastructureName
		}

		return field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), infraName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty")
	}

	capiMachineSet.Spec.Template.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
	capiMachineSet.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
	capiMachineSet.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName

	return nil
}

// NutanixProviderStatusFromRawExtension unmarshals a RawExtension into a NutanixMachineProviderStatus.
func NutanixProviderStatusFromRawExtension(rawExtension *runtime.RawExtension) (*mapiv1.NutanixMachineProviderStatus, error) {
	if rawExtension == nil {
		return &mapiv1.NutanixMachineProviderStatus{}, nil
	}

	status := &mapiv1.NutanixMachineProviderStatus{}
	if err := yaml.Unmarshal(rawExtension.Raw, &status); err != nil {
		return &mapiv1.NutanixMachineProviderStatus{}, fmt.Errorf("error unmarshalling providerStatus: %w", err)
	}

	return status, nil
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

//nolint:funlen
func (m *nutanixMachineAndInfra) toNutanixMachine(providerConfig *mapiv1.NutanixMachineProviderConfig) (*nutanixv1.NutanixMachine, []string, field.ErrorList) {
	var errors field.ErrorList

	var warnings []string

	if providerConfig == nil {
		errors = append(errors, field.Invalid(field.NewPath("providerConfig"), providerConfig, "providerConfig cannot be nil"))
		return nil, warnings, errors
	}

	image, errs := convertImageToCAPX(&providerConfig.Image)
	errors = append(errors, errs...)

	subnets, errs := convertSubnetsToCAPX(providerConfig.Subnets)
	errors = append(errors, errs...)

	dataDisks, errs := convertDataDisksToCAPX(providerConfig.DataDisks)
	errors = append(errors, errs...)

	gpus, errs := convertNutanixGPUToCAPX(&providerConfig.GPUs)
	errors = append(errors, errs...)

	cluster, errs := convertNutanixResourceIdentifierToCAPX(&providerConfig.Cluster)
	errors = append(errors, errs...)

	bootType, errs, newWarnings := convertNutanixBootTypeToCAPX(providerConfig.BootType, warnings)
	errors = append(errors, errs...)
	warnings = append(warnings, newWarnings...)

	project, errs := convertOptionalNutanixResourceIdentifierToCAPX(&providerConfig.Project)
	errors = append(errors, errs...)

	additionalCategories := convertCategoriesToCAPX(providerConfig.Categories)

	// Defensive check: cluster should never be nil from convertNutanixResourceIdentifierToCAPX,
	// but guard against potential panic if implementation changes.
	if cluster == nil {
		errors = append(errors, field.Invalid(field.NewPath("cluster"), cluster, "cluster resource identifier cannot be nil"))
		return nil, warnings, errors
	}

	spec := &nutanixv1.NutanixMachineSpec{
		VCPUsPerSocket:       providerConfig.VCPUsPerSocket,
		VCPUSockets:          providerConfig.VCPUSockets,
		MemorySize:           providerConfig.MemorySize,
		Image:                image,
		Cluster:              *cluster,
		BootType:             bootType,
		SystemDiskSize:       providerConfig.SystemDiskSize,
		DataDisks:            dataDisks,
		GPUs:                 gpus,
		Subnets:              subnets,
		Project:              project,
		AdditionalCategories: additionalCategories,
	}

	// Normalize nil vs empty slices for stable downstream comparisons.
	// This is critical for preventing unnecessary spec mutations that cause reconciliation loops.
	// Even though conversion functions already ensure empty slices, we normalize here as defensive programming.
	spec.DataDisks = ensureEmptySliceNotNil(spec.DataDisks)
	spec.GPUs = ensureEmptySliceNotNil(spec.GPUs)
	spec.Subnets = ensureEmptySliceNotNil(spec.Subnets)

	if spec.AdditionalCategories != nil {
		spec.AdditionalCategories = ensureEmptySliceNotNil(spec.AdditionalCategories)
	}

	return &nutanixv1.NutanixMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: nutanixv1.GroupVersion.String(),
			Kind:       nutanixMachineKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        m.machine.Name,
			Namespace:   capiNamespace,
			Labels:      providerConfig.ObjectMeta.Labels,
			Annotations: providerConfig.ObjectMeta.Annotations,
		},
		Spec: *spec,
	}, warnings, errors
}

func nutanixMachineToNutanixMachineTemplate(nutanixMachine *nutanixv1.NutanixMachine, name string, namespace string) (*nutanixv1.NutanixMachineTemplate, error) {
	if nutanixMachine == nil {
		return nil, errNutanixMachineNil
	}

	nameWithHash, err := util.GenerateInfraMachineTemplateNameWithSpecHash(name, nutanixMachine.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate infrastructure machine template name with spec hash: %w", err)
	}

	return &nutanixv1.NutanixMachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: nutanixv1.GroupVersion.String(),
			Kind:       nutanixMachineTemplateKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameWithHash,
			Namespace: namespace,
		},
		Spec: nutanixv1.NutanixMachineTemplateSpec{
			Template: nutanixv1.NutanixMachineTemplateResource{
				Spec: nutanixMachine.Spec,
			},
		},
	}, nil
}

func convertImageToCAPX(img *mapiv1.NutanixResourceIdentifier) (*nutanixv1.NutanixResourceIdentifier, field.ErrorList) {
	errors := field.ErrorList{}
	if img == nil || img.Type == "" {
		return nil, errors
	}

	converted, errs := convertNutanixResourceIdentifierToCAPX(img)
	errors = append(errors, errs...)

	return converted, errors
}

func convertSubnetsToCAPX(subnets []mapiv1.NutanixResourceIdentifier) ([]nutanixv1.NutanixResourceIdentifier, field.ErrorList) {
	errors := field.ErrorList{}
	result := make([]nutanixv1.NutanixResourceIdentifier, 0, len(subnets))

	for _, s := range subnets {
		conv, errs := convertOptionalNutanixResourceIdentifierToCAPX(&s)
		errors = append(errors, errs...)

		if conv != nil {
			result = append(result, *conv)
		}
	}

	return result, errors
}

func convertDataDisksToCAPX(disks []mapiv1.NutanixVMDisk) ([]nutanixv1.NutanixMachineVMDisk, field.ErrorList) {
	errors := field.ErrorList{}

	// Always return a non-nil slice for consistent comparisons in downstream consumers.
	// nil vs [] slice differences cause unnecessary spec mutations and reconciliation loops.
	if len(disks) == 0 {
		return make([]nutanixv1.NutanixMachineVMDisk, 0), errors
	}

	result := make([]nutanixv1.NutanixMachineVMDisk, 0, len(disks))

	for _, d := range disks {
		conv, errs := convertNutanixVMDiskToCAPX(&d)
		errors = append(errors, errs...)

		if conv != nil {
			result = append(result, *conv)
		}
	}

	return ensureEmptySliceNotNil(result), errors
}

func convertCategoriesToCAPX(categories []mapiv1.NutanixCategory) []nutanixv1.NutanixCategoryIdentifier {
	if len(categories) == 0 {
		return nil
	}

	result := make([]nutanixv1.NutanixCategoryIdentifier, len(categories))
	for i, c := range categories {
		result[i] = nutanixv1.NutanixCategoryIdentifier{
			Key:   c.Key,
			Value: c.Value,
		}
	}

	return result
}

func (m *nutanixMachineAndInfra) toMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	if m == nil || m.machine == nil {
		return nil, nil, nil, field.ErrorList{field.Invalid(field.NewPath("machine"), m, "machine cannot be nil")}
	}

	nutanixProviderConfig, err := m.nutanixProviderSpecFromRawExtension(m.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, nil, nil, field.ErrorList{field.Invalid(field.NewPath("spec", "providerSpec", "value"), m.machine.Spec.ProviderSpec.Value, err.Error())}
	}

	capxMachine, warns, errs := m.toNutanixMachine(nutanixProviderConfig)
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// Guard against nil capxMachine from toNutanixMachine if errors occurred
	if capxMachine == nil {
		return nil, nil, warnings, errors
	}

	warnings = append(warnings, warns...)

	capiMachine, errs := fromMAPIMachineToCAPIMachine(m.machine, nutanixv1.GroupVersion.String(), nutanixMachineKind)
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// Guard against nil capiMachine
	if capiMachine == nil {
		return nil, nil, warnings, errors
	}

	// Extract and plug ProviderID on CAPX, if the providerID is present on CAPI (instance has been provisioned).
	if capiMachine.Spec.ProviderID != nil {
		capxMachine.Spec.ProviderID = *capiMachine.Spec.ProviderID
	}

	m.applyProviderConfigToCAPIMachine(capiMachine, nutanixProviderConfig)

	if err := m.setInfrastructureNameOnMachine(capiMachine); err != nil {
		errors = append(errors, err)
	}

	syncLabelsAndAnnotations(capiMachine, capxMachine)

	return capiMachine, capxMachine, warnings, errors
}

func (m *nutanixMachineAndInfra) applyProviderConfigToCAPIMachine(capiMachine *clusterv1.Machine, providerConfig *mapiv1.NutanixMachineProviderConfig) {
	// Plug into Core CAPI Machine fields that come from the MAPI ProviderConfig which belong here instead of the CAPI NutanixMachineTemplate.
	if providerConfig.FailureDomain != nil {
		capiMachine.Spec.FailureDomain = ptr.To(providerConfig.FailureDomain.Name)
	}

	// Set Bootstrap configuration if UserDataSecret is provided.
	if providerConfig.UserDataSecret != nil && providerConfig.UserDataSecret.Name != "" {
		capiMachine.Spec.Bootstrap = clusterv1.Bootstrap{
			DataSecretName: &providerConfig.UserDataSecret.Name,
		}
	}
}

func (m *nutanixMachineAndInfra) setInfrastructureNameOnMachine(capiMachine *clusterv1.Machine) *field.Error {
	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		var infraName string
		if m.infrastructure != nil {
			infraName = m.infrastructure.Status.InfrastructureName
		}

		return field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), infraName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty")
	}

	capiMachine.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
	capiMachine.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName

	return nil
}

func syncLabelsAndAnnotations(capiMachine *clusterv1.Machine, capxMachine client.Object) {
	// The InfraMachine should always have the same labels and annotations as the Machine.
	// See https://github.com/kubernetes-sigs/cluster-api/blob/f88d7ae5155700c2cc367b31ddcc151c9ad579e4/internal/controllers/machineset/machineset_controller.go#L578-L579
	capiMachineAnnotations := capiMachine.GetAnnotations()
	if len(capiMachineAnnotations) > 0 {
		capxMachine.SetAnnotations(capiMachineAnnotations)
	}

	capiMachineLabels := capiMachine.GetLabels()
	if len(capiMachineLabels) > 0 {
		capxMachine.SetLabels(capiMachineLabels)
	}
}

func convertNutanixVMDiskToCAPX(disk *mapiv1.NutanixVMDisk) (*nutanixv1.NutanixMachineVMDisk, field.ErrorList) {
	errors := field.ErrorList{}
	if disk == nil {
		return &nutanixv1.NutanixMachineVMDisk{}, errors
	}

	mapiDisk := &nutanixv1.NutanixMachineVMDisk{
		DiskSize: disk.DiskSize,
	}

	ds, errs := convertDataSourceCAPX(disk.DataSource)
	errors = append(errors, errs...)

	if ds != nil {
		mapiDisk.DataSource = ds
	}

	dp, errs := convertDevicePropertiesCAPX(disk.DeviceProperties)
	errors = append(errors, errs...)

	if dp != nil {
		mapiDisk.DeviceProperties = dp
	}

	sc, errs := convertStorageConfigCAPX(disk.StorageConfig)
	errors = append(errors, errs...)

	if sc != nil {
		mapiDisk.StorageConfig = sc
	}

	return mapiDisk, errors
}

func convertDataSourceCAPX(ds *mapiv1.NutanixResourceIdentifier) (*nutanixv1.NutanixResourceIdentifier, field.ErrorList) {
	errors := field.ErrorList{}
	if ds == nil {
		return nil, errors
	}

	mapped, errs := convertNutanixResourceIdentifierToCAPX(ds)
	if len(errs) > 0 {
		errors = append(errors, field.Invalid(field.NewPath("dataSource"), ds, "DataSource failed to convert"))
	}

	// Guard against nil return from conversion function
	if mapped == nil {
		errors = append(errors, field.Invalid(field.NewPath("dataSource"), ds, "converted DataSource is nil"))
		return nil, errors
	}

	return mapped, errors
}

func convertDevicePropertiesCAPX(dp *mapiv1.NutanixVMDiskDeviceProperties) (*nutanixv1.NutanixMachineVMDiskDeviceProperties, field.ErrorList) {
	errors := field.ErrorList{}
	if dp == nil {
		return nil, errors
	}

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

	if dp.DeviceIndex != 0 {
		ddp.DeviceIndex = dp.DeviceIndex
	}

	if ddp.DeviceType == "" && ddp.AdapterType == "" && ddp.DeviceIndex == 0 {
		return nil, errors
	}

	return ddp, errors
}

func convertStorageConfigCAPX(sc *mapiv1.NutanixVMStorageConfig) (*nutanixv1.NutanixMachineVMStorageConfig, field.ErrorList) {
	errors := field.ErrorList{}
	if sc == nil {
		return nil, errors
	}

	storage := &nutanixv1.NutanixMachineVMStorageConfig{}

	switch sc.DiskMode {
	case mapiv1.NutanixDiskModeFlash:
		storage.DiskMode = nutanixv1.NutanixMachineDiskModeFlash
	case mapiv1.NutanixDiskModeStandard:
		storage.DiskMode = nutanixv1.NutanixMachineDiskModeStandard
	default:
		errors = append(errors, field.Invalid(field.NewPath("DiskMode"), sc.DiskMode, "DiskMode can be Standard and Flash"))
	}

	if sc.StorageContainer != nil {
		storageContainer, errs := convertNutanixResourceIdentifierToStorageCAPX(sc.StorageContainer)
		errors = append(errors, errs...)
		// Guard against nil storage container from conversion
		if storageContainer != nil {
			storage.StorageContainer = storageContainer
		}
	}

	if storage.DiskMode == "" && (storage.StorageContainer == nil || storage.StorageContainer.Type == "" || storage.StorageContainer.UUID == nil) {
		return nil, errors
	}

	return storage, errors
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
		} else {
			obj.UUID = id.UUID
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
		} else {
			obj.Name = identifier.Name
		}
	case mapiv1.NutanixIdentifierUUID:
		obj.Type = nutanixv1.NutanixIdentifierUUID

		if identifier.UUID == nil {
			errors = append(errors, field.Required(field.NewPath("uuid"), "UUID must be set for UUID type identifier"))
		} else {
			obj.UUID = identifier.UUID
		}
	default:
		errors = append(errors, field.Invalid(field.NewPath("type"), identifier.Type, "invalid identifier type"))
	}

	return &obj, errors
}

// convertOptionalNutanixResourceIdentifierToCAPX converts a NutanixResourceIdentifier but treats
// empty type as "unspecified" and returns nil without error. This is suitable for optional fields
// like Project and Subnets where a missing identifier should be ignored.
func convertOptionalNutanixResourceIdentifierToCAPX(identifier *mapiv1.NutanixResourceIdentifier) (*nutanixv1.NutanixResourceIdentifier, field.ErrorList) {
	errors := field.ErrorList{}
	if identifier == nil || identifier.Type == "" {
		return nil, errors
	}

	return convertNutanixResourceIdentifierToCAPX(identifier)
}

func convertNutanixBootTypeToCAPX(bootType mapiv1.NutanixBootType, warnings []string) (nutanixv1.NutanixBootType, field.ErrorList, []string) {
	var capxBootType nutanixv1.NutanixBootType

	errors := field.ErrorList{}

	switch bootType {
	case mapiv1.NutanixUEFIBoot:
		capxBootType = nutanixv1.NutanixBootTypeUEFI
	case mapiv1.NutanixLegacyBoot:
		capxBootType = nutanixv1.NutanixBootTypeLegacy
	case mapiv1.NutanixSecureBoot:
		warnings = append(warnings, "SecureBoot boot type is not supported in CAPX, using Legacy boot type instead")
		capxBootType = nutanixv1.NutanixBootTypeLegacy
	default:
		// Treat empty bootType as unspecified and default to Legacy for compatibility.
		if string(bootType) == "" {
			warnings = append(warnings, "bootType not set; defaulting to Legacy")
			capxBootType = nutanixv1.NutanixBootTypeLegacy
		} else {
			errors = append(errors, field.Invalid(field.NewPath("bootType"), bootType, "invalid boot type"))
		}
	}

	return capxBootType, errors, warnings
}

func convertNutanixGPUToCAPX(gpus *[]mapiv1.NutanixGPU) ([]nutanixv1.NutanixGPU, field.ErrorList) {
	errors := field.ErrorList{}

	// Always return a non-nil slice for consistent comparisons in downstream consumers.
	// nil vs [] slice differences cause unnecessary spec mutations and reconciliation loops.
	if gpus == nil || len(*gpus) == 0 {
		return make([]nutanixv1.NutanixGPU, 0), errors
	}

	mapiGPUs := make([]nutanixv1.NutanixGPU, 0, len(*gpus))

	for _, g := range *gpus {
		obj := nutanixv1.NutanixGPU{}

		switch g.Type {
		case mapiv1.NutanixGPUIdentifierDeviceID:
			obj.Type = nutanixv1.NutanixGPUIdentifierDeviceID

			if g.DeviceID == nil {
				errors = append(errors, field.Required(field.NewPath("gpus").Index(len(mapiGPUs)), "DeviceID must be set for DeviceID type GPU"))
			} else {
				obj.DeviceID = ptr.To(int64(*g.DeviceID))
			}

		case mapiv1.NutanixGPUIdentifierName:
			obj.Type = nutanixv1.NutanixGPUIdentifierName

			if g.Name == nil {
				errors = append(errors, field.Required(field.NewPath("gpus").Index(len(mapiGPUs)), "Name must be set for Name type GPU"))
			} else {
				obj.Name = ptr.To(*g.Name)
			}
		default:
			errors = append(errors, field.Invalid(field.NewPath("gpus").Index(len(mapiGPUs)), g.Type, "invalid GPU identifier type"))
			continue
		}

		mapiGPUs = append(mapiGPUs, obj)
	}

	return ensureEmptySliceNotNil(mapiGPUs), errors
}

// ensureEmptySliceNotNil returns an empty slice instead of nil for consistent comparisons.
// This prevents nil vs [] slice differences from causing unnecessary spec mutations.
func ensureEmptySliceNotNil[T any](s []T) []T {
	if s == nil {
		return make([]T, 0)
	}

	return s
}

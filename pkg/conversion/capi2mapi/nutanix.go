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

	if m.machine == nil {
		errors = append(errors, field.Required(field.NewPath("machine"), "machine must not be nil"))
		return nil, warnings, errors
	}
	if m.nutanixMachine == nil {
		errors = append(errors, field.Required(field.NewPath("nutanixMachine"), "nutanixMachine must not be nil"))
		return nil, warnings, errors
	}

	var userData *corev1.LocalObjectReference
	if m.machine.Spec.Bootstrap.DataSecretName != nil {
		userData = &corev1.LocalObjectReference{
			Name: *m.machine.Spec.Bootstrap.DataSecretName,
		}
	}

	var image mapiv1.NutanixResourceIdentifier
	if m.nutanixMachine.Spec.Image != nil {
		image = *convertNutanixResourceIdentifierToMapi(m.nutanixMachine.Spec.Image)
	}

	var project mapiv1.NutanixResourceIdentifier
	if m.nutanixMachine.Spec.Project != nil {
		if proj := convertNutanixResourceIdentifierToMapi(m.nutanixMachine.Spec.Project); proj != nil {
			project = *proj
		}
	}

	subnets := make([]mapiv1.NutanixResourceIdentifier, 0, len(m.nutanixMachine.Spec.Subnets))
	for _, s := range m.nutanixMachine.Spec.Subnets {
		if id := convertNutanixResourceIdentifierToMapi(&s); id != nil {
			subnets = append(subnets, *id)
		}
	}

	dataDisks := make([]mapiv1.NutanixVMDisk, 0, len(m.nutanixMachine.Spec.DataDisks))
	for _, d := range m.nutanixMachine.Spec.DataDisks {
		if disk := convertNutanixVMDiskToMapi(&d); disk != nil {
			dataDisks = append(dataDisks, *disk)
		}
	}

	gpus := make([]mapiv1.NutanixGPU, 0, len(m.nutanixMachine.Spec.GPUs))
	for _, g := range m.nutanixMachine.Spec.GPUs {
		gpu := mapiv1.NutanixGPU{
			Type: mapiv1.NutanixGPUIdentifierType(g.Type),
			Name: g.Name,
		}
		if g.DeviceID != nil {
			val := int32(*g.DeviceID)
			gpu.DeviceID = &val
		}
		gpus = append(gpus, gpu)
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
		Cluster:        *convertNutanixResourceIdentifierToMapi(&m.nutanixMachine.Spec.Cluster),
		Subnets:        subnets,
		Project:        project,
		BootType:       mapiv1.NutanixBootType(m.nutanixMachine.Spec.BootType),
		DataDisks:      dataDisks,
		GPUs:           gpus,
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
	if errs != nil {
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

func convertNutanixResourceIdentifierToMapi(identifier *nutanixv1.NutanixResourceIdentifier) *mapiv1.NutanixResourceIdentifier {
	if identifier == nil {
		return &mapiv1.NutanixResourceIdentifier{}
	}

	obj := mapiv1.NutanixResourceIdentifier{
		Type: mapiv1.NutanixIdentifierType(identifier.Type),
	}

	if identifier.Name != nil {
		obj.Name = ptr.To(*identifier.Name)
	}

	if identifier.UUID != nil {
		obj.UUID = ptr.To(*identifier.UUID)
	}

	return &obj
}

func convertNutanixResourceIdentifierToStorageMapi(id *nutanixv1.NutanixResourceIdentifier) *mapiv1.NutanixStorageResourceIdentifier {
	if id == nil {
		return &mapiv1.NutanixStorageResourceIdentifier{}
	}
	out := &mapiv1.NutanixStorageResourceIdentifier{
		Type: mapiv1.NutanixIdentifierType(id.Type),
	}
	if id.UUID != nil {
		out.UUID = ptr.To(*id.UUID)
	}
	return out
}

func convertNutanixVMDiskToMapi(disk *nutanixv1.NutanixMachineVMDisk) *mapiv1.NutanixVMDisk {
	if disk == nil {
		return &mapiv1.NutanixVMDisk{}
	}

	mapiDisk := &mapiv1.NutanixVMDisk{
		DiskSize: disk.DiskSize,
	}

	if disk.DataSource != nil {
		if ds := convertNutanixResourceIdentifierToMapi(disk.DataSource); ds != nil {
			mapiDisk.DataSource = ds
		}
	}

	if disk.DeviceProperties != nil {
		mapiDisk.DeviceProperties = &mapiv1.NutanixVMDiskDeviceProperties{}
		if disk.DeviceProperties.DeviceType != "" {
			mapiDisk.DeviceProperties.DeviceType = mapiv1.NutanixDiskDeviceType(disk.DeviceProperties.DeviceType)
		}
		if disk.DeviceProperties.AdapterType != "" {
			mapiDisk.DeviceProperties.AdapterType = mapiv1.NutanixDiskAdapterType(disk.DeviceProperties.AdapterType)
		}
		if disk.DeviceProperties.DeviceIndex != 0 {
			mapiDisk.DeviceProperties.DeviceIndex = disk.DeviceProperties.DeviceIndex
		}
		// Remove DeviceProperties if all fields are zero/nil
		if mapiDisk.DeviceProperties.DeviceType == "" &&
			mapiDisk.DeviceProperties.AdapterType == "" &&
			mapiDisk.DeviceProperties.DeviceIndex == 0 {
			mapiDisk.DeviceProperties = nil
		}
	}

	if disk.StorageConfig != nil {
		storage := &mapiv1.NutanixVMStorageConfig{}
		if disk.StorageConfig.DiskMode != "" {
			storage.DiskMode = mapiv1.NutanixDiskMode(disk.StorageConfig.DiskMode)
		}
		if disk.StorageConfig.StorageContainer != nil {
			storage.StorageContainer = convertNutanixResourceIdentifierToStorageMapi(disk.StorageConfig.StorageContainer)
		}
		// Remove StorageConfig if all fields are zero/nil
		if storage.StorageContainer != nil &&
			storage.DiskMode != "" &&
			storage.StorageContainer.Type != "" &&
			storage.StorageContainer.UUID != nil {
			mapiDisk.StorageConfig = storage
		}
	}

	return mapiDisk
}

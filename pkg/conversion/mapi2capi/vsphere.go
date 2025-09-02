/*
Copyright 2024 Red Hat, Inc.

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
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	vsphereMachineKind         = "VSphereMachine"
	vsphereMachineTemplateKind = "VSphereMachineTemplate"
)

// vsphereMachineAndInfra stores the details of a Machine API VSphereMachine and Infra.
type vsphereMachineAndInfra struct {
	machine        *mapiv1beta1.Machine
	infrastructure *configv1.Infrastructure
}

// vsphereMachineSetAndInfra stores the details of a Machine API VSphereMachineSet and Infra.
type vsphereMachineSetAndInfra struct {
	machineSet     *mapiv1beta1.MachineSet
	infrastructure *configv1.Infrastructure
	*vsphereMachineAndInfra
}

// FromVSphereMachineAndInfra wraps a Machine API Machine for vSphere and the OCP Infrastructure object into a mapi2capi VSphereMachine.
func FromVSphereMachineAndInfra(m *mapiv1beta1.Machine, i *configv1.Infrastructure) Machine {
	return &vsphereMachineAndInfra{machine: m, infrastructure: i}
}

// FromVSphereMachineSetAndInfra wraps a Machine API MachineSet for vSphere and the OCP Infrastructure object into a mapi2capi VSphereMachineSet.
func FromVSphereMachineSetAndInfra(m *mapiv1beta1.MachineSet, i *configv1.Infrastructure) MachineSet {
	return &vsphereMachineSetAndInfra{
		machineSet:     m,
		infrastructure: i,
		vsphereMachineAndInfra: &vsphereMachineAndInfra{
			machine: &mapiv1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      m.Spec.Template.ObjectMeta.Labels,
					Annotations: m.Spec.Template.ObjectMeta.Annotations,
				},
				Spec: m.Spec.Template.Spec,
			},
			infrastructure: i,
		},
	}
}

// ToMachineAndInfrastructureMachine converts a MAPI Machine to a CAPI Machine and VSphereMachine.
func (v *vsphereMachineAndInfra) ToMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, error) {
	if v.machine == nil || v.infrastructure == nil {
		return nil, nil, nil, fmt.Errorf("machine and infrastructure must not be nil")
	}

	machine, infraMachine, warnings, errs := v.toMachineAndInfrastructureMachine()
	if len(errs) > 0 {
		return nil, nil, warnings, errs.ToAggregate()
	}

	return machine, infraMachine, warnings, nil
}

// toMachineAndInfrastructureMachine is the internal implementation of the conversion.
func (v *vsphereMachineAndInfra) toMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, field.ErrorList) {
	var (
		errs     field.ErrorList
		warnings []string
	)

	providerSpec, err := vSphereProviderSpecFromRawExtension(v.machine.Spec.ProviderSpec.Value)
	if err != nil {
		errs = append(errs, field.Invalid(field.NewPath("spec", "providerSpec", "value"), v.machine.Spec.ProviderSpec.Value, fmt.Sprintf("failed to extract providerSpec: %v", err)))
		return nil, nil, warnings, errs
	}

	vsphereMachine, warn, machineErrs := v.toVSphereMachine(providerSpec)
	if len(machineErrs) > 0 {
		errs = append(errs, machineErrs...)
	}
	warnings = append(warnings, warn...)

	if len(errs) > 0 {
		return nil, nil, warnings, errs
	}

	capiMachine, machineErrs := fromMAPIMachineToCAPIMachine(v.machine, vsphereMachineKind, vspherev1.GroupVersion.String())
	if len(machineErrs) > 0 {
		errs = append(errs, machineErrs...)
	}

	if len(errs) > 0 {
		return nil, nil, warnings, errs
	}
	// Set the infrastructure reference
	capiMachine.Spec.InfrastructureRef.APIVersion = vspherev1.GroupVersion.String()
	capiMachine.Spec.InfrastructureRef.Kind = vsphereMachineKind
	capiMachine.Spec.InfrastructureRef.Name = v.machine.Name
	capiMachine.Spec.InfrastructureRef.Namespace = v.machine.Namespace
	capiMachine.Spec.ClusterName = v.infrastructure.Status.InfrastructureName
	capiMachine.Labels[clusterv1.ClusterNameLabel] = v.infrastructure.Status.InfrastructureName

	// Set bootstrap configuration if UserDataSecret is available
	if providerSpec.UserDataSecret != nil && providerSpec.UserDataSecret.Name != "" {
		capiMachine.Spec.Bootstrap = clusterv1.Bootstrap{
			DataSecretName: &providerSpec.UserDataSecret.Name,
		}
	}

	// Set ProviderID if available
	if v.machine.Spec.ProviderID != nil {
		vsphereMachine.Spec.ProviderID = v.machine.Spec.ProviderID
	}

	// Note: MAPI machines don't have a direct FailureDomain field like CAPI
	// This would need to be handled through other mechanisms if needed

	return capiMachine, vsphereMachine, warnings, nil
}

// ToMachineSetAndMachineTemplate converts a MAPI MachineSet to a CAPI MachineSet and VSphereMachineTemplate.
func (v *vsphereMachineSetAndInfra) ToMachineSetAndMachineTemplate() (*clusterv1.MachineSet, client.Object, []string, error) {
	if v.machineSet == nil || v.infrastructure == nil || v.vsphereMachineAndInfra == nil {
		return nil, nil, nil, fmt.Errorf("machineSet, infrastructure, and vsphereMachineAndInfra must not be nil")
	}

	var (
		errors   []error
		warnings []string
	)

	// Run the full ToMachine conversion to check for errors
	_, vsphereMachine, warn, err := v.ToMachineAndInfrastructureMachine()
	if err != nil {
		errors = append(errors, err)
	}
	warnings = append(warnings, warn...)

	// Convert the MachineSet
	capiMachineSet, aggErr := fromMAPIMachineSetToCAPIMachineSet(v.machineSet)
	if aggErr != nil {
		errors = append(errors, aggErr)
	}

	if len(errors) > 0 {
		return nil, nil, warnings, utilerrors.NewAggregate(errors)
	}

	// Convert the VSphereMachine to VSphereMachineTemplate
	vsphereMachineTemplate, err := vsphereMachineToVSphereMachineTemplate(vsphereMachine.(*vspherev1.VSphereMachine), v.machineSet.Name, v.infrastructure.Status.InfrastructureName)
	if err != nil {
		return nil, nil, warnings, fmt.Errorf("failed to convert VSphereMachine to VSphereMachineTemplate: %w", err)
	}

	// Set the infrastructure reference in the MachineSet
	capiMachineSet.Spec.ClusterName = v.infrastructure.Status.InfrastructureName
	capiMachineSet.Spec.Template.Spec.ClusterName = v.infrastructure.Status.InfrastructureName
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.APIVersion = vspherev1.GroupVersion.String()
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Kind = vsphereMachineTemplateKind
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = vsphereMachineTemplate.Name

	capiMachineSet.Labels[clusterv1.ClusterNameLabel] = v.infrastructure.Status.InfrastructureName
	capiMachineSet.Spec.Template.ObjectMeta.Labels[clusterv1.ClusterNameLabel] = v.infrastructure.Status.InfrastructureName

	// todo: jcallen ???
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Namespace = v.machineSet.Namespace

	// Set bootstrap configuration from provider spec if available
	providerSpec, err := vSphereProviderSpecFromRawExtension(v.machineSet.Spec.Template.Spec.ProviderSpec.Value)
	if err == nil && providerSpec.UserDataSecret != nil && providerSpec.UserDataSecret.Name != "" {
		capiMachineSet.Spec.Template.Spec.Bootstrap.DataSecretName = &providerSpec.UserDataSecret.Name
	}

	return capiMachineSet, vsphereMachineTemplate, warnings, nil
}

// vSphereProviderSpecFromRawExtension unmarshals a raw extension into a VSphereMachineProviderSpec type.
func vSphereProviderSpecFromRawExtension(rawExtension *runtime.RawExtension) (mapiv1beta1.VSphereMachineProviderSpec, error) {
	if rawExtension == nil {
		return mapiv1beta1.VSphereMachineProviderSpec{}, nil
	}

	spec := mapiv1beta1.VSphereMachineProviderSpec{}
	if err := yaml.Unmarshal(rawExtension.Raw, &spec); err != nil {
		return mapiv1beta1.VSphereMachineProviderSpec{}, fmt.Errorf("error unmarshalling providerSpec: %w", err)
	}

	return spec, nil
}

// toVSphereMachine converts a MAPI VSphereMachineProviderConfig to a CAPI VSphereMachine.
func (v *vsphereMachineAndInfra) toVSphereMachine(providerSpec mapiv1beta1.VSphereMachineProviderSpec) (*vspherev1.VSphereMachine, []string, field.ErrorList) {
	fldPath := field.NewPath("spec", "providerSpec", "value")

	var (
		errs     field.ErrorList
		warnings []string
	)

	// Convert network configuration
	capiNetworkSpec, networkWarnings, networkErrs := convertMAPINetworkSpecToCAPI(fldPath.Child("network"), providerSpec.Network)
	if len(networkErrs) > 0 {
		errs = append(errs, networkErrs...)
	}
	warnings = append(warnings, networkWarnings...)

	// Convert data disks
	capiDataDisks, diskWarnings, diskErrs := convertMAPIDataDisksToCAPI(fldPath.Child("dataDisks"), providerSpec.DataDisks)
	if len(diskErrs) > 0 {
		errs = append(errs, diskErrs...)
	}
	warnings = append(warnings, diskWarnings...)

	// Convert clone mode
	capiCloneMode := convertMAPICloneModeToCAPI(providerSpec.CloneMode)

	spec := vspherev1.VSphereMachineSpec{
		PowerOffMode: vspherev1.VirtualMachinePowerOpModeHard,
		VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
			Template:          providerSpec.Template,
			CloneMode:         capiCloneMode,
			Snapshot:          providerSpec.Snapshot,
			NumCPUs:           providerSpec.NumCPUs,
			NumCoresPerSocket: providerSpec.NumCoresPerSocket,
			MemoryMiB:         providerSpec.MemoryMiB,
			DiskGiB:           providerSpec.DiskGiB,
			TagIDs:            providerSpec.TagIDs,
			Network:           capiNetworkSpec,
		},
	}

	if len(capiDataDisks) > 0 {
		spec.DataDisks = capiDataDisks
	}

	// Set workspace fields if available
	if providerSpec.Workspace != nil {
		spec.VirtualMachineCloneSpec.Server = providerSpec.Workspace.Server
		spec.VirtualMachineCloneSpec.Datacenter = providerSpec.Workspace.Datacenter
		spec.VirtualMachineCloneSpec.Folder = providerSpec.Workspace.Folder
		spec.VirtualMachineCloneSpec.Datastore = providerSpec.Workspace.Datastore
		spec.VirtualMachineCloneSpec.ResourcePool = providerSpec.Workspace.ResourcePool
	}

	// todo: jcallen: missing vspheremachine name ******
	vsphereMachine := &vspherev1.VSphereMachine{
		Spec: spec,
		ObjectMeta: metav1.ObjectMeta{
			Name:      v.machine.Name,
			Namespace: v.machine.Namespace,
		},
	}

	// Set the type meta
	vsphereMachine.TypeMeta.APIVersion = vspherev1.GroupVersion.String()
	vsphereMachine.TypeMeta.Kind = vsphereMachineKind

	if len(errs) > 0 {
		return nil, warnings, errs
	}

	return vsphereMachine, warnings, nil
}

// vsphereMachineToVSphereMachineTemplate converts a VSphereMachine to a VSphereMachineTemplate.
func vsphereMachineToVSphereMachineTemplate(vsphereMachine *vspherev1.VSphereMachine, name string, namespace string) (*vspherev1.VSphereMachineTemplate, error) {
	nameWithHash, err := generateInfraMachineTemplateNameWithSpecHash(name, vsphereMachine.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate infrastructure machine template name with spec hash: %w", err)
	}

	// todo: jcallen: ????
	// This doesn't exist can MAPI and we certainly do not want to use CAPV default
	// VirtualMachinePowerOpModeHard VirtualMachinePowerOpMode = "hard"
	// Setting this to VirtualMachinePowerOpModeTrySoft VirtualMachinePowerOpMode = "trySoft"
	// The timeout might have to be adjusted
	vsphereMachine.Spec.PowerOffMode = vspherev1.VirtualMachinePowerOpModeHard

	template := &vspherev1.VSphereMachineTemplate{
		Spec: vspherev1.VSphereMachineTemplateSpec{
			Template: vspherev1.VSphereMachineTemplateResource{
				Spec: vsphereMachine.Spec,
			},
		},
	}

	// Set the correct TypeMeta for the template
	template.TypeMeta.APIVersion = vspherev1.GroupVersion.String()
	template.TypeMeta.Kind = vsphereMachineTemplateKind

	// Set the generated name
	template.ObjectMeta.Name = nameWithHash
	template.ObjectMeta.Namespace = namespace

	return template, nil
}

// generateInfraMachineTemplateNameWithSpecHash generates a name with a hash suffix based on the spec.
// This is a simplified implementation - in practice, you'd want to use a proper hash function.
func generateInfraMachineTemplateNameWithSpecHash(name string, spec interface{}) (string, error) {
	// For now, just return the name - in a real implementation, you'd compute a hash
	// of the spec and append it to ensure uniqueness
	return name, nil
}

//////// Conversion helpers

// convertMAPINetworkSpecToCAPI converts MAPI NetworkSpec to CAPI NetworkSpec.
func convertMAPINetworkSpecToCAPI(fldPath *field.Path, mapiNetwork mapiv1beta1.NetworkSpec) (vspherev1.NetworkSpec, []string, field.ErrorList) {
	var (
		errs     field.ErrorList
		warnings []string
	)

	devices := make([]vspherev1.NetworkDeviceSpec, len(mapiNetwork.Devices))
	for i, device := range mapiNetwork.Devices {
		devices[i] = vspherev1.NetworkDeviceSpec{
			NetworkName: device.NetworkName,
			DHCP4:       len(device.IPAddrs) == 0 && len(device.AddressesFromPools) == 0, // Use DHCP if no static IPs
			Gateway4:    device.Gateway,
			IPAddrs:     device.IPAddrs,
			Nameservers: device.Nameservers,
		}

		// Convert AddressesFromPools
		if len(device.AddressesFromPools) > 0 {
			addressesFromPools := make([]corev1.TypedLocalObjectReference, len(device.AddressesFromPools))
			for j, pool := range device.AddressesFromPools {
				addressesFromPools[j] = corev1.TypedLocalObjectReference{
					APIGroup: &pool.Group,
					Kind:     pool.Resource, // This might need adjustment based on actual mapping
					Name:     pool.Name,
				}
			}
			devices[i].AddressesFromPools = addressesFromPools
		}
	}

	return vspherev1.NetworkSpec{
		Devices: devices,
	}, warnings, errs
}

// convertMAPIDataDisksToCAPI converts MAPI DataDisks to CAPI DataDisks.
func convertMAPIDataDisksToCAPI(fldPath *field.Path, mapiDisks []mapiv1beta1.VSphereDisk) ([]vspherev1.VSphereDisk, []string, field.ErrorList) {
	var (
		errs     field.ErrorList
		warnings []string
	)

	capiDisks := make([]vspherev1.VSphereDisk, len(mapiDisks))
	for i, disk := range mapiDisks {
		capiDisks[i] = vspherev1.VSphereDisk{
			Name:    disk.Name,
			SizeGiB: disk.SizeGiB,
		}

		// Convert provisioning mode
		switch disk.ProvisioningMode {
		case mapiv1beta1.ProvisioningModeThin:
			capiDisks[i].ProvisioningMode = vspherev1.ThinProvisioningMode
		case mapiv1beta1.ProvisioningModeThick:
			capiDisks[i].ProvisioningMode = vspherev1.ThickProvisioningMode
		case mapiv1beta1.ProvisioningModeEagerlyZeroed:
			capiDisks[i].ProvisioningMode = vspherev1.EagerlyZeroedProvisioningMode
		case "":
			// Default - no setting
		default:
			errs = append(errs, field.Invalid(fldPath.Index(i).Child("provisioningMode"), disk.ProvisioningMode, "unsupported provisioning mode"))
		}
	}

	return capiDisks, warnings, errs
}

// convertMAPICloneModeToCAPI converts MAPI CloneMode to CAPI CloneMode.
func convertMAPICloneModeToCAPI(mapiMode mapiv1beta1.CloneMode) vspherev1.CloneMode {
	switch mapiMode {
	case mapiv1beta1.FullClone:
		return vspherev1.FullClone
	case mapiv1beta1.LinkedClone:
		return vspherev1.LinkedClone
	default:
		return vspherev1.FullClone // Default to FullClone
	}
}

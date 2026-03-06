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
	"github.com/openshift/cluster-capi-operator/pkg/conversion/consts"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	vSphereMachineKind         = "VSphereMachine"
	vSphereMachineTemplateKind = "VSphereMachineTemplate"
)

// vSphereMachineAndInfra stores the details of a Machine API VSphereMachine and Infra.
type vSphereMachineAndInfra struct {
	machine        *mapiv1beta1.Machine
	infrastructure *configv1.Infrastructure
}

// vSphereMachineSetAndInfra stores the details of a Machine API VSphereMachineSet and Infra.
type vSphereMachineSetAndInfra struct {
	machineSet     *mapiv1beta1.MachineSet
	infrastructure *configv1.Infrastructure
	*vSphereMachineAndInfra
}

// FromVSphereMachineAndInfra wraps a Machine API Machine for vSphere and the OCP Infrastructure object into a mapi2capi VSphereMachine.
func FromVSphereMachineAndInfra(m *mapiv1beta1.Machine, i *configv1.Infrastructure) Machine {
	return &vSphereMachineAndInfra{machine: m, infrastructure: i}
}

// FromVSphereMachineSetAndInfra wraps a Machine API MachineSet for vSphere and the OCP Infrastructure object into a mapi2capi VSphereMachineSet.
func FromVSphereMachineSetAndInfra(m *mapiv1beta1.MachineSet, i *configv1.Infrastructure) MachineSet {
	return &vSphereMachineSetAndInfra{
		machineSet:     m,
		infrastructure: i,
		vSphereMachineAndInfra: &vSphereMachineAndInfra{
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
func (v *vSphereMachineAndInfra) ToMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, error) {
	machine, infraMachine, warnings, errs := v.toMachineAndInfrastructureMachine()
	if len(errs) > 0 {
		return nil, nil, warnings, errs.ToAggregate()
	}

	return machine, infraMachine, warnings, nil
}

// toMachineAndInfrastructureMachine is the internal implementation of the conversion.
func (v *vSphereMachineAndInfra) toMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, field.ErrorList) {
	var (
		errs     field.ErrorList
		warnings []string
	)

	vSphereProviderConfig, err := vSphereProviderSpecFromRawExtension(v.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, nil, nil, field.ErrorList{field.Invalid(field.NewPath("spec", "providerSpec", "value"), v.machine.Spec.ProviderSpec.Value, err.Error())}
	}

	capvMachine, warn, machineErrs := v.toVSphereMachine(vSphereProviderConfig)
	if machineErrs != nil {
		errs = append(errs, machineErrs...)
	}

	warnings = append(warnings, warn...)

	capiMachine, machineErrs := fromMAPIMachineToCAPIMachine(v.machine, vspherev1.GroupVersion.Group, vSphereMachineKind)
	if machineErrs != nil {
		errs = append(errs, machineErrs...)
	}

	// Set ProviderID if available
	if v.machine.Spec.ProviderID != nil {
		capvMachine.Spec.ProviderID = v.machine.Spec.ProviderID
	}

	// Set FailureDomain from MAPI machine zone label
	// vSphere doesn't have a FailureDomain field in the provider spec, so it's stored in metadata
	if zone, ok := v.machine.Labels[consts.MAPIMachineMetadataLabelZone]; ok && zone != "" {
		capiMachine.Spec.FailureDomain = zone
	}

	// Plug into Core CAPI Machine fields that come from the MAPI ProviderConfig which belong here instead of the CAPI VSphereMachineTemplate.
	if vSphereProviderConfig.UserDataSecret != nil && vSphereProviderConfig.UserDataSecret.Name != "" {
		capiMachine.Spec.Bootstrap = clusterv1.Bootstrap{
			DataSecretName: &vSphereProviderConfig.UserDataSecret.Name,
		}
	}

	// Populate the CAPI Machine ClusterName from the OCP Infrastructure object
	if v.infrastructure == nil || v.infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), v.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachine.Spec.ClusterName = v.infrastructure.Status.InfrastructureName
		capiMachine.Labels[clusterv1.ClusterNameLabel] = v.infrastructure.Status.InfrastructureName
	}

	// The InfraMachine should always have the same labels and annotations as the Machine.
	// See https://github.com/kubernetes-sigs/cluster-api/blob/f88d7ae5155700c2cc367b31ddcc151c9ad579e4/internal/controllers/machineset/machineset_controller.go#L578-L579
	capiMachineAnnotations := capiMachine.GetAnnotations()
	if len(capiMachineAnnotations) > 0 {
		capvMachine.SetAnnotations(capiMachineAnnotations)
	}

	capiMachineLabels := capiMachine.GetLabels()
	if len(capiMachineLabels) > 0 {
		capvMachine.SetLabels(capiMachineLabels)
	}

	return capiMachine, capvMachine, warnings, errs
}

// ToMachineSetAndMachineTemplate converts a mapi2capi vSphereMachineSetAndInfra into a CAPI MachineSet and CAPV vSphereMachineTemplate.
//
//nolint:dupl
func (v *vSphereMachineSetAndInfra) ToMachineSetAndMachineTemplate() (*clusterv1.MachineSet, client.Object, []string, error) {
	var (
		errors   []error
		warnings []string
	)

	// Run the full ToMachine conversion to check for errors
	capiMachine, capvMachineObj, warns, machineErrs := v.toMachineAndInfrastructureMachine()
	if machineErrs != nil {
		errors = append(errors, machineErrs.ToAggregate().Errors()...)
	}

	warnings = append(warnings, warns...)

	capvMachine, ok := capvMachineObj.(*vspherev1.VSphereMachine)
	if !ok {
		panic(fmt.Errorf("%w: %T", errUnexpectedObjectTypeForMachine, capvMachineObj))
	}

	capvMachineTemplate, err := vSphereMachineToVSphereMachineTemplate(capvMachine, v.machineSet.Name, capiNamespace)
	if err != nil {
		errors = append(errors, err)
	}

	capiMachineSet, machineSetErrs := fromMAPIMachineSetToCAPIMachineSet(v.machineSet)
	if machineSetErrs != nil {
		errors = append(errors, machineSetErrs.Errors()...)
	}

	if capiMachine.Spec.MinReadySeconds == nil {
		capiMachine.Spec.MinReadySeconds = capiMachineSet.Spec.Template.Spec.MinReadySeconds
	}

	capiMachineSet.Spec.Template.Spec = capiMachine.Spec

	// We have to merge these two maps so that labels and annotations added to the template objectmeta are persisted
	// along with the labels and annotations from the machine objectmeta.
	capiMachineSet.Spec.Template.ObjectMeta.Labels = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Labels, capiMachine.Labels)
	capiMachineSet.Spec.Template.ObjectMeta.Annotations = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Annotations, capiMachine.Annotations)

	// Override the reference so that it matches the VSphereMachineTemplate.
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Kind = vSphereMachineTemplateKind
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = capvMachineTemplate.Name

	if v.infrastructure == nil || v.infrastructure.Status.InfrastructureName == "" {
		errors = append(errors, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), v.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachineSet.Spec.Template.Spec.ClusterName = v.infrastructure.Status.InfrastructureName
		capiMachineSet.Spec.ClusterName = v.infrastructure.Status.InfrastructureName
		capiMachineSet.Labels[clusterv1.ClusterNameLabel] = v.infrastructure.Status.InfrastructureName
	}

	if len(errors) > 0 {
		return nil, nil, warnings, utilerrors.NewAggregate(errors)
	}

	return capiMachineSet, capvMachineTemplate, warnings, nil
}

// toVSphereMachine converts a MAPI VSphereMachineProviderConfig to a CAPI VSphereMachine.
func (v *vSphereMachineAndInfra) toVSphereMachine(providerSpec mapiv1beta1.VSphereMachineProviderSpec) (*vspherev1.VSphereMachine, []string, field.ErrorList) {
	var (
		errs     field.ErrorList
		warnings []string
	)

	fldPath := field.NewPath("spec", "providerSpec", "value")

	// Convert network configuration
	capiNetworkSpec, networkWarnings, networkErrs := convertMAPINetworkSpecToCAPI(fldPath.Child("network"), providerSpec.Network)
	if len(networkErrs) > 0 {
		errs = append(errs, networkErrs...)
	}

	warnings = append(warnings, networkWarnings...)

	// Convert data disks
	capiDataDisks, diskErrs := convertMAPIDataDisksToCAPI(fldPath.Child("dataDisks"), providerSpec.DataDisks)
	if len(diskErrs) > 0 {
		errs = append(errs, diskErrs...)
	}

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

	return &vspherev1.VSphereMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: vspherev1.GroupVersion.String(),
			Kind:       vSphereMachineKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      v.machine.Name,
			Namespace: capiNamespace,
		},
		Spec: spec,
	}, warnings, errs
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

// vSphereMachineToVSphereMachineTemplate converts a VSphereMachine to a VSphereMachineTemplate.
func vSphereMachineToVSphereMachineTemplate(vSphereMachine *vspherev1.VSphereMachine, name string, namespace string) (*vspherev1.VSphereMachineTemplate, error) {
	nameWithHash, err := util.GenerateInfraMachineTemplateNameWithSpecHash(name, vSphereMachine.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate infrastructure machine template name with spec hash: %w", err)
	}

	return &vspherev1.VSphereMachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: vspherev1.GroupVersion.String(),
			Kind:       vSphereMachineTemplateKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameWithHash,
			Namespace: namespace,
		},
		Spec: vspherev1.VSphereMachineTemplateSpec{
			Template: vspherev1.VSphereMachineTemplateResource{
				Spec: vSphereMachine.Spec,
			},
		},
	}, nil
}

//////// Conversion helpers

// convertMAPINetworkSpecToCAPI converts MAPI NetworkSpec to CAPI NetworkSpec.
func convertMAPINetworkSpecToCAPI(fldPath *field.Path, mapiNetwork mapiv1beta1.NetworkSpec) (vspherev1.NetworkSpec, []string, field.ErrorList) { //nolint:unparam
	var (
		errs     field.ErrorList
		warnings []string
	)

	if len(mapiNetwork.Devices) == 0 {
		return vspherev1.NetworkSpec{
			Devices: nil,
		}, warnings, errs
	}

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
func convertMAPIDataDisksToCAPI(fldPath *field.Path, mapiDisks []mapiv1beta1.VSphereDisk) ([]vspherev1.VSphereDisk, field.ErrorList) {
	var (
		errs field.ErrorList
	)

	// Return nil disks slice if empty to ensure roundtrip consistency
	// (MAPI nil -> CAPI nil -> MAPI nil)
	if len(mapiDisks) == 0 {
		return nil, errs
	}

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

	return capiDisks, errs
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

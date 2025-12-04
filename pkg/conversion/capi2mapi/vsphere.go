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
package capi2mapi

import (
	"errors"
	"fmt"
	"reflect"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/consts"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var (
	errCAPIMachineVSphereMachineVSphereClusterCannotBeNil            = errors.New("provided Machine, VSphereMachine and VSphereCluster can not be nil")
	errCAPIMachineSetVSphereMachineTemplateVSphereClusterCannotBeNil = errors.New("provided MachineSet, VSphereMachineTemplate and VSphereCluster can not be nil")
)

const (
	errUnsupportedCAPVProvisioningMode = "unable to convert provisioning mode, unknown value"
	errUnsupportedCAPVCloneMode        = "unable to convert clone mode, unknown value"
	vsphereCredentialsSecretName       = "vsphere-cloud-credentials" //#nosec G101 -- False positive, not actually a credential.

)

// machineAndVSphereMachineAndVSphereCluster stores the details of a Cluster API Machine and VSphereMachine and VSphereCluster.
type machineAndVSphereMachineAndVSphereCluster struct {
	machine                               *clusterv1.Machine
	vSphereMachine                        *vspherev1.VSphereMachine
	vSphereCluster                        *vspherev1.VSphereCluster
	excludeMachineAPILabelsAndAnnotations bool
}

// machineSetAndVSphereMachineTemplateAndVSphereCluster stores the details of a Cluster API MachineSet and VSphereMachineTemplate and VSphereCluster.
type machineSetAndVSphereMachineTemplateAndVSphereCluster struct {
	machineSet             *clusterv1.MachineSet
	vSphereMachineTemplate *vspherev1.VSphereMachineTemplate
	vSphereCluster         *vspherev1.VSphereCluster
	*machineAndVSphereMachineAndVSphereCluster
}

// FromMachineAndVSphereMachineAndVSphereCluster wraps a CAPI Machine and CAPV VSphereMachine and CAPV VSphereCluster into a capi2mapi MachineAndInfrastructureMachine.
func FromMachineAndVSphereMachineAndVSphereCluster(m *clusterv1.Machine, vm *vspherev1.VSphereMachine, vc *vspherev1.VSphereCluster) MachineAndInfrastructureMachine {
	return &machineAndVSphereMachineAndVSphereCluster{machine: m, vSphereMachine: vm, vSphereCluster: vc}
}

// FromMachineSetAndVSphereMachineTemplateAndVSphereCluster wraps a CAPI MachineSet and CAPV VSphereMachineTemplate and CAPV VSphereCluster into a capi2mapi MachineSetAndMachineTemplate.
func FromMachineSetAndVSphereMachineTemplateAndVSphereCluster(ms *clusterv1.MachineSet, vmt *vspherev1.VSphereMachineTemplate, vc *vspherev1.VSphereCluster) MachineSetAndMachineTemplate {
	return &machineSetAndVSphereMachineTemplateAndVSphereCluster{
		machineSet:             ms,
		vSphereMachineTemplate: vmt,
		vSphereCluster:         vc,
		machineAndVSphereMachineAndVSphereCluster: &machineAndVSphereMachineAndVSphereCluster{
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      ms.Spec.Template.ObjectMeta.Labels,      //nolint:staticcheck // ObjectMeta is a field, not embedded
					Annotations: ms.Spec.Template.ObjectMeta.Annotations, //nolint:staticcheck // ObjectMeta is a field, not embedded
				},
				Spec: ms.Spec.Template.Spec,
			},
			vSphereMachine: &vspherev1.VSphereMachine{
				Spec: vmt.Spec.Template.Spec,
			},
			vSphereCluster:                        vc,
			excludeMachineAPILabelsAndAnnotations: true,
		},
	}
}

// ToMachine converts a capi2mapi MachineAndVSphereMachineTemplate into a MAPI Machine.
func (m machineAndVSphereMachineAndVSphereCluster) ToMachine() (*mapiv1beta1.Machine, []string, error) {
	if m.machine == nil || m.vSphereMachine == nil || m.vSphereCluster == nil {
		return nil, nil, errCAPIMachineVSphereMachineVSphereClusterCannotBeNil
	}

	var errs field.ErrorList

	mapaSpec, warning, err := m.toProviderSpec()
	if err != nil {
		errs = append(errs, err...)
	}

	vsphereSpecRawExt, errRaw := RawExtensionFromInterface(mapaSpec)
	if errRaw != nil {
		return nil, nil, fmt.Errorf("unable to convert vSphere providerSpec to raw extension: %w", errRaw)
	}

	additionalMachineAPIMetadataLabels, additionalMachineAPIMetadataAnnotations := m.buildAdditionalMetadata()

	mapiMachine, err := fromCAPIMachineToMAPIMachine(m.machine, additionalMachineAPIMetadataLabels, additionalMachineAPIMetadataAnnotations)

	if err != nil {
		errs = append(errs, err...)
	}

	mapiMachine.Spec.ProviderSpec.Value = vsphereSpecRawExt
	// Note: ProviderStatus is not set during conversion, similar to OpenStack and PowerVS providers.
	// During the migration period when MAPI is authoritative, the MAPI controller manages the status.
	// When CAPI becomes authoritative, the ProviderStatus will remain stale but unused, as status
	// will be tracked in the CAPI VSphereMachine status instead.

	if len(errs) > 0 {
		return nil, warning, errs.ToAggregate()
	}

	return mapiMachine, warning, nil
}

// ToMachineSet converts a capi2mapi MachineSetAndVSphereMachineTemplate into a MAPI MachineSet.
func (m machineSetAndVSphereMachineTemplateAndVSphereCluster) ToMachineSet() (*mapiv1beta1.MachineSet, []string, error) {
	if m.machineSet == nil || m.vSphereMachineTemplate == nil || m.vSphereCluster == nil || m.machineAndVSphereMachineAndVSphereCluster == nil {
		return nil, nil, errCAPIMachineSetVSphereMachineTemplateVSphereClusterCannotBeNil
	}

	var errs []error

	// Run the full ToMachine conversion so that we can check for
	// any Machine level conversion errs in the spec translation.
	mapiMachine, warn, err := m.ToMachine()
	if err != nil {
		errs = append(errs, err)
	}

	warnings := make([]string, 0, len(warn))
	warnings = append(warnings, warn...)

	mapiMachineSet, err := fromCAPIMachineSetToMAPIMachineSet(m.machineSet)
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errs)
	}

	mapiMachineSet.Spec.Template.Spec = mapiMachine.Spec

	// Copy the labels and annotations from the Machine to the template.
	// Note: The fuzzer ensures template.spec.metadata and template.metadata have the same labels/annotations
	// because CAPI only has one metadata location (template.metadata), and during roundtrip conversion
	// both MAPI locations must match to preserve the original values.
	mapiMachineSet.Spec.Template.ObjectMeta.Annotations = mapiMachine.ObjectMeta.Annotations //nolint:staticcheck // ObjectMeta is a field, not embedded
	mapiMachineSet.Spec.Template.ObjectMeta.Labels = mapiMachine.ObjectMeta.Labels           //nolint:staticcheck // ObjectMeta is a field, not embedded

	return mapiMachineSet, warnings, nil
}

// toProviderSpec converts a capi2mapi MachineAndVSphereMachineTemplateAndVSphereCluster into a MAPI VSphereMachineProviderSpec.
//
//nolint:funlen
func (m machineAndVSphereMachineAndVSphereCluster) toProviderSpec() (*mapiv1beta1.VSphereMachineProviderSpec, []string, field.ErrorList) {
	var errs field.ErrorList

	fldPath := field.NewPath("spec")

	// Convert clone mode
	mapiCloneMode, err := convertCAPVCloneModeToMAPI(fldPath.Child("cloneMode"), m.vSphereMachine.Spec.CloneMode)
	if err != nil {
		errs = append(errs, err)
	}

	// Convert network configuration
	mapiNetworkSpec, networkWarnings, networkErrors := convertCAPVNetworkSpecToMAPI(fldPath.Child("network"), m.vSphereMachine.Spec.Network)
	if len(networkErrors) > 0 {
		errs = append(errs, networkErrors...)
	}

	// Convert data disks
	// AdditionalDisksGiB is deprecated in CAPV in favor of DataDisks.
	// If AdditionalDisksGiB is set, convert it to DataDisks format first.
	allDataDisks := m.vSphereMachine.Spec.DataDisks
	if len(m.vSphereMachine.Spec.AdditionalDisksGiB) > 0 {
		// Convert AdditionalDisksGiB to VSphereDisk format and append to existing DataDisks
		for i, sizeGiB := range m.vSphereMachine.Spec.AdditionalDisksGiB {
			allDataDisks = append(allDataDisks, vspherev1.VSphereDisk{
				Name:    fmt.Sprintf("additional-disk-%d", i),
				SizeGiB: sizeGiB,
			})
		}
	}

	mapiDataDisks, diskWarnings, diskErrors := convertCAPVDataDisksToMAPI(fldPath.Child("dataDisks"), allDataDisks)
	if len(diskErrors) > 0 {
		errs = append(errs, diskErrors...)
	}

	warnings := make([]string, 0, len(networkWarnings)+len(diskWarnings))
	warnings = append(warnings, networkWarnings...)
	warnings = append(warnings, diskWarnings...)

	mapiProviderConfig := mapiv1beta1.VSphereMachineProviderSpec{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VSphereMachineProviderSpec",
			APIVersion: "machine.openshift.io/v1beta1",
		},
		Template:          m.vSphereMachine.Spec.Template,
		NumCPUs:           m.vSphereMachine.Spec.NumCPUs,
		NumCoresPerSocket: m.vSphereMachine.Spec.NumCoresPerSocket,
		MemoryMiB:         m.vSphereMachine.Spec.MemoryMiB,
		DiskGiB:           m.vSphereMachine.Spec.DiskGiB,
		TagIDs:            m.vSphereMachine.Spec.TagIDs,
		Snapshot:          m.vSphereMachine.Spec.Snapshot,
		CloneMode:         mapiCloneMode,
		Network:           mapiNetworkSpec,
		DataDisks:         mapiDataDisks,
	}

	// Create workspace from the CAPV spec fields
	workspace := &mapiv1beta1.Workspace{
		Server:       m.vSphereMachine.Spec.Server,
		Datacenter:   m.vSphereMachine.Spec.Datacenter,
		Folder:       m.vSphereMachine.Spec.Folder,
		Datastore:    m.vSphereMachine.Spec.Datastore,
		ResourcePool: m.vSphereMachine.Spec.ResourcePool,
	}

	userDataSecretName := ptr.Deref(m.machine.Spec.Bootstrap.DataSecretName, "")
	if userDataSecretName != "" {
		mapiProviderConfig.UserDataSecret = &corev1.LocalObjectReference{
			Name: userDataSecretName,
		}
	}

	mapiProviderConfig.CredentialsSecret = &corev1.LocalObjectReference{
		Name: vsphereCredentialsSecretName,
	}

	// Only set workspace if any field is set
	if workspace.Server != "" || workspace.Datacenter != "" || workspace.Folder != "" ||
		workspace.Datastore != "" || workspace.ResourcePool != "" {
		mapiProviderConfig.Workspace = workspace
	}

	// Below this line are fields not converted from the CAPI VSphereMachine.

	// ProviderID - Populated at a different level.
	// FailureDomain - Handled via zone label in buildAdditionalMetadata.
	// PowerOffMode - Controls VM power operations (hard vs soft power off), not part of machine provisioning spec.
	// GuestSoftPowerOffTimeout - Timeout for soft power operations, not part of machine provisioning spec.
	// NamingStrategy - Controls VM naming behavior, not part of machine provisioning spec.
	// Thumbprint - Ignore - Not required, TLS validation handled by cluster-level configuration.
	// StoragePolicyName - Ignore - Not supported in MAPI VSphereMachineProviderSpec.
	// Resources - Ignore - CPU/memory reservations, limits, and shares not supported in MAPI.
	// AdditionalDisksGiB - Deprecated in CAPV, converted to DataDisks above.
	// CustomVMXKeys - Ignore - Not supported in MAPI VSphereMachineProviderSpec.
	// PciDevices - Ignore - Not supported in MAPI VSphereMachineProviderSpec.
	// OS - Ignore - Not supported in MAPI VSphereMachineProviderSpec.
	// HardwareVersion - Ignore - Not supported in MAPI VSphereMachineProviderSpec.

	// There are quite a few unsupported fields, so break them out for now.
	errs = append(errs, handleUnsupportedVSphereFields(fldPath, m.vSphereMachine.Spec)...)

	if len(errs) > 0 {
		return nil, warnings, errs
	}

	return &mapiProviderConfig, warnings, nil
}

// handleUnsupportedVSphereFields checks for CAPV fields that are not supported in MAPI and returns a list of errors.
func handleUnsupportedVSphereFields(fldPath *field.Path, spec vspherev1.VSphereMachineSpec) field.ErrorList {
	errs := field.ErrorList{}

	if spec.Thumbprint != "" {
		// TLS validation is handled at the cluster level, not per-machine.
		errs = append(errs, field.Invalid(fldPath.Child("thumbprint"), spec.Thumbprint, "thumbprint is not supported"))
	}

	if spec.StoragePolicyName != "" {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("storagePolicyName"), spec.StoragePolicyName, "storagePolicyName is not supported"))
	}

	if !reflect.DeepEqual(spec.Resources, vspherev1.VirtualMachineResources{}) {
		// CPU/memory reservations, limits, and shares are not supported in MAPI.
		errs = append(errs, field.Invalid(fldPath.Child("resources"), spec.Resources, "resources are not supported"))
	}

	// AdditionalDisksGiB is deprecated in CAPV in favor of DataDisks.
	// This field is handled during conversion by merging with DataDisks.

	if len(spec.CustomVMXKeys) > 0 {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("customVMXKeys"), spec.CustomVMXKeys, "customVMXKeys are not supported"))
	}

	if len(spec.PciDevices) > 0 {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("pciDevices"), spec.PciDevices, "pciDevices are not supported"))
	}

	if spec.OS != "" {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("os"), spec.OS, "os is not supported"))
	}

	if spec.HardwareVersion != "" {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("hardwareVersion"), spec.HardwareVersion, "hardwareVersion is not supported"))
	}

	if spec.PowerOffMode != "" && spec.PowerOffMode != vspherev1.VirtualMachinePowerOpModeHard {
		// We always use hard power off mode. Other modes are not supported in MAPI.
		errs = append(errs, field.Invalid(fldPath.Child("powerOffMode"), spec.PowerOffMode, "powerOffMode must be hard or unset"))
	}

	if spec.GuestSoftPowerOffTimeout != nil {
		// This is only used with soft power off modes, which we don't support.
		errs = append(errs, field.Invalid(fldPath.Child("guestSoftPowerOffTimeout"), spec.GuestSoftPowerOffTimeout, "guestSoftPowerOffTimeout is not supported"))
	}

	if spec.NamingStrategy != nil {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("namingStrategy"), spec.NamingStrategy, "namingStrategy is not supported"))
	}

	return errs
}

//////// Conversion helpers

// buildAdditionalMetadata constructs the additional labels and annotations for the MAPI machine.
func (m machineAndVSphereMachineAndVSphereCluster) buildAdditionalMetadata() (map[string]string, map[string]string) {
	var additionalMachineAPIMetadataLabels, additionalMachineAPIMetadataAnnotations map[string]string

	// vSphere MUST set the zone label when FailureDomain is present because it's not stored in the ProviderSpec
	// (unlike AWS which has Placement.AvailabilityZone). The zone label is the only place to preserve
	// the FailureDomain during roundtrip conversion.
	if m.machine.Spec.FailureDomain != "" {
		additionalMachineAPIMetadataLabels = map[string]string{
			consts.MAPIMachineMetadataLabelZone: m.machine.Spec.FailureDomain,
		}
	}

	if !m.excludeMachineAPILabelsAndAnnotations {
		if additionalMachineAPIMetadataLabels == nil {
			additionalMachineAPIMetadataLabels = make(map[string]string)
		}
		// For vSphere, we use template name as instance type and datacenter as region
		additionalMachineAPIMetadataLabels[consts.MAPIMachineMetadataLabelInstanceType] = m.vSphereMachine.Spec.Template
		additionalMachineAPIMetadataLabels[consts.MAPIMachineMetadataLabelRegion] = m.vSphereMachine.Spec.Datacenter

		// Get instance state from VM status - use empty string if VM is not yet provisioned
		instanceState := m.getInstanceState()

		additionalMachineAPIMetadataAnnotations = map[string]string{
			consts.MAPIMachineMetadataAnnotationInstanceState: instanceState,
		}
	}

	return additionalMachineAPIMetadataLabels, additionalMachineAPIMetadataAnnotations
}

// getInstanceState determines the instance state from the VSphereMachine status.
// Returns empty string if VM is not yet provisioned, "ready" if provisioned and ready,
// or "not-ready" if provisioned but not ready.
// This matches behavior of other providers (AWS, OpenStack, PowerVS).
func (m machineAndVSphereMachineAndVSphereCluster) getInstanceState() string {
	// We check if addresses are set to determine if the VM has been provisioned
	if len(m.vSphereMachine.Status.Addresses) == 0 {
		return ""
	}

	if m.vSphereMachine.Status.Ready {
		return "ready"
	}

	return "not-ready"
}

// convertCAPVCloneModeToMAPI converts CAPV CloneMode to MAPI CloneMode.
func convertCAPVCloneModeToMAPI(fldPath *field.Path, capvMode vspherev1.CloneMode) (mapiv1beta1.CloneMode, *field.Error) {
	switch capvMode {
	case vspherev1.FullClone:
		return mapiv1beta1.FullClone, nil
	case vspherev1.LinkedClone:
		return mapiv1beta1.LinkedClone, nil
	case "":
		return "", nil
	default:
		return "", field.Invalid(fldPath, capvMode, errUnsupportedCAPVCloneMode)
	}
}

// convertCAPVNetworkSpecToMAPI converts CAPV NetworkSpec to MAPI NetworkSpec.
//
//nolint:unparam
func convertCAPVNetworkSpecToMAPI(_ *field.Path, capvNetwork vspherev1.NetworkSpec) (mapiv1beta1.NetworkSpec, []string, field.ErrorList) {
	var (
		errs     field.ErrorList
		warnings []string
	)

	// Return nil devices slice if empty to match MAPI's JSON marshaling behavior
	// (produces "devices": null instead of "devices": [])
	var devices []mapiv1beta1.NetworkDeviceSpec
	if len(capvNetwork.Devices) > 0 {
		devices = make([]mapiv1beta1.NetworkDeviceSpec, len(capvNetwork.Devices))
		for i, device := range capvNetwork.Devices {
			devices[i] = mapiv1beta1.NetworkDeviceSpec{
				NetworkName: device.NetworkName,
				Gateway:     device.Gateway4, // Map IPv4 gateway
				IPAddrs:     device.IPAddrs,
				Nameservers: device.Nameservers,
			}

			// Convert AddressesFromPools
			if len(device.AddressesFromPools) > 0 {
				addressesFromPools := make([]mapiv1beta1.AddressesFromPool, len(device.AddressesFromPools))
				for j, pool := range device.AddressesFromPools {
					addressesFromPools[j] = mapiv1beta1.AddressesFromPool{
						Group:    ptr.Deref(pool.APIGroup, ""),
						Resource: pool.Kind, // This might need adjustment based on actual mapping
						Name:     pool.Name,
					}
				}

				devices[i].AddressesFromPools = addressesFromPools
			}

			// Note: DHCP settings are not directly represented in MAPI NetworkDeviceSpec
			// The presence of DHCP4/DHCP6 in CAPV is inferred from the absence of static IPs
			if device.DHCP4 || device.DHCP6 {
				if len(device.IPAddrs) > 0 {
					warnings = append(warnings, fmt.Sprintf("device %d has both DHCP and static IPs configured, MAPI will use static IPs", i))
				}
			}
		}
	}

	return mapiv1beta1.NetworkSpec{
		Devices: devices,
	}, warnings, errs
}

// convertCAPVDataDisksToMAPI converts CAPV DataDisks to MAPI DataDisks.
//
//nolint:unparam
func convertCAPVDataDisksToMAPI(fldPath *field.Path, capvDisks []vspherev1.VSphereDisk) ([]mapiv1beta1.VSphereDisk, []string, field.ErrorList) {
	var (
		errs     field.ErrorList
		warnings []string
	)

	// Return nil disks slice if empty to match MAPI's JSON marshaling behavior
	// (produces "dataDisks": null instead of "dataDisks": [])
	if len(capvDisks) == 0 {
		return nil, warnings, errs
	}

	mapiDisks := make([]mapiv1beta1.VSphereDisk, len(capvDisks))
	for i, disk := range capvDisks {
		mapiDisks[i] = mapiv1beta1.VSphereDisk{
			Name:    disk.Name,
			SizeGiB: disk.SizeGiB,
		}

		// Convert provisioning mode
		switch disk.ProvisioningMode {
		case vspherev1.ThinProvisioningMode:
			mapiDisks[i].ProvisioningMode = mapiv1beta1.ProvisioningModeThin
		case vspherev1.ThickProvisioningMode:
			mapiDisks[i].ProvisioningMode = mapiv1beta1.ProvisioningModeThick
		case vspherev1.EagerlyZeroedProvisioningMode:
			mapiDisks[i].ProvisioningMode = mapiv1beta1.ProvisioningModeEagerlyZeroed
		case "":
			// Default - no setting
		default:
			errs = append(errs, field.Invalid(fldPath.Index(i).Child("provisioningMode"), disk.ProvisioningMode, errUnsupportedCAPVProvisioningMode))
		}
	}

	return mapiDisks, warnings, errs
}

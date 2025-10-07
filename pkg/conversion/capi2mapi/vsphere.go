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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	mapiv1 "github.com/openshift/api/machine/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
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
	machine        *clusterv1.Machine
	vsphereMachine *vspherev1.VSphereMachine
	vsphereCluster *vspherev1.VSphereCluster
}

// machineSetAndVSphereMachineTemplateAndVSphereCluster stores the details of a Cluster API MachineSet and VSphereMachineTemplate and VSphereCluster.
type machineSetAndVSphereMachineTemplateAndVSphereCluster struct {
	machineSet             *clusterv1.MachineSet
	vsphereMachineTemplate *vspherev1.VSphereMachineTemplate
	vsphereCluster         *vspherev1.VSphereCluster
	*machineAndVSphereMachineAndVSphereCluster
}

// FromMachineAndVSphereMachineAndVSphereCluster wraps a CAPI Machine and CAPV VSphereMachine and CAPV VSphereCluster into a capi2mapi MachineAndInfrastructureMachine.
func FromMachineAndVSphereMachineAndVSphereCluster(m *clusterv1.Machine, vm *vspherev1.VSphereMachine, vc *vspherev1.VSphereCluster) MachineAndInfrastructureMachine {
	return &machineAndVSphereMachineAndVSphereCluster{machine: m, vsphereMachine: vm, vsphereCluster: vc}
}

// FromMachineSetAndVSphereMachineTemplateAndVSphereCluster wraps a CAPI MachineSet and CAPV VSphereMachineTemplate and CAPV VSphereCluster into a capi2mapi MachineSetAndMachineTemplate.
func FromMachineSetAndVSphereMachineTemplateAndVSphereCluster(ms *clusterv1.MachineSet, vmt *vspherev1.VSphereMachineTemplate, vc *vspherev1.VSphereCluster) MachineSetAndMachineTemplate {
	return &machineSetAndVSphereMachineTemplateAndVSphereCluster{
		machineSet:             ms,
		vsphereMachineTemplate: vmt,
		vsphereCluster:         vc,
		machineAndVSphereMachineAndVSphereCluster: &machineAndVSphereMachineAndVSphereCluster{
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      ms.Spec.Template.ObjectMeta.Labels,
					Annotations: ms.Spec.Template.ObjectMeta.Annotations,
				},
				Spec: ms.Spec.Template.Spec,
			},
			vsphereMachine: &vspherev1.VSphereMachine{
				Spec: vmt.Spec.Template.Spec,
			},
			vsphereCluster: vc,
		},
	}
}

// ToMachine converts a capi2mapi MachineAndVSphereMachineTemplate into a MAPI Machine.
func (m machineAndVSphereMachineAndVSphereCluster) ToMachine() (*mapiv1.Machine, []string, error) {
	if m.machine == nil || m.vsphereMachine == nil || m.vsphereCluster == nil {
		return nil, nil, errCAPIMachineVSphereMachineVSphereClusterCannotBeNil
	}

	var (
		errors   field.ErrorList
		warnings []string
	)

	mapaSpec, warn, err := m.toProviderSpec()
	if err != nil {
		errors = append(errors, err...)
	}

	vsphereRawExt, errRaw := RawExtensionFromProviderSpec(mapaSpec)
	if errRaw != nil {
		return nil, nil, fmt.Errorf("unable to convert vSphere providerSpec to raw extension: %w", errRaw)
	}

	warnings = append(warnings, warn...)

	mapiMachine, err := fromCAPIMachineToMAPIMachine(m.machine)
	if err != nil {
		errors = append(errors, err...)
	}

	mapiMachine.Spec.ProviderSpec.Value = vsphereRawExt

	if len(errors) > 0 {
		return nil, warnings, errors.ToAggregate()
	}

	return mapiMachine, warnings, nil
}

// ToMachineSet converts a capi2mapi MachineSetAndVSphereMachineTemplate into a MAPI MachineSet.
func (m machineSetAndVSphereMachineTemplateAndVSphereCluster) ToMachineSet() (*mapiv1.MachineSet, []string, error) { //nolint:dupl
	if m.machineSet == nil || m.vsphereMachineTemplate == nil || m.vsphereCluster == nil || m.machineAndVSphereMachineAndVSphereCluster == nil {
		return nil, nil, errCAPIMachineSetVSphereMachineTemplateVSphereClusterCannotBeNil
	}

	var (
		errors   []error
		warnings []string
	)

	// Run the full ToMachine conversion so that we can check for
	// any Machine level conversion errors in the spec translation.
	mapiMachine, warn, err := m.ToMachine()
	if err != nil {
		errors = append(errors, err)
	}

	warnings = append(warnings, warn...)

	mapiMachineSet, err := fromCAPIMachineSetToMAPIMachineSet(m.machineSet)
	if err != nil {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errors)
	}

	mapiMachineSet.Spec.Template.Spec = mapiMachine.Spec

	// todo: jcallen: looking at existing clusters we never have these labels assigned, either in machineset or machine for mapi
	// todo: this is probably the wrong way to do this, more for testing
	mapiMachineSet.Spec.Template.Spec.ObjectMeta.Labels = nil

	// Copy the labels and annotations from the Machine to the template.
	mapiMachineSet.Spec.Template.ObjectMeta.Annotations = mapiMachine.ObjectMeta.Annotations
	mapiMachineSet.Spec.Template.ObjectMeta.Labels = mapiMachine.ObjectMeta.Labels

	// todo: jcallen: afaik the default delete policy is empty
	mapiMachineSet.Spec.DeletePolicy = ""

	//mapiMachineSet.Spec.Template.ObjectMeta.Labels[]

	return mapiMachineSet, warnings, nil
}

// toProviderSpec converts a capi2mapi MachineAndVSphereMachineTemplateAndVSphereCluster into a MAPI VSphereMachineProviderSpec.
//
//nolint:funlen
func (m machineAndVSphereMachineAndVSphereCluster) toProviderSpec() (*mapiv1.VSphereMachineProviderSpec, []string, field.ErrorList) {
	var (
		warnings []string
		errors   field.ErrorList
	)

	fldPath := field.NewPath("spec")

	// Convert clone mode
	mapiCloneMode, err := convertCAPVCloneModeToMAPI(fldPath.Child("cloneMode"), m.vsphereMachine.Spec.CloneMode)
	if err != nil {
		errors = append(errors, err)
	}

	// Convert network configuration
	mapiNetworkSpec, networkWarnings, networkErrors := convertCAPVNetworkSpecToMAPI(fldPath.Child("network"), m.vsphereMachine.Spec.Network)
	if len(networkErrors) > 0 {
		errors = append(errors, networkErrors...)
	}
	warnings = append(warnings, networkWarnings...)

	// Convert data disks
	mapiDataDisks, diskWarnings, diskErrors := convertCAPVDataDisksToMAPI(fldPath.Child("dataDisks"), m.vsphereMachine.Spec.DataDisks)
	if len(diskErrors) > 0 {
		errors = append(errors, diskErrors...)
	}
	warnings = append(warnings, diskWarnings...)

	mapiProviderConfig := mapiv1.VSphereMachineProviderSpec{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VSphereMachineProviderSpec",
			APIVersion: "machine.openshift.io/v1beta1",
		},
		Template:          m.vsphereMachine.Spec.Template,
		NumCPUs:           m.vsphereMachine.Spec.NumCPUs,
		NumCoresPerSocket: m.vsphereMachine.Spec.NumCoresPerSocket,
		MemoryMiB:         m.vsphereMachine.Spec.MemoryMiB,
		DiskGiB:           m.vsphereMachine.Spec.DiskGiB,
		TagIDs:            m.vsphereMachine.Spec.TagIDs,
		Snapshot:          m.vsphereMachine.Spec.Snapshot,
		CloneMode:         mapiCloneMode,
		Network:           mapiNetworkSpec,
		DataDisks:         mapiDataDisks,
	}

	// Create workspace from the CAPV spec fields
	workspace := &mapiv1.Workspace{
		Server:       m.vsphereMachine.Spec.Server,
		Datacenter:   m.vsphereMachine.Spec.Datacenter,
		Folder:       m.vsphereMachine.Spec.Folder,
		Datastore:    m.vsphereMachine.Spec.Datastore,
		ResourcePool: m.vsphereMachine.Spec.ResourcePool,
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

	if len(errors) > 0 {
		return nil, warnings, errors
	}

	return &mapiProviderConfig, warnings, nil
}

//////// Conversion helpers

// convertCAPVCloneModeToMAPI converts CAPV CloneMode to MAPI CloneMode.
func convertCAPVCloneModeToMAPI(fldPath *field.Path, capvMode vspherev1.CloneMode) (mapiv1.CloneMode, *field.Error) {
	switch capvMode {
	case vspherev1.FullClone:
		return mapiv1.FullClone, nil
	case vspherev1.LinkedClone:
		return mapiv1.LinkedClone, nil
	case "":
		return "", nil
	default:
		return "", field.Invalid(fldPath, capvMode, errUnsupportedCAPVCloneMode)
	}
}

// convertCAPVNetworkSpecToMAPI converts CAPV NetworkSpec to MAPI NetworkSpec.
func convertCAPVNetworkSpecToMAPI(fldPath *field.Path, capvNetwork vspherev1.NetworkSpec) (mapiv1.NetworkSpec, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	devices := make([]mapiv1.NetworkDeviceSpec, len(capvNetwork.Devices))
	for i, device := range capvNetwork.Devices {
		devices[i] = mapiv1.NetworkDeviceSpec{
			NetworkName: device.NetworkName,
			Gateway:     device.Gateway4, // Map IPv4 gateway
			IPAddrs:     device.IPAddrs,
			Nameservers: device.Nameservers,
		}

		// Convert AddressesFromPools
		if len(device.AddressesFromPools) > 0 {
			addressesFromPools := make([]mapiv1.AddressesFromPool, len(device.AddressesFromPools))
			for j, pool := range device.AddressesFromPools {
				addressesFromPools[j] = mapiv1.AddressesFromPool{
					Group:    *pool.APIGroup,
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

	return mapiv1.NetworkSpec{
		Devices: devices,
	}, warnings, errors
}

// convertCAPVDataDisksToMAPI converts CAPV DataDisks to MAPI DataDisks.
func convertCAPVDataDisksToMAPI(fldPath *field.Path, capvDisks []vspherev1.VSphereDisk) ([]mapiv1.VSphereDisk, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	mapiDisks := make([]mapiv1.VSphereDisk, len(capvDisks))
	for i, disk := range capvDisks {
		mapiDisks[i] = mapiv1.VSphereDisk{
			Name:    disk.Name,
			SizeGiB: disk.SizeGiB,
		}

		// Convert provisioning mode
		switch disk.ProvisioningMode {
		case vspherev1.ThinProvisioningMode:
			mapiDisks[i].ProvisioningMode = mapiv1.ProvisioningModeThin
		case vspherev1.ThickProvisioningMode:
			mapiDisks[i].ProvisioningMode = mapiv1.ProvisioningModeThick
		case vspherev1.EagerlyZeroedProvisioningMode:
			mapiDisks[i].ProvisioningMode = mapiv1.ProvisioningModeEagerlyZeroed
		case "":
			// Default - no setting
		default:
			errors = append(errors, field.Invalid(fldPath.Index(i).Child("provisioningMode"), disk.ProvisioningMode, errUnsupportedCAPVProvisioningMode))
		}
	}

	return mapiDisks, warnings, errors
}

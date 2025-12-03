/*
Copyright 2025 Red Hat, Inc.

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
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1alpha1 "github.com/openshift/api/machine/v1alpha1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	openstackMachineKind         = "OpenStackMachine"
	openstackMachineTemplateKind = "OpenStackMachineTemplate"
)

// openstackMachineAndInfra stores the details of a Machine API Machine for OpenStack and Infra.
type openstackMachineAndInfra struct {
	machine        *mapiv1beta1.Machine
	infrastructure *configv1.Infrastructure
}

// openstackMachineSetAndInfra stores the details of a Machine API Machine Set for OpenStack and Infra.
type openstackMachineSetAndInfra struct {
	machineSet     *mapiv1beta1.MachineSet
	infrastructure *configv1.Infrastructure
	*openstackMachineAndInfra
}

// FromOpenStackMachineAndInfra wraps a Machine API Machine for OpenStack and the OCP Infrastructure object into a mapi2capi OpenstackProviderSpec.
func FromOpenStackMachineAndInfra(m *mapiv1beta1.Machine, i *configv1.Infrastructure) Machine {
	return &openstackMachineAndInfra{machine: m, infrastructure: i}
}

// FromOpenStackMachineSetAndInfra wraps a Machine API MachineSet for OpenStack and the OCP Infrastructure object into a mapi2capi OpenstackProviderSpec.
func FromOpenStackMachineSetAndInfra(m *mapiv1beta1.MachineSet, i *configv1.Infrastructure) MachineSet {
	return &openstackMachineSetAndInfra{
		machineSet:     m,
		infrastructure: i,
		openstackMachineAndInfra: &openstackMachineAndInfra{
			machine: &mapiv1beta1.Machine{
				Spec: m.Spec.Template.Spec,
			},
			infrastructure: i,
		},
	}
}

// ToMachineAndInfrastructureMachine is used to generate a CAPI Machine and the corresponding InfrastructureMachine
// from the stored MAPI Machine and Infrastructure objects.
func (m *openstackMachineAndInfra) ToMachineAndInfrastructureMachine() (*clusterv1beta1.Machine, client.Object, []string, error) {
	capiMachine, capoMachine, warnings, errors := m.toMachineAndInfrastructureMachine()

	if len(errors) > 0 {
		return nil, nil, warnings, errors.ToAggregate()
	}

	return capiMachine, capoMachine, warnings, nil
}

func (m *openstackMachineAndInfra) toMachineAndInfrastructureMachine() (*clusterv1beta1.Machine, client.Object, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	openstackProviderConfig, err := openstackProviderSpecFromRawExtension(m.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, nil, nil, field.ErrorList{field.Invalid(field.NewPath("spec", "providerSpec", "value"), m.machine.Spec.ProviderSpec.Value, err.Error())}
	}

	capoMachine, warns, errs := m.toOpenStackMachine(openstackProviderConfig)
	if errs != nil {
		errors = append(errors, errs...)
	}

	warnings = append(warnings, warns...)

	capiMachine, errs := fromMAPIMachineToCAPIMachine(m.machine, openstackv1.SchemeGroupVersion.String(), openstackMachineKind)
	if errs != nil {
		errors = append(errors, errs...)
	}

	// Plug into Core CAPI Machine fields that come from the MAPI ProviderConfig which belong here instead of the CAPI OpenStackMachineTemplate.
	if openstackProviderConfig.AvailabilityZone != "" {
		capiMachine.Spec.FailureDomain = ptr.To(openstackProviderConfig.AvailabilityZone)
	}

	if openstackProviderConfig.UserDataSecret != nil && openstackProviderConfig.UserDataSecret.Name != "" {
		capiMachine.Spec.Bootstrap = clusterv1beta1.Bootstrap{
			DataSecretName: &openstackProviderConfig.UserDataSecret.Name,
		}
	}

	// Populate the CAPI Machine ClusterName from the OCP Infrastructure object.
	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errors = append(errors, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachine.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachine.Labels[clusterv1beta1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	// The InfraMachine should always have the same labels and annotations as the Machine.
	// See https://github.com/kubernetes-sigs/cluster-api/blob/f88d7ae5155700c2cc367b31ddcc151c9ad579e4/internal/controllers/machineset/machineset_controller.go#L578-L579
	capiMachineAnnotations := capiMachine.GetAnnotations()
	if len(capiMachineAnnotations) > 0 {
		capoMachine.SetAnnotations(capiMachineAnnotations)
	}

	capiMachineLabels := capiMachine.GetLabels()
	if len(capiMachineLabels) > 0 {
		capoMachine.SetLabels(capiMachineLabels)
	}

	return capiMachine, capoMachine, warnings, errors
}

// ToMachineSetAndMachineTemplate converts a mapi2capi OpenStackMachineSetAndInfra into a CAPI MachineSet and CAPO OpenStackMachineTemplate.
func (m *openstackMachineSetAndInfra) ToMachineSetAndMachineTemplate() (*clusterv1beta1.MachineSet, client.Object, []string, error) { //nolint:dupl
	var (
		errors   []error
		warnings []string
	)

	capiMachine, capoMachineObj, warns, machineErrs := m.toMachineAndInfrastructureMachine()
	if machineErrs != nil {
		errors = append(errors, machineErrs.ToAggregate().Errors()...)
	}

	warnings = append(warnings, warns...)

	capoMachine, ok := capoMachineObj.(*openstackv1.OpenStackMachine)
	if !ok {
		panic(fmt.Errorf("%w: %T", errUnexpectedObjectTypeForMachine, capoMachineObj))
	}

	capoMachineTemplate, err := openstackMachineToOpenStackMachineTemplate(capoMachine, m.machineSet.Name, capiNamespace)
	if err != nil {
		errors = append(errors, err)
	}

	capiMachineSet, machineSetErrs := fromMAPIMachineSetToCAPIMachineSet(m.machineSet)
	if machineSetErrs != nil {
		errors = append(errors, machineSetErrs.Errors()...)
	}

	capiMachineSet.Spec.Template.Spec = capiMachine.Spec

	// We have to merge these two maps so that labels and annotations added to the template objectmeta are persisted
	// along with the labels and annotations from the machine objectmeta.
	capiMachineSet.Spec.Template.ObjectMeta.Labels = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Labels, capiMachine.Labels)
	capiMachineSet.Spec.Template.ObjectMeta.Annotations = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Annotations, capiMachine.Annotations)

	// Override the reference so that it matches the OpenStackMachineTemplate.
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Kind = openstackMachineTemplateKind
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = capoMachineTemplate.Name

	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errors = append(errors, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachineSet.Spec.Template.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachineSet.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachineSet.Labels[clusterv1beta1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	if len(errors) > 0 {
		return nil, nil, warnings, utilerrors.NewAggregate(errors)
	}

	return capiMachineSet, capoMachineTemplate, warnings, nil
}

// toOpenStackMachine implements the ProviderSpec conversion interface for the OpenStack provider,
// it converts OpenstackProviderSpec to OpenStackMachine.
//
//nolint:funlen
func (m *openstackMachineAndInfra) toOpenStackMachine(providerSpec mapiv1alpha1.OpenstackProviderSpec) (*openstackv1.OpenStackMachine, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	fldPath := field.NewPath("spec", "providerSpec", "value")

	ports, warns, errs := convertMAPOPortsToCAPO(fldPath.Child("ports"), providerSpec.Ports)
	if errs != nil {
		errors = append(errors, errs...)
	}

	warnings = append(warnings, warns...)

	networkPorts, warns, errs := convertMAPONetworksToCAPO(fldPath.Child("networks"), providerSpec.Networks)
	if errs != nil {
		errors = append(errors, errs...)
	}

	warnings = append(warnings, warns...)
	ports = append(ports, networkPorts...)

	additionalBlockDevices, errs := convertMAPOAdditionalBlockDevicesToCAPO(fldPath.Child("additionalBlockDevices"), providerSpec.AdditionalBlockDevices)
	if errs != nil {
		errors = append(errors, errs...)
	}

	rootVolume, warns, errs := convertMAPORootVolumeToCAPO(fldPath.Child("rootVolume"), providerSpec.RootVolume)
	if errs != nil {
		errors = append(errors, errs...)
	}

	warnings = append(warnings, warns...)

	spec := openstackv1.OpenStackMachineSpec{
		AdditionalBlockDevices: *additionalBlockDevices,
		// AvailabilityZone is not provider-specific and is part of the CAPI Machine definition
		ConfigDrive: providerSpec.ConfigDrive,
		Flavor:      &providerSpec.Flavor,
		// TODO(OSASINFRA-3779): Add VAP to prevent usage.
		// FlavorID. Allows you to define flavor by ID, but MAPO uses names so we don't set this.
		// FloatingIPPoolRef. Not used in OpenShift.
		IdentityRef: convertMAPOCloudNameSecretToCAPO(providerSpec.CloudName, providerSpec.CloudsSecret),
		Image:       convertMAPOImageToCAPO(providerSpec.Image),
		// ProviderID. This is populated when this is called in higher level funcs (ToMachine(), ToMachineSet())
		Ports:      ports,
		RootVolume: rootVolume,
		// SchedulerHintAdditionalProperties. Not used in OpenShift.
		SecurityGroups: convertMAPOSecurityGroupsToCAPO(providerSpec.SecurityGroups),
		ServerGroup:    convertMAPOServerGroupToCAPO(providerSpec.ServerGroupID, providerSpec.ServerGroupName),
		ServerMetadata: convertMAPOServerMetadataToCAPO(providerSpec.ServerMetadata),
		SSHKeyName:     providerSpec.KeyName,
		Trunk:          providerSpec.Trunk,
		Tags:           providerSpec.Tags,
	}

	return &openstackv1.OpenStackMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: openstackv1.SchemeGroupVersion.String(),
			Kind:       openstackMachineKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.machine.Name,
			Namespace: capiNamespace,
		},
		Spec: spec,
	}, warnings, errors
}

// openstackProviderSpecFromRawExtension unmarshals a raw extension into an OpenStackMachineProviderSpec type.
func openstackProviderSpecFromRawExtension(rawExtension *runtime.RawExtension) (mapiv1alpha1.OpenstackProviderSpec, error) {
	if rawExtension == nil {
		return mapiv1alpha1.OpenstackProviderSpec{}, nil
	}

	spec := mapiv1alpha1.OpenstackProviderSpec{}
	if err := yaml.Unmarshal(rawExtension.Raw, &spec); err != nil {
		return mapiv1alpha1.OpenstackProviderSpec{}, fmt.Errorf("error unmarshalling providerSpec: %w", err)
	}

	return spec, nil
}

func openstackMachineToOpenStackMachineTemplate(openstackMachine *openstackv1.OpenStackMachine, name string, namespace string) (*openstackv1.OpenStackMachineTemplate, error) {
	nameWithHash, err := util.GenerateInfraMachineTemplateNameWithSpecHash(name, openstackMachine.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate infrastructure machine template name with spec hash: %w", err)
	}

	return &openstackv1.OpenStackMachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: openstackv1.SchemeGroupVersion.String(),
			Kind:       openstackMachineTemplateKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameWithHash,
			Namespace: namespace,
		},
		Spec: openstackv1.OpenStackMachineTemplateSpec{
			Template: openstackv1.OpenStackMachineTemplateResource{
				Spec: openstackMachine.Spec,
			},
		},
	}, nil
}

//////// Conversion helpers

func convertMAPOAdditionalBlockDevicesToCAPO(fldPath *field.Path, mapoAdditionalBlockDevices []mapiv1alpha1.AdditionalBlockDevice) (*[]openstackv1.AdditionalBlockDevice, field.ErrorList) {
	errors := field.ErrorList{}

	capoAdditionalBlockDevices := []openstackv1.AdditionalBlockDevice{}

	for i, mapoAdditionalBlockDevice := range mapoAdditionalBlockDevices {
		capoAdditionalBlockDevice := openstackv1.AdditionalBlockDevice{
			Name:    mapoAdditionalBlockDevice.Name,
			SizeGiB: mapoAdditionalBlockDevice.SizeGiB,
			Storage: openstackv1.BlockDeviceStorage{
				Type: openstackv1.BlockDeviceType(mapoAdditionalBlockDevice.Storage.Type),
			},
		}

		if mapoAdditionalBlockDevice.Storage.Type == mapiv1alpha1.VolumeBlockDevice {
			if mapoAdditionalBlockDevice.Storage.Volume == nil {
				// Field must be populated
				errors = append(errors, field.Required(fldPath.Index(i).Child("volume"), "volume is required, but is missing"))
				continue
			}

			name := openstackv1.VolumeAZName(mapoAdditionalBlockDevice.Storage.Volume.AvailabilityZone)
			capoAdditionalBlockDevice.Storage.Volume = &openstackv1.BlockDeviceVolume{
				AvailabilityZone: &openstackv1.VolumeAvailabilityZone{
					From: openstackv1.VolumeAZFromName,
					Name: &name,
				},
				Type: mapoAdditionalBlockDevice.Storage.Volume.Type,
			}
		}

		capoAdditionalBlockDevices = append(capoAdditionalBlockDevices, capoAdditionalBlockDevice)
	}

	return &capoAdditionalBlockDevices, errors
}

func convertMAPOCloudNameSecretToCAPO(mapoCloudName string, mapoCloudSecret *corev1.SecretReference) *openstackv1.OpenStackIdentityReference {
	if mapoCloudSecret == nil || mapoCloudSecret.Name == "" {
		return nil
	}

	capoCloudSecret := &openstackv1.OpenStackIdentityReference{
		Name:      mapoCloudSecret.Name,
		CloudName: mapoCloudName,
	}

	return capoCloudSecret
}

func convertMAPOImageToCAPO(mapoImage string) openstackv1.ImageParam {
	// NOTE(stephenfin): MAPO always uses a name
	capoImage := openstackv1.ImageParam{
		Filter: &openstackv1.ImageFilter{
			Name: &mapoImage,
		},
	}

	return capoImage
}

//nolint:funlen
func convertMAPONetworksToCAPO(fldPath *field.Path, mapoNetworks []mapiv1alpha1.NetworkParam) ([]openstackv1.PortOpts, []string, field.ErrorList) { //nolint:gocognit,cyclop,gocyclo
	errors := field.ErrorList{}
	warnings := []string{}

	capoPorts := []openstackv1.PortOpts{}

	for i, mapoNetwork := range mapoNetworks {
		capoNetworkPorts := []openstackv1.PortOpts{}

		if mapoNetwork.FixedIp != "" {
			// Field exists in the API but is never used within the codebase.
			warnings = append(warnings, field.Invalid(fldPath.Index(i).Child("fixedIP"), mapoNetwork.FixedIp, "fixedIp is ignored by MAPO, ignoring").Error())
		}

		capoNetwork := openstackv1.NetworkParam{}

		networkID := mapoNetwork.UUID
		if networkID == "" {
			networkID = mapoNetwork.Filter.ID
		}

		if networkID != "" {
			capoNetwork.ID = &networkID
		}

		// convert .Filter
		if (capoNetwork.ID == nil && mapoNetwork.Filter != mapiv1alpha1.Filter{}) {
			projectID := mapoNetwork.Filter.ProjectID
			if projectID == "" {
				projectID = mapoNetwork.Filter.TenantID
			}

			capoNetwork.Filter = &openstackv1.NetworkFilter{
				Name:        mapoNetwork.Filter.Name,
				Description: mapoNetwork.Filter.Description,
				ProjectID:   projectID,
				FilterByNeutronTags: openstackv1.FilterByNeutronTags{
					NotTags:    splitTags(mapoNetwork.Filter.NotTags),
					NotTagsAny: splitTags(mapoNetwork.Filter.NotTagsAny),
					Tags:       splitTags(mapoNetwork.Filter.Tags),
					TagsAny:    splitTags(mapoNetwork.Filter.TagsAny),
				},
			}
		}

		tags := mapoNetwork.PortTags

		// convert .Subnets
		if (capoNetwork == openstackv1.NetworkParam{}) { //nolint:nestif
			// Case: network is undefined and only has subnets
			// Create a port for each subnet
			for j, mapoSubnet := range mapoNetwork.Subnets {
				portTags := append(tags, mapoSubnet.PortTags...) //nolint:gocritic
				capoPort := openstackv1.PortOpts{Tags: portTags}

				subnetID := mapoSubnet.UUID
				if subnetID == "" {
					subnetID = mapoSubnet.Filter.ID
				}

				projectID := mapoSubnet.Filter.ProjectID
				if projectID == "" {
					projectID = mapoSubnet.Filter.TenantID
				}

				if mapoSubnet.Filter.NetworkID != "" {
					// Field exists in the API but is never used within the codebase.
					warnings = append(warnings, field.Invalid(fldPath.Index(i).Child("subnets").Index(j).Child("filter", "networkId"), mapoSubnet.Filter.NetworkID, "networkId is ignored by MAPO, ignoring").Error())
				}

				capoSubnet := openstackv1.SubnetParam{}
				if subnetID != "" {
					capoSubnet.ID = &subnetID
				}

				if (mapoSubnet.Filter != mapiv1alpha1.SubnetFilter{}) {
					capoSubnet.Filter = &openstackv1.SubnetFilter{
						CIDR:            mapoSubnet.Filter.CIDR,
						Description:     mapoSubnet.Filter.Description,
						GatewayIP:       mapoSubnet.Filter.GatewayIP,
						IPVersion:       mapoSubnet.Filter.IPVersion,
						IPv6AddressMode: mapoSubnet.Filter.IPv6AddressMode,
						IPv6RAMode:      mapoSubnet.Filter.IPv6RAMode,
						Name:            mapoSubnet.Filter.Name,
						// We ignore NetworkID since it's silently ignored by MAPO itself
						ProjectID: projectID,
						FilterByNeutronTags: openstackv1.FilterByNeutronTags{
							NotTags:    splitTags(mapoSubnet.Filter.NotTags),
							NotTagsAny: splitTags(mapoSubnet.Filter.NotTagsAny),
							Tags:       splitTags(mapoSubnet.Filter.Tags),
							TagsAny:    splitTags(mapoSubnet.Filter.TagsAny),
						},
					}
				}

				if (capoSubnet != openstackv1.SubnetParam{}) {
					capoPort.FixedIPs = []openstackv1.FixedIP{
						{Subnet: &capoSubnet},
					}
				}

				if mapoSubnet.PortSecurity != nil && !*mapoSubnet.PortSecurity {
					// negate
					disablePortSecurity := true
					capoPort.DisablePortSecurity = &disablePortSecurity
				}

				capoNetworkPorts = append(capoNetworkPorts, capoPort)
			}
		} else {
			// Case: network and subnet are defined
			// Create a single port with an interface for each subnet
			capoFixedIPs := make([]openstackv1.FixedIP, len(mapoNetwork.Subnets))

			for j, mapoSubnet := range mapoNetwork.Subnets {
				subnetID := mapoSubnet.UUID
				if subnetID == "" {
					subnetID = mapoSubnet.Filter.ID
				}

				projectID := mapoSubnet.Filter.ProjectID
				if projectID == "" {
					projectID = mapoSubnet.Filter.TenantID
				}

				if mapoSubnet.Filter.NetworkID != "" {
					// Field exists in the API but is never used within the codebase.
					warnings = append(warnings, field.Invalid(fldPath.Index(j).Child("subnets").Index(j).Child("filter", "networkId"), mapoSubnet.Filter.NetworkID, "networkId is ignored by MAPO, ignoring").Error())
				}

				capoFixedIPs[j] = openstackv1.FixedIP{
					Subnet: &openstackv1.SubnetParam{
						ID: &subnetID,
						Filter: &openstackv1.SubnetFilter{
							CIDR:            mapoSubnet.Filter.CIDR,
							Description:     mapoSubnet.Filter.Description,
							GatewayIP:       mapoSubnet.Filter.GatewayIP,
							IPVersion:       mapoSubnet.Filter.IPVersion,
							IPv6AddressMode: mapoSubnet.Filter.IPv6AddressMode,
							IPv6RAMode:      mapoSubnet.Filter.IPv6RAMode,
							Name:            mapoSubnet.Filter.Name,
							// We ignore NetworkID since it's silently ignored by MAPO itself
							ProjectID: projectID,
							FilterByNeutronTags: openstackv1.FilterByNeutronTags{
								NotTags:    splitTags(mapoSubnet.Filter.NotTags),
								NotTagsAny: splitTags(mapoSubnet.Filter.NotTagsAny),
								Tags:       splitTags(mapoSubnet.Filter.Tags),
								TagsAny:    splitTags(mapoSubnet.Filter.TagsAny),
							},
						},
					},
				}

				tags = append(tags, mapoSubnet.PortTags...)
			}

			capoPort := openstackv1.PortOpts{
				FixedIPs: capoFixedIPs,
				Network:  &capoNetwork,
				Tags:     tags,
			}

			capoNetworkPorts = append(capoNetworkPorts, capoPort)
		}

		for _, capoPort := range capoNetworkPorts {
			// convert .NoAllowedAddressPairs
			if mapoNetwork.NoAllowedAddressPairs {
				capoPort.AllowedAddressPairs = []openstackv1.AddressPair{}
			}

			// convert .PortSecurity
			if mapoNetwork.PortSecurity != nil && !*mapoNetwork.PortSecurity {
				// negate
				capoDisablePortSecurity := true
				capoPort.DisablePortSecurity = &capoDisablePortSecurity
			}

			// convert .Profile
			capoProfile := openstackv1.BindingProfile{}

			for k, v := range mapoNetwork.Profile {
				if k == "capabilities" {
					if strings.Contains(mapoNetwork.Profile["capabilities"], "switchdev") {
						capoOVSHWOffload := true
						capoProfile.OVSHWOffload = &capoOVSHWOffload
					}
				} else if k == "trusted" && v == "true" {
					capoTrustedVF := true
					capoProfile.TrustedVF = &capoTrustedVF
				}
			}

			// convert .VNICType
			if mapoNetwork.VNICType != "" {
				capoPort.VNICType = &mapoNetwork.VNICType
			}
		}

		capoPorts = append(capoPorts, capoNetworkPorts...)
	}

	return capoPorts, warnings, errors
}

//nolint:funlen,gocognit
func convertMAPOPortsToCAPO(fldPath *field.Path, mapoPorts []mapiv1alpha1.PortOpts) ([]openstackv1.PortOpts, []string, field.ErrorList) {
	errors := field.ErrorList{}
	warnings := []string{}
	capoPorts := []openstackv1.PortOpts{}

	for i, mapoPort := range mapoPorts {
		var macAddress *string
		if mapoPort.MACAddress != "" {
			macAddress = &mapoPort.MACAddress
		}

		var vnicType *string
		if mapoPort.VNICType != "" {
			vnicType = &mapoPort.VNICType
		}

		capoPort := openstackv1.PortOpts{
			Description: &mapoPort.Description,
			NameSuffix:  &mapoPort.NameSuffix,
			Network: &openstackv1.NetworkParam{
				ID: &mapoPort.NetworkID,
			},
			// We ignore the ProjectID, TenantID fields since they are ignored by MAPO
			ResolvedPortSpecFields: openstackv1.ResolvedPortSpecFields{
				AdminStateUp: mapoPort.AdminStateUp,
				MACAddress:   macAddress,
				VNICType:     vnicType,
			},
			Tags:  mapoPort.Tags,
			Trunk: mapoPort.Trunk,
		}

		// convert .AllowedAddressPairs
		capoAddressPairs := []openstackv1.AddressPair{}

		for _, mapoAddressPair := range mapoPort.AllowedAddressPairs {
			capoAddressPair := openstackv1.AddressPair{
				IPAddress: mapoAddressPair.IPAddress,
			}
			if mapoAddressPair.MACAddress != "" {
				capoAddressPair.MACAddress = &mapoAddressPair.MACAddress
			}

			capoAddressPairs = append(capoAddressPairs, capoAddressPair)
		}

		capoPort.AllowedAddressPairs = capoAddressPairs

		// convert .FixedIPs
		capoFixedIPs := []openstackv1.FixedIP{}

		for _, mapoFixedIP := range mapoPort.FixedIPs {
			capoFixedIP := openstackv1.FixedIP{
				IPAddress: &mapoFixedIP.IPAddress,
			}
			capoFixedIPs = append(capoFixedIPs, capoFixedIP)
		}

		capoPort.FixedIPs = capoFixedIPs

		// convert .PortSecurity
		if mapoPort.PortSecurity != nil && !*mapoPort.PortSecurity {
			// negate
			capoDisablePortSecurity := true
			capoPort.DisablePortSecurity = &capoDisablePortSecurity
		}

		// convert .Profile
		capoProfile := openstackv1.BindingProfile{}

		for k, v := range mapoPort.Profile {
			switch k {
			case "capabilities":
				capoOVSHWOffload := false
				if strings.Contains(v, "switchdev") {
					capoOVSHWOffload = true
				}

				capoProfile.OVSHWOffload = &capoOVSHWOffload
			case "trusted":
				capoTrustedVF := false
				if mapoPort.Profile["trusted"] == "true" {
					capoTrustedVF = true
				}

				capoProfile.TrustedVF = &capoTrustedVF
			default:
				errors = append(errors, field.Invalid(fldPath.Index(i).Child("profile"), mapoPort.Profile, "profile contains an unsupported key"))
			}
		}

		capoPort.Profile = &capoProfile

		// convert .SecurityGroups
		capoSecurityGroups := []openstackv1.SecurityGroupParam{}

		if mapoPort.SecurityGroups != nil {
			for _, mapoSecurityGroup := range *mapoPort.SecurityGroups {
				capoSecurityGroup := openstackv1.SecurityGroupParam{
					ID: &mapoSecurityGroup,
				}
				capoSecurityGroups = append(capoSecurityGroups, capoSecurityGroup)
			}
		}

		if len(capoSecurityGroups) > 0 {
			capoPort.SecurityGroups = capoSecurityGroups
		}

		// We intentionally ignore the DeprecatedHostID field since it's now ignored by
		// MAPO itself.
		if mapoPort.DeprecatedHostID != "" {
			warnings = append(warnings, field.Invalid(fldPath.Index(i).Child("hostID"), mapoPort.DeprecatedHostID, "hostID is ignored by MAPO, ignoring").Error())
		}

		capoPorts = append(capoPorts, capoPort)
	}

	return capoPorts, warnings, errors
}

func convertMAPORootVolumeToCAPO(fldPath *field.Path, mapoRootVolume *mapiv1alpha1.RootVolume) (*openstackv1.RootVolume, []string, field.ErrorList) {
	errors := field.ErrorList{}
	warnings := []string{}

	if mapoRootVolume == nil {
		return nil, warnings, errors
	}

	capoRootVolume := &openstackv1.RootVolume{}
	capoRootVolume.SizeGiB = mapoRootVolume.Size
	capoRootVolume.Type = mapoRootVolume.VolumeType

	if mapoRootVolume.Zone != "" {
		name := openstackv1.VolumeAZName(mapoRootVolume.Zone)
		capoRootVolume.AvailabilityZone = &openstackv1.VolumeAvailabilityZone{
			From: "Name",
			Name: &name,
		}
	}

	// We intentionally ignore the DeprecatedSourceType, DeprecatedDeviceType fields since they're
	// now ignored by MAPO itself and they have no equivalent in CAPO
	if mapoRootVolume.DeprecatedDeviceType != "" {
		// deviceType is deprecated and silently ignored.
		warnings = append(warnings, field.Invalid(fldPath.Child("deviceType"), mapoRootVolume.DeprecatedDeviceType, "deviceType is silently ignored by MAPO and will not be converted").Error())
	}

	if mapoRootVolume.DeprecatedSourceType != "" {
		// sourceType is deprecated and silently ignored.
		warnings = append(warnings, field.Invalid(fldPath.Child("sourceType"), mapoRootVolume.DeprecatedSourceType, "sourceType is silently ignored by MAPO and will not be converted").Error())
	}

	if mapoRootVolume.SourceUUID != "" {
		// SourceUUID is ignored if the property is set on the platform instead.
		// See https://github.com/openshift/machine-api-provider-openstack/blob/release-4.17/pkg/machine/convert.go#L163-L167
		// NOTE(stephenfin): We may wish to return this value and use it if spec.image is not set
		warnings = append(warnings, field.Invalid(fldPath.Child("sourceUUID"), mapoRootVolume.SourceUUID, "sourceUUID is superseded by spec.image in MAPO and will be ignored here").Error())
	}

	return capoRootVolume, warnings, errors
}

func convertMAPOSecurityGroupsToCAPO(mapoSecurityGroups []mapiv1alpha1.SecurityGroupParam) []openstackv1.SecurityGroupParam {
	capoSecurityGroups := []openstackv1.SecurityGroupParam{}

	for _, mapoSecurityGroup := range mapoSecurityGroups {
		capoSecurityGroup := openstackv1.SecurityGroupParam{}

		if mapoSecurityGroup.UUID != "" {
			capoSecurityGroup.ID = &mapoSecurityGroup.UUID
		} else { // Filters
			capoSecurityGroup.Filter = &openstackv1.SecurityGroupFilter{
				Name:        mapoSecurityGroup.Filter.Name,
				Description: mapoSecurityGroup.Filter.Description,
				// We ignore the TenantID field since they are ignored by MAPO
				ProjectID: mapoSecurityGroup.Filter.ProjectID,
				FilterByNeutronTags: openstackv1.FilterByNeutronTags{
					NotTags:    splitTags(mapoSecurityGroup.Filter.NotTags),
					NotTagsAny: splitTags(mapoSecurityGroup.Filter.NotTagsAny),
					Tags:       splitTags(mapoSecurityGroup.Filter.Tags),
					TagsAny:    splitTags(mapoSecurityGroup.Filter.TagsAny),
				},
			}

			if mapoSecurityGroup.Name != "" {
				capoSecurityGroup.Filter.Name = mapoSecurityGroup.Name
			}
		}

		capoSecurityGroups = append(capoSecurityGroups, capoSecurityGroup)
	}

	return capoSecurityGroups
}

func convertMAPOServerGroupToCAPO(mapoServerGroupID, mapoServerGroupName string) *openstackv1.ServerGroupParam {
	switch {
	case mapoServerGroupID != "":
		return &openstackv1.ServerGroupParam{ID: &mapoServerGroupID}
	case mapoServerGroupName != "": // name
		return &openstackv1.ServerGroupParam{
			Filter: &openstackv1.ServerGroupFilter{
				Name: &mapoServerGroupName,
			},
		}
	default:
		return nil
	}
}

func convertMAPOServerMetadataToCAPO(mapoServerMetadata map[string]string) []openstackv1.ServerMetadata {
	capoServerMetadata := []openstackv1.ServerMetadata{}
	for k, v := range mapoServerMetadata {
		capoServerMetadata = append(capoServerMetadata, openstackv1.ServerMetadata{Key: k, Value: v})
	}

	return capoServerMetadata
}

func splitTags(tags string) []openstackv1.NeutronTag {
	if tags == "" {
		return nil
	}

	var ret []openstackv1.NeutronTag

	for _, tag := range strings.Split(tags, ",") {
		if tag != "" {
			ret = append(ret, openstackv1.NeutronTag(tag))
		}
	}

	return ret
}

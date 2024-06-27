package mapi2capi

import (
	"fmt"
	"strings"

	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1alpha1 "github.com/openshift/api/machine/v1alpha1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	"github.com/openshift/cluster-capi-operator/pkg/util"
)

// OpenStackProviderSpecAndInfra stores the details of a MAPI OpenStackProviderSpec and Infra.
type OpenStackProviderSpecAndInfra struct {
	Spec           *mapiv1alpha1.OpenstackProviderSpec
	Infrastructure *configv1.Infrastructure
}

// OpenStackMachineAndInfra stores the details of a MAPI Machine and Infra.
type OpenStackMachineAndInfra struct {
	Machine        *mapiv1.Machine
	Infrastructure *configv1.Infrastructure
}

// OpenStackMachineSetAndInfra stores the details of a MAPI MachineSet and Infra.
type OpenStackMachineSetAndInfra struct {
	MachineSet     *mapiv1.MachineSet
	Infrastructure *configv1.Infrastructure
}

// FromOpenStackProviderSpecAndInfra() wraps a MAPI OpenStackMachineProviderConfig into a mapi2capi OpenStackProviderSpec.
func FromOpenStackProviderSpecAndInfra(s *mapiv1alpha1.OpenstackProviderSpec, i *configv1.Infrastructure) OpenStackProviderSpecAndInfra {
	return OpenStackProviderSpecAndInfra{Spec: s, Infrastructure: i}
}

// FromOpenStackMachineAndInfra() wraps a MAPI Machine and an Infrastructure object into a mapi2capi OpenStackMachineAndInfra object.
func FromOpenStackMachineAndInfra(m *mapiv1.Machine, i *configv1.Infrastructure) OpenStackMachineAndInfra {
	return OpenStackMachineAndInfra{Machine: m, Infrastructure: i}
}

// FromOpenStackMachineAndInfra() wraps a MAPI MachineSet and an Infrastructure object into a mapi2capi OpenStackMachineSetAndInfra object.
func FromOpenStackMachineSetAndInfra(m *mapiv1.MachineSet, i *configv1.Infrastructure) OpenStackMachineSetAndInfra {
	return OpenStackMachineSetAndInfra{MachineSet: m, Infrastructure: i}
}

// (OpenStackMachineAndInfra).ToMachineAndMachineTemplate() converts a MAPI Machine, wrapped in a mapi2capi OpenStackMachineAndInfra object, to a CAPI Machine and CAPO OpenStackMachineTemplate
func (m OpenStackMachineAndInfra) ToMachineAndMachineTemplate() (capiv1.Machine, capov1.OpenStackMachineTemplate, []string, error) {
	var errs []error
	var warnings []string

	openstackProviderConfig, err := OpenStackProviderSpecFromRawExtension(m.Machine.Spec.ProviderSpec.Value)
	if err != nil {
		errs = append(errs, err)
	}

	capoSpec, warn, err := FromOpenStackProviderSpecAndInfra(&openstackProviderConfig, m.Infrastructure).ToMachineTemplateSpec()
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	capoMachineTemplate, warn, err := openstackMachineTemplateSpecToOpenStackMachineTemplate(capoSpec, m.Machine.Name, capiNamespace)
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	capiMachine, warn, err := fromMachineToMachine(m.Machine)
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	// Plug into Core CAPI Machine fields that come from the MAPI ProviderConfig which belong here instead of the CAPI OpenStackMachineTemplate.
	if openstackProviderConfig.AvailabilityZone != "" {
		capiMachine.Spec.FailureDomain = ptr.To(openstackProviderConfig.AvailabilityZone)
	}
	if openstackProviderConfig.UserDataSecret != nil && openstackProviderConfig.UserDataSecret.Name != "" {
		capiMachine.Spec.Bootstrap = capiv1.Bootstrap{
			DataSecretName: &openstackProviderConfig.UserDataSecret.Name,
		}
	}

	// Populate the CAPI Machine ClusterName from the OCP Infrastructure object.
	if m.Infrastructure == nil || m.Infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, fmt.Errorf("infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachine.Spec.ClusterName = m.Infrastructure.Status.InfrastructureName
	}

	// Store source object.
	conversionDataAnnotationValue, err := util.GetAnnotationValueFromSourceObject(m)
	if err != nil {
		errs = append(errs, err)
	}
	if capiMachine.ObjectMeta.Annotations == nil {
		capiMachine.ObjectMeta.Annotations = map[string]string{}
	}
	capiMachine.ObjectMeta.Annotations[util.MAPIV1Beta1ConversionDataAnnotationKey] = conversionDataAnnotationValue

	if len(errs) > 0 {
		return capiv1.Machine{}, capov1.OpenStackMachineTemplate{}, warnings, utilerrors.NewAggregate(errs)
	}

	return capiMachine, capoMachineTemplate, warnings, nil
}

// (OpenStackMachineSetAndInfra).ToMachineSetAndMachineTemplate() converts a MAPI MachineSet,
// wrapped in a mapi2capi OpenStackMachineSetAndInfra object, to a CAPI MachineSet and CAPO
// OpenStackMachineTemplate
func (m OpenStackMachineSetAndInfra) ToMachineSetAndMachineTemplate() (capiv1.MachineSet, capov1.OpenStackMachineTemplate, []string, error) {
	var errs []error
	var warnings []string

	openstackProviderConfig, err := OpenStackProviderSpecFromRawExtension(m.MachineSet.Spec.Template.Spec.ProviderSpec.Value)
	if err != nil {
		errs = append(errs, err)
	}

	capoSpec, warn, err := FromOpenStackProviderSpecAndInfra(&openstackProviderConfig, m.Infrastructure).ToMachineTemplateSpec()
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	capoMachineTemplate, warn, err := openstackMachineTemplateSpecToOpenStackMachineTemplate(capoSpec, m.MachineSet.Name, capiNamespace)
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	capiMachineSet, warn, err := FromMachineSetToMachineSet(m.MachineSet)
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	// Plug into Core CAPI MachineSet fields that come from the MAPI ProviderConfig which belong here instead of the CAPI OpenStackMachineTemplate.
	if openstackProviderConfig.AvailabilityZone != "" {
		capiMachineSet.Spec.Template.Spec.FailureDomain = ptr.To(openstackProviderConfig.AvailabilityZone)
	}

	if openstackProviderConfig.UserDataSecret != nil && openstackProviderConfig.UserDataSecret.Name != "" {
		capiMachineSet.Spec.Template.Spec.Bootstrap = capiv1.Bootstrap{
			DataSecretName: &openstackProviderConfig.UserDataSecret.Name,
		}
	}
	if m.Infrastructure == nil || m.Infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, fmt.Errorf("infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachineSet.Spec.Template.Spec.ClusterName = m.Infrastructure.Status.InfrastructureName
		capiMachineSet.Spec.ClusterName = m.Infrastructure.Status.InfrastructureName
	}

	// Store source object.
	conversionDataAnnotationValue, err := util.GetAnnotationValueFromSourceObject(m)
	if err != nil {
		errs = append(errs, err)
	}

	if capiMachineSet.ObjectMeta.Annotations == nil {
		capiMachineSet.ObjectMeta.Annotations = map[string]string{}
	}
	capiMachineSet.ObjectMeta.Annotations[util.MAPIV1Beta1ConversionDataAnnotationKey] = conversionDataAnnotationValue

	if len(errs) > 0 {
		return capiv1.MachineSet{}, capov1.OpenStackMachineTemplate{}, warnings, utilerrors.NewAggregate(errs)
	}

	return capiMachineSet, capoMachineTemplate, warnings, nil
}

// (OpenStackProviderSpecAndInfra).ToMachineTemplateSpec() implements the ProviderSpec conversion interface for the OpenStack provider.
// It converts OpenStackProviderSpec to OpenStackMachineTemplateSpec.
func (p OpenStackProviderSpecAndInfra) ToMachineTemplateSpec() (capov1.OpenStackMachineTemplateSpec, []string, error) {
	var errors []error
	var warnings []string

	spec := capov1.OpenStackMachineTemplateSpec{
		Template: capov1.OpenStackMachineTemplateResource{
			Spec: capov1.OpenStackMachineSpec{
				AdditionalBlockDevices: convertMAPOAdditionalBlockDevicesToCAPO(p.Spec.AdditionalBlockDevices),
				ConfigDrive:            p.Spec.ConfigDrive,
				Flavor:                 p.Spec.Flavor,
				Image:                  convertMAPOImageToCAPO(p.Spec.Image),
				Ports:                  convertMAPOPortsToCAPO(p.Spec.Ports),
				RootVolume:             convertMAPORootVolumeToCAPO(*p.Spec.RootVolume),
				SecurityGroups:         convertMAPOSecurityGroupsToCAPO(p.Spec.SecurityGroups),
				ServerGroup:            convertMAPOServerGroupToCAPO(p.Spec.ServerGroupID, p.Spec.ServerGroupName),
				ServerMetadata:         convertMAPOServerMetadataToCAPO(p.Spec.ServerMetadata),
				SSHKeyName:             p.Spec.KeyName,
				Trunk:                  p.Spec.Trunk,
				Tags:                   p.Spec.Tags,
			},
		},
	}

	if len(errors) > 0 {
		return capov1.OpenStackMachineTemplateSpec{}, warnings, utilerrors.NewAggregate(errors)
	}

	return spec, warnings, nil
}

// OpenStackProviderSpecFromRawExtension() unmarshals a raw extension into an OpenStackMachineProviderSpec type
func OpenStackProviderSpecFromRawExtension(rawExtension *runtime.RawExtension) (mapiv1alpha1.OpenstackProviderSpec, error) {
	if rawExtension == nil {
		return mapiv1alpha1.OpenstackProviderSpec{}, nil
	}

	spec := mapiv1alpha1.OpenstackProviderSpec{}
	if err := yaml.Unmarshal(rawExtension.Raw, &spec); err != nil {
		return mapiv1alpha1.OpenstackProviderSpec{}, fmt.Errorf("error unmarshalling providerSpec: %v", err)
	}

	return spec, nil
}

func openstackMachineTemplateSpecToOpenStackMachineTemplate(spec capov1.OpenStackMachineTemplateSpec, name string, namespace string) (capov1.OpenStackMachineTemplate, []string, error) {
	var warns []string
	var errs []error

	mt := capov1.OpenStackMachineTemplate{}
	mt.ObjectMeta = metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
	}
	mt.TypeMeta = metav1.TypeMeta{
		Kind:       openstackTemplateKind,
		APIVersion: openstackTemplateAPIVersion,
	}
	mt.Name = name
	mt.Namespace = namespace
	mt.Spec = spec

	if len(errs) > 0 {
		return capov1.OpenStackMachineTemplate{}, warns, utilerrors.NewAggregate(errs)
	}

	return mt, warns, nil
}

//////// Conversion helpers

func convertMAPOAdditionalBlockDevicesToCAPO(mapoAdditionalBlockDevices []mapiv1alpha1.AdditionalBlockDevice) []capov1.AdditionalBlockDevice {
	capoAdditionalBlockDevices := []capov1.AdditionalBlockDevice{}

	for _, mapoAdditionalBlockDevice := range mapoAdditionalBlockDevices {
		capoAdditionalBlockDevice := capov1.AdditionalBlockDevice{}
		capoAdditionalBlockDevice.SizeGiB = mapoAdditionalBlockDevice.SizeGiB
		capoAdditionalBlockDevice.Storage = capov1.BlockDeviceStorage{
			Type: capov1.BlockDeviceType(mapoAdditionalBlockDevice.Storage.Type),
		}
		if mapoAdditionalBlockDevice.Storage.Type == "Volume" {
			// TODO(stephenfin): Can we be sure that '.Volume' is not nil when '.Type' == "Volume"?
			name := capov1.VolumeAZName(mapoAdditionalBlockDevice.Storage.Volume.AvailabilityZone)
			capoAdditionalBlockDevice.Storage.Volume = &capov1.BlockDeviceVolume{
				AvailabilityZone: &capov1.VolumeAvailabilityZone{
					From: "Name",
					Name: &name,
				},
				Type: mapoAdditionalBlockDevice.Storage.Volume.Type,
			}
		}

		capoAdditionalBlockDevices = append(capoAdditionalBlockDevices, capoAdditionalBlockDevice)
	}

	return capoAdditionalBlockDevices
}

func convertMAPOImageToCAPO(mapoImage string) capov1.ImageParam {
	// TODO(stephenfin): I'm pretty sure MAPO always uses a name, but should we check for UUIDs?
	capoImage := capov1.ImageParam{
		Filter: &capov1.ImageFilter{
			Name: &mapoImage,
		},
	}
	return capoImage
}

func convertMAPOPortsToCAPO(mapoPorts []mapiv1alpha1.PortOpts) []capov1.PortOpts {
	capoPorts := []capov1.PortOpts{}

	for _, mapoPort := range mapoPorts {
		capoPort := capov1.PortOpts{
			Description: &mapoPort.Description,
			NameSuffix:  &mapoPort.NameSuffix,
			Network: &capov1.NetworkParam{
				ID: &mapoPort.NetworkID,
			},
			// Lossy!!! No equivalent of ProjectID, TenantID fields
			ResolvedPortSpecFields: capov1.ResolvedPortSpecFields{
				// TODO: We need if checks for all these to avoid setting pointers to empty strings :(
				AdminStateUp: mapoPort.AdminStateUp,
				MACAddress:   &mapoPort.MACAddress,
				VNICType:     &mapoPort.VNICType,
			},
			Tags:  mapoPort.Tags,
			Trunk: mapoPort.Trunk,
		}

		// convert .AllowedAddressPairs
		capoAddressPairs := []capov1.AddressPair{}
		for _, mapoAddressPair := range mapoPort.AllowedAddressPairs {
			capoAddressPair := capov1.AddressPair{
				IPAddress: mapoAddressPair.IPAddress,
			}
			if mapoAddressPair.MACAddress != "" {
				capoAddressPair.MACAddress = &mapoAddressPair.MACAddress
			}
			capoAddressPairs = append(capoAddressPairs, capoAddressPair)
		}
		capoPort.ResolvedPortSpecFields.AllowedAddressPairs = capoAddressPairs

		// convert .FixedIPs
		capoFixedIPs := []capov1.FixedIP{}
		for _, mapoFixedIP := range mapoPort.FixedIPs {
			capoFixedIP := capov1.FixedIP{
				IPAddress: &mapoFixedIP.IPAddress,
			}
			capoFixedIPs = append(capoFixedIPs, capoFixedIP)
		}
		capoPort.FixedIPs = capoFixedIPs

		// convert .PortSecurity
		capoDisablePortSecurity := false
		if mapoPort.PortSecurity != nil && *mapoPort.PortSecurity == false {
			// negate
			capoDisablePortSecurity = true
		}
		capoPort.DisablePortSecurity = &capoDisablePortSecurity

		// convert .Profile
		capoProfile := capov1.BindingProfile{}
		if _, ok := mapoPort.Profile["capabilities"]; ok {
			capoOVSHWOffload := false
			if strings.Contains(mapoPort.Profile["capabilities"], "switchdev") {
				capoOVSHWOffload = true
			}
			capoProfile.OVSHWOffload = &capoOVSHWOffload
		}
		if _, ok := mapoPort.Profile["trusted"]; ok {
			capoTrustedVF := false
			// TODO(stephenfin): Does neutron allow other "truthy" values?
			if mapoPort.Profile["trusted"] == "true" {
				capoTrustedVF = true
			}
			capoProfile.TrustedVF = &capoTrustedVF
		}
		// LOSSY!! We don't/can't handle other profile flags.
		capoPort.Profile = &capoProfile

		// convert .SecurityGroups
		capoSecurityGroups := []capov1.SecurityGroupParam{}
		for _, mapoSecurityGroup := range *mapoPort.SecurityGroups {
			capoSecurityGroup := capov1.SecurityGroupParam{
				ID: &mapoSecurityGroup,
			}
			capoSecurityGroups = append(capoSecurityGroups, capoSecurityGroup)
		}
		capoPort.SecurityGroups = capoSecurityGroups

		capoPorts = append(capoPorts, capoPort)
	}

	// We intentionally ignore the DeprecatedHostID field since it's now ignored by
	// MAPO itself.

	return capoPorts
}

func convertMAPORootVolumeToCAPO(mapoRootVolume mapiv1alpha1.RootVolume) *capov1.RootVolume {
	capoRootVolume := &capov1.RootVolume{}
	// TODO(stephenfin): CAPO uses GiB, MAPO allegedly uses GB. Are they actually different (and therefore need conversion)?
	capoRootVolume.SizeGiB = mapoRootVolume.Size
	capoRootVolume.Type = mapoRootVolume.VolumeType
	if mapoRootVolume.Zone != "" {
		name := capov1.VolumeAZName(mapoRootVolume.Zone)
		capoRootVolume.AvailabilityZone = &capov1.VolumeAvailabilityZone{
			From: "Name",
			Name: &name,
		}
	}

	// We intentionally ignore the DeprecatedSourceType, DeprecatedDeviceType fields since they're
	// now ignored by MAPO itself and they have no equivalent in CAPO
	// TODO(stephenfin): Do we need to handle the deprecated SourceUUID field?

	return capoRootVolume
}

func convertMAPOSecurityGroupsToCAPO(mapoSecurityGroups []mapiv1alpha1.SecurityGroupParam) []capov1.SecurityGroupParam {
	capoSecurityGroups := []capov1.SecurityGroupParam{}

	splitTags := func(tags string) []capov1.NeutronTag {
		if tags == "" {
			return nil
		}

		var ret []capov1.NeutronTag

		for _, tag := range strings.Split(tags, ",") {
			if tag != "" {
				ret = append(ret, capov1.NeutronTag(tag))
			}
		}
		return ret
	}

	for _, mapoSecurityGroup := range mapoSecurityGroups {
		capoSecurityGroup := capov1.SecurityGroupParam{}
		if mapoSecurityGroup.UUID != "" {
			capoSecurityGroup.ID = &mapoSecurityGroup.UUID
		} else { // Filters
			capoSecurityGroup.Filter = &capov1.SecurityGroupFilter{
				Name:        mapoSecurityGroup.Filter.Name,
				Description: mapoSecurityGroup.Filter.Description,
				// TODO(stephenfin): Handle the deprecated TenantID field?
				ProjectID: mapoSecurityGroup.Filter.ProjectID,
				FilterByNeutronTags: capov1.FilterByNeutronTags{
					NotTags:    splitTags(mapoSecurityGroup.Filter.NotTags),
					NotTagsAny: splitTags(mapoSecurityGroup.Filter.NotTagsAny),
					Tags:       splitTags(mapoSecurityGroup.Filter.Tags),
					TagsAny:    splitTags(mapoSecurityGroup.Filter.TagsAny),
				},
			}
		}
		capoSecurityGroups = append(capoSecurityGroups, capoSecurityGroup)
	}

	return capoSecurityGroups
}

func convertMAPOServerGroupToCAPO(mapoServerGroupID, mapoServerGroupName string) *capov1.ServerGroupParam {
	capoServerGroup := &capov1.ServerGroupParam{}
	if mapoServerGroupID != "" {
		capoServerGroup.ID = &mapoServerGroupID
	} else if mapoServerGroupName == "" { // name
		capoServerGroup.Filter = &capov1.ServerGroupFilter{
			Name: &mapoServerGroupName,
		}
	}
	return capoServerGroup
}

func convertMAPOServerMetadataToCAPO(mapoServerMetadata map[string]string) []capov1.ServerMetadata {
	capoServerMetadata := []capov1.ServerMetadata{}
	for k, v := range mapoServerMetadata {
		capoServerMetadata = append(capoServerMetadata, capov1.ServerMetadata{Key: k, Value: v})
	}
	return capoServerMetadata
}

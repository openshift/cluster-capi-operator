package capi2mapi

import (
	"encoding/json"
	"fmt"

	mapiv1 "github.com/openshift/api/machine/v1beta1"

	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"
)

// AWSMachineTemplateSpec stores the details of a Cluster API AWSMachineTemplateSpec.
type AWSMachineTemplateSpec struct {
	Spec *capav1.AWSMachineTemplateSpec
}

// MachineAndAWSMachineTemplate stores the details of a Cluster API Machine and AWSMachineTemplate.
type MachineAndAWSMachineTemplate struct {
	Machine  *capiv1.Machine
	Template *capav1.AWSMachineTemplate
}

// MachineSetAndAWSMachineTemplate stores the details of a Cluster API MachineSet and AWSMachineTemplate.
type MachineSetAndAWSMachineTemplate struct {
	MachineSet *capiv1.MachineSet
	Template   *capav1.AWSMachineTemplate
}

func FromAWSMachineTemplateSpec(mts *capav1.AWSMachineTemplateSpec) AWSMachineTemplateSpec {
	return AWSMachineTemplateSpec{Spec: mts}
}

func FromMachineAndAWSMachineTemplate(m *capiv1.Machine, mts *capav1.AWSMachineTemplate) MachineAndAWSMachineTemplate {
	return MachineAndAWSMachineTemplate{Machine: m, Template: mts}
}

func FromMachineSetAndAWSMachineTemplate(ms *capiv1.MachineSet, mts *capav1.AWSMachineTemplate) MachineSetAndAWSMachineTemplate {
	return MachineSetAndAWSMachineTemplate{MachineSet: ms, Template: mts}
}

func (m AWSMachineTemplateSpec) ToProviderSpec() (*mapiv1.AWSMachineProviderConfig, []string, error) {
	if m.Spec == nil {
		return nil, nil, fmt.Errorf("provided AWSMachineTemplateSpec can not be nil")
	}

	var warnings []string
	var errors []error

	mapaProviderConfig := mapiv1.AWSMachineProviderConfig{}

	// mapiProviderConfig.AMI = convertAWSResourceReferenceToMAPI(m.Spec.Template.Spec.AMI)//  TODO
	mapaProviderConfig.InstanceType = m.Spec.Template.Spec.InstanceType
	mapaProviderConfig.Tags = convertAWSTagsToMAPI(m.Spec.Template.Spec.AdditionalTags)
	mapaProviderConfig.IAMInstanceProfile = &mapiv1.AWSResourceReference{
		ID: &m.Spec.Template.Spec.IAMInstanceProfile,
	}
	mapaProviderConfig.KeyName = m.Spec.Template.Spec.SSHKeyName
	mapaProviderConfig.PublicIP = m.Spec.Template.Spec.PublicIP
	mapaProviderConfig.Placement = mapiv1.Placement{
		// AvailabilityZone: ptr.Deref(m.Spec.Template.Spec.FailureDomain, ""), // TODO
		Tenancy: convertAWSTenancyToMAPI(m.Spec.Template.Spec.Tenancy),
		Region:  "", // TODO: fetch region from cluster object
	}
	mapaProviderConfig.SecurityGroups = convertAWSSecurityGroupstoMAPI(m.Spec.Template.Spec.AdditionalSecurityGroups)
	mapaProviderConfig.Subnet = convertAWSResourceReferenceToMAPI(ptr.Deref(m.Spec.Template.Spec.Subnet, capav1.AWSResourceReference{}))
	mapaProviderConfig.SpotMarketOptions = convertAWSSpotMarketOptionsToMAPI(m.Spec.Template.Spec.SpotMarketOptions)
	mapaProviderConfig.BlockDevices = convertAWSBlockDeviceMappingSpecToMAPI(m.Spec.Template.Spec.RootVolume, m.Spec.Template.Spec.NonRootVolumes)

	if len(errors) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errors)
	}

	return &mapaProviderConfig, warnings, nil
}

func (m MachineAndAWSMachineTemplate) ToMachine() (*mapiv1.Machine, []string, error) {
	if m.Machine == nil || m.Template == nil {
		return nil, nil, fmt.Errorf("provided Machine and AWSMachineTemplate can not be nil")
	}
	var errors []error
	var warnings []string

	mapaSpec, warn, err := FromAWSMachineTemplateSpec(&m.Template.Spec).ToProviderSpec()
	if err != nil {
		errors = append(errors, err)
	}
	warnings = append(warnings, warn...)

	mapiMachine, warn, err := FromMachineToMachine(m.Machine)
	if err != nil {
		errors = append(errors, err)
	}
	warnings = append(warnings, warn...)

	awsRawExt, err := RawExtensionFromProviderSpec(mapaSpec)
	if err != nil {
		errors = append(errors, err)
	}

	mapiMachine.Spec.ProviderSpec.Value = awsRawExt

	if len(errors) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errors)
	}

	return mapiMachine, warnings, nil
}

func (m MachineSetAndAWSMachineTemplate) ToMachineSet() (*mapiv1.MachineSet, []string, error) {
	if m.MachineSet == nil || m.Template == nil {
		return nil, nil, fmt.Errorf("Machine and AWSMachineTemplate can not be nil")
	}

	var errors []error
	var warnings []string

	mapaSpec, warn, err := FromAWSMachineTemplateSpec(&m.Template.Spec).ToProviderSpec()
	if err != nil {
		errors = append(errors, err)
	}
	warnings = append(warnings, warn...)

	mapiMachineSet, warn, err := FromMachineSetToMachineSet(m.MachineSet)
	if err != nil {
		errors = append(errors, err)
	}
	warnings = append(warnings, warn...)

	awsRawExt, err := RawExtensionFromProviderSpec(mapaSpec)
	if err != nil {
		errors = append(errors, err)
	}

	mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value = awsRawExt

	if len(errors) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errors)
	}

	return mapiMachineSet, warnings, nil
}

//////// Conversion helpers

// RawExtensionFromProviderSpec marshals the machine provider spec.
func RawExtensionFromProviderSpec(spec *mapiv1.AWSMachineProviderConfig) (*runtime.RawExtension, error) {
	if spec == nil {
		return &runtime.RawExtension{}, nil
	}

	var rawBytes []byte
	var err error
	if rawBytes, err = json.Marshal(spec); err != nil {
		return nil, fmt.Errorf("error marshalling providerSpec: %v", err)
	}

	return &runtime.RawExtension{
		Raw: rawBytes,
	}, nil
}

func convertAWSResourceReferenceToMAPI(mapiReference capav1.AWSResourceReference) mapiv1.AWSResourceReference {
	return mapiv1.AWSResourceReference{
		ID: mapiReference.ID,
		// ARN:     mapiReference.ARN, TODO
		Filters: convertAWSFiltersToMAPI(mapiReference.Filters),
	}
}

func convertAWSFiltersToMAPI(capiFilters []capav1.Filter) []mapiv1.Filter {
	mapiFilters := []mapiv1.Filter{}
	for _, filter := range capiFilters {
		mapiFilters = append(mapiFilters, mapiv1.Filter{
			Name:   filter.Name,
			Values: filter.Values,
		})
	}
	return mapiFilters
}

func convertAWSTagsToMAPI(capiTags capav1.Tags) []mapiv1.TagSpecification {
	mapiTags := []mapiv1.TagSpecification{}
	for key, value := range capiTags {
		mapiTags = append(mapiTags, mapiv1.TagSpecification{
			Name:  key,
			Value: value,
		})
	}
	return mapiTags
}

func convertAWSSecurityGroupstoMAPI(sgs []capav1.AWSResourceReference) []mapiv1.AWSResourceReference {
	mapiSGs := []mapiv1.AWSResourceReference{}
	for _, sg := range sgs {
		mapiSGs = append(mapiSGs, convertAWSResourceReferenceToMAPI(sg))
	}
	return mapiSGs
}

func convertAWSSpotMarketOptionsToMAPI(capiSpotMarketOptions *capav1.SpotMarketOptions) *mapiv1.SpotMarketOptions {
	if capiSpotMarketOptions == nil {
		return nil
	}
	return &mapiv1.SpotMarketOptions{
		MaxPrice: capiSpotMarketOptions.MaxPrice,
	}
}

func convertAWSTenancyToMAPI(capiTenancy string) mapiv1.InstanceTenancy {
	switch capiTenancy {
	case "default":
		return mapiv1.DefaultTenancy
	case "dedicated":
		return mapiv1.DedicatedTenancy
	default:
		return mapiv1.HostTenancy
	}
}

func convertAWSBlockDeviceMappingSpecToMAPI(rootVolume *capav1.Volume, nonRootVolumes []capav1.Volume) []mapiv1.BlockDeviceMappingSpec {
	blockDeviceMapping := []mapiv1.BlockDeviceMappingSpec{}
	if rootVolume == nil {
		return blockDeviceMapping
	}

	blockDeviceMapping = append(blockDeviceMapping, mapiv1.BlockDeviceMappingSpec{
		EBS: &mapiv1.EBSBlockDeviceSpec{
			VolumeSize: &rootVolume.Size,
			// VolumeType: &rootVolume.Type, TODO
			Iops:      &rootVolume.IOPS,
			Encrypted: rootVolume.Encrypted,
			KMSKey:    convertKMSKeyToMAPI(rootVolume.EncryptionKey),
		},
	})

	for _, volume := range nonRootVolumes {
		blockDeviceMapping = append(blockDeviceMapping, mapiv1.BlockDeviceMappingSpec{
			DeviceName: &volume.DeviceName,
			EBS: &mapiv1.EBSBlockDeviceSpec{
				VolumeSize: &volume.Size,
				// VolumeType: &volume.Type, TODO
				Iops: &volume.IOPS,
				// Encrypted: &volume.Encrypted, // TODO
				KMSKey: convertKMSKeyToMAPI(volume.EncryptionKey),
			},
		})
	}

	return blockDeviceMapping
}

func convertKMSKeyToMAPI(kmsKey string) mapiv1.AWSResourceReference {
	return mapiv1.AWSResourceReference{
		ID: &kmsKey,
	}
}

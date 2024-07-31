package capi2mapi

import (
	"encoding/json"
	"fmt"

	mapiv1 "github.com/openshift/api/machine/v1beta1"
	"gopkg.in/yaml.v2"

	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"k8s.io/utils/ptr"

	"github.com/openshift/cluster-capi-operator/pkg/util"
)

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

func FromMachineAndAWSMachineTemplate(m *capiv1.Machine, mts *capav1.AWSMachineTemplate) MachineAndAWSMachineTemplate {
	return MachineAndAWSMachineTemplate{Machine: m, Template: mts}
}

func FromMachineSetAndAWSMachineTemplate(ms *capiv1.MachineSet, mts *capav1.AWSMachineTemplate) MachineSetAndAWSMachineTemplate {
	return MachineSetAndAWSMachineTemplate{MachineSet: ms, Template: mts}
}

func (m MachineAndAWSMachineTemplate) ToProviderSpec() (*mapiv1.AWSMachineProviderConfig, []string, error) {
	if m.Machine == nil || m.Template == nil {
		return nil, nil, fmt.Errorf("provided Machine and AWSMachineTemplate can not be nil")
	}

	var warnings []string
	var errors []error

	// Restore logic
	annotationVal, ok := m.Machine.ObjectMeta.Annotations[util.MAPIV1Beta1ConversionDataAnnotationKey]
	if !ok {
		return nil, nil, fmt.Errorf("unable to find %q annotation from the source Object", util.MAPIV1Beta1ConversionDataAnnotationKey)
	}
	uObj, err := util.GetRestoreObjectFromAnnotationValue(annotationVal)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to get %q annotation from the source Object: %w", util.MAPIV1Beta1ConversionDataAnnotationKey, err)
	}

	restoredMAPIMachine := &mapiv1.Machine{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(uObj.Object, &restoredMAPIMachine); err != nil {
		return nil, nil, fmt.Errorf("unable to decode %q annotation from the source Object: %w", util.MAPIV1Beta1ConversionDataAnnotationKey, err)
	}
	restoredAWSProviderSpec, err := AWSProviderSpecFromRawExtension(restoredMAPIMachine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to decode providerSpec from restored Object: %w", err)
	}

	mapaProviderConfig := mapiv1.AWSMachineProviderConfig{}

	mapaTenancy, err := convertAWSTenancyToMAPI(m.Template.Spec.Template.Spec.Tenancy)
	if err != nil {
		errors = append(errors, err)
	}
	// The use of ARN and Filters to reference AMIs was present
	// in CAPA but has been deprecated and then removed
	// ref: https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3257
	mapaProviderConfig.AMI.ID = m.Template.Spec.Template.Spec.AMI.ID

	mapaProviderConfig.TypeMeta = metav1.TypeMeta{
		Kind: "AWSMachineProviderConfig",
		// TODO: this in the original machineSets is sometimes ""awsproviderconfig.openshift.io/v1beta1" some other times "machine.openshift.io/v1beta1"
		// is it fine to always set it to one?
		// APIVersion: "awsproviderconfig.openshift.io/v1beta1",
		APIVersion: restoredAWSProviderSpec.TypeMeta.APIVersion, // From restore Object.
	}
	mapaProviderConfig.InstanceType = m.Template.Spec.Template.Spec.InstanceType
	mapaProviderConfig.Tags = convertAWSTagsToMAPI(m.Template.Spec.Template.Spec.AdditionalTags)
	mapaProviderConfig.IAMInstanceProfile = &mapiv1.AWSResourceReference{
		ID: &m.Template.Spec.Template.Spec.IAMInstanceProfile,
	}
	mapaProviderConfig.KeyName = m.Template.Spec.Template.Spec.SSHKeyName
	mapaProviderConfig.PublicIP = m.Template.Spec.Template.Spec.PublicIP
	mapaProviderConfig.Placement = mapiv1.Placement{
		AvailabilityZone: ptr.Deref(m.Machine.Spec.FailureDomain, ""),
		Tenancy:          mapaTenancy,
		Region:           restoredAWSProviderSpec.Placement.Region, // From restored Object.
	}
	mapaProviderConfig.SecurityGroups = convertAWSSecurityGroupstoMAPI(m.Template.Spec.Template.Spec.AdditionalSecurityGroups)
	mapaProviderConfig.Subnet = convertAWSResourceReferenceToMAPI(ptr.Deref(m.Template.Spec.Template.Spec.Subnet, capav1.AWSResourceReference{}))
	mapaProviderConfig.SpotMarketOptions = convertAWSSpotMarketOptionsToMAPI(m.Template.Spec.Template.Spec.SpotMarketOptions)
	mapaProviderConfig.BlockDevices = convertAWSBlockDeviceMappingSpecToMAPI(m.Template.Spec.Template.Spec.RootVolume, m.Template.Spec.Template.Spec.NonRootVolumes)

	metadataServiceOpts, warnings, err := convertAWSMetadataOptionsToMAPI(m.Template.Spec.Template.Spec.InstanceMetadataOptions)
	if err != nil {
		errors = append(errors, err)
	}
	mapaProviderConfig.MetadataServiceOptions = metadataServiceOpts
	mapaProviderConfig.UserDataSecret = &corev1.LocalObjectReference{
		Name: ptr.Deref(m.Machine.Spec.Bootstrap.DataSecretName, "worker-user-data"),
	}

	// TODO: lossy: needs to be restored from hash.
	// mapaProviderConfig.BlockDevices.EBS.KMSKey
	mapaProviderConfig.CredentialsSecret = restoredAWSProviderSpec.CredentialsSecret // From restored Object.

	if len(errors) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errors)
	}

	return &mapaProviderConfig, warnings, nil
}

func (m MachineAndAWSMachineTemplate) ToMachine() (mapiv1.Machine, []string, error) {
	if m.Machine == nil || m.Template == nil {
		return mapiv1.Machine{}, nil, fmt.Errorf("provided Machine and AWSMachineTemplate can not be nil")
	}
	var errors []error
	var warnings []string

	mapaSpec, warn, err := FromMachineAndAWSMachineTemplate(m.Machine, m.Template).ToProviderSpec()
	if err != nil {
		errors = append(errors, err)
	}
	warnings = append(warnings, warn...)

	mapiMachine, warn, err := fromMachineToMachine(m.Machine)
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
		return mapiv1.Machine{}, warnings, utilerrors.NewAggregate(errors)
	}

	return mapiMachine, warnings, nil
}

func (m MachineSetAndAWSMachineTemplate) ToMachineSet() (mapiv1.MachineSet, []string, error) {
	if m.MachineSet == nil || m.Template == nil {
		return mapiv1.MachineSet{}, nil, fmt.Errorf("Machine and AWSMachineTemplate can not be nil")
	}

	var errors []error
	var warnings []string

	mapaSpec, warn, err := FromMachineAndAWSMachineTemplate(
		&capiv1.Machine{
			Spec: m.MachineSet.Spec.Template.Spec,
			ObjectMeta: metav1.ObjectMeta{
				Annotations: m.MachineSet.ObjectMeta.Annotations,
			},
		}, m.Template).ToProviderSpec()
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
		return mapiv1.MachineSet{}, warnings, utilerrors.NewAggregate(errors)
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

// AWSProviderSpecFromRawExtension unmarshals a raw extension into an AWSMachineProviderSpec type
func AWSProviderSpecFromRawExtension(rawExtension *runtime.RawExtension) (mapiv1.AWSMachineProviderConfig, error) {
	if rawExtension == nil {
		return mapiv1.AWSMachineProviderConfig{}, nil
	}

	spec := mapiv1.AWSMachineProviderConfig{}
	if err := yaml.Unmarshal(rawExtension.Raw, &spec); err != nil {
		return mapiv1.AWSMachineProviderConfig{}, fmt.Errorf("error unmarshalling providerSpec: %v", err)
	}

	return spec, nil
}

func convertAWSMetadataOptionsToMAPI(capiMetadataOpts *capav1.InstanceMetadataOptions) (mapiv1.MetadataServiceOptions, []string, error) {
	if capiMetadataOpts == nil {
		return mapiv1.MetadataServiceOptions{}, nil, nil
	}
	var errors []error
	var warnings []string

	var auth mapiv1.MetadataServiceAuthentication

	switch capiMetadataOpts.HTTPTokens {
	case "":
		//
	case capav1.HTTPTokensStateOptional:
		auth = mapiv1.MetadataServiceAuthenticationOptional
	case capav1.HTTPTokensStateRequired:
		auth = mapiv1.MetadataServiceAuthenticationRequired
	default:
		errors = append(errors, fmt.Errorf("HTTPTokens State %q is not supported by MAPI", capiMetadataOpts.HTTPTokens))
	}

	// TODO: lossy: These fields are not present in MAPI, so they will be lost in CAPI -> MAPI conversion.
	// opts.HTTPEndpoint
	// opts.HTTPPutResponseHopLimit
	// opts.InstanceMetadataTags

	metadataOpts := mapiv1.MetadataServiceOptions{
		Authentication: auth,
	}

	if len(errors) > 0 {
		return mapiv1.MetadataServiceOptions{}, warnings, utilerrors.NewAggregate(errors)
	}

	return metadataOpts, warnings, nil
}

func convertAWSResourceReferenceToMAPI(capiReference capav1.AWSResourceReference) mapiv1.AWSResourceReference {
	return mapiv1.AWSResourceReference{
		ID:      capiReference.ID,
		Filters: convertAWSFiltersToMAPI(capiReference.Filters),
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

func convertAWSTenancyToMAPI(capiTenancy string) (mapiv1.InstanceTenancy, error) {
	switch capiTenancy {
	case "default":
		return mapiv1.DefaultTenancy, nil
	case "dedicated":
		return mapiv1.DedicatedTenancy, nil
	case "host":
		return mapiv1.HostTenancy, nil
	case "":
		return "", nil
	default:
		return "", fmt.Errorf("unable to convert unsupported CAPA Tenancy %q to MAPI", capiTenancy)
	}
}

func convertAWSBlockDeviceMappingSpecToMAPI(rootVolume *capav1.Volume, nonRootVolumes []capav1.Volume) []mapiv1.BlockDeviceMappingSpec {
	blockDeviceMapping := []mapiv1.BlockDeviceMappingSpec{}
	if rootVolume == nil {
		return blockDeviceMapping
	}

	blockDeviceMapping = append(blockDeviceMapping, mapiv1.BlockDeviceMappingSpec{
		EBS: &mapiv1.EBSBlockDeviceSpec{
			VolumeSize: ptr.To(rootVolume.Size),
			VolumeType: ptr.To(string(rootVolume.Type)),
			Iops:       ptr.To(rootVolume.IOPS),
			Encrypted:  rootVolume.Encrypted,
			KMSKey:     convertKMSKeyToMAPI(rootVolume.EncryptionKey),
		},
	})

	for _, volume := range nonRootVolumes {
		blockDeviceMapping = append(blockDeviceMapping, mapiv1.BlockDeviceMappingSpec{
			DeviceName: &volume.DeviceName,
			EBS: &mapiv1.EBSBlockDeviceSpec{
				VolumeSize: ptr.To(volume.Size),
				VolumeType: ptr.To(string(rootVolume.Type)),
				Iops:       ptr.To(volume.IOPS),
				Encrypted:  volume.Encrypted,
				KMSKey:     convertKMSKeyToMAPI(volume.EncryptionKey),
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

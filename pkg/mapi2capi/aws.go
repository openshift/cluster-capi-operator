package mapi2capi

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"
)

// AWSProviderSpecAndInfra stores the details of a Machine API AWSProviderSpec and Infra.
type AWSProviderSpecAndInfra struct {
	Spec           *mapiv1.AWSMachineProviderConfig
	Infrastructure *configv1.Infrastructure
}

// AWSMachineAndInfra stores the details of a Machine API AWSMachine and Infra.
type AWSMachineAndInfra struct {
	Machine        *mapiv1.Machine
	Infrastructure *configv1.Infrastructure
}

// AWSMachineSetAndInfra stores the details of a Machine API AWSMachine and Infra.
type AWSMachineSetAndInfra struct {
	MachineSet     *mapiv1.MachineSet
	Infrastructure *configv1.Infrastructure
}

// FromAWSProviderSpecAndInfra wraps a Machine API AWSMachineProviderConfig into a mapi2capi AWSProviderSpec.
func FromAWSProviderSpecAndInfra(s *mapiv1.AWSMachineProviderConfig, i *configv1.Infrastructure) AWSProviderSpecAndInfra {
	return AWSProviderSpecAndInfra{Spec: s, Infrastructure: i}
}

func FromAWSMachineAndInfra(m *mapiv1.Machine, i *configv1.Infrastructure) AWSMachineAndInfra {
	return AWSMachineAndInfra{Machine: m, Infrastructure: i}
}

func FromAWSMachineSetAndInfra(m *mapiv1.MachineSet, i *configv1.Infrastructure) AWSMachineSetAndInfra {
	return AWSMachineSetAndInfra{MachineSet: m, Infrastructure: i}
}

func (m AWSMachineAndInfra) ToMachineAndMachineTemplate() (capiv1.Machine, capav1.AWSMachineTemplate, []string, error) {
	var errs []error
	var warnings []string

	awsProviderConfig, err := AWSProviderSpecFromRawExtension(m.Machine.Spec.ProviderSpec.Value)
	if err != nil {
		errs = append(errs, err)
	}

	capaSpec, warn, err := FromAWSProviderSpecAndInfra(&awsProviderConfig, m.Infrastructure).ToMachineTemplateSpec()
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	capaMachineTemplate, warn, err := awsMachineTemplateSpecToAWSMachineTemplate(capaSpec, nil, m.Machine.Name, capiNamespace)
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	capiMachine, warn, err := fromMachineToMachine(m.Machine)
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	// Plug into Core CAPI Machine fields that come from the MAPI ProviderConfig which belong here instead of the CAPI AWSMachineTemplate.
	if awsProviderConfig.Placement.AvailabilityZone != "" {
		capiMachine.Spec.FailureDomain = ptr.To(awsProviderConfig.Placement.AvailabilityZone)
	}
	if awsProviderConfig.UserDataSecret != nil && awsProviderConfig.UserDataSecret.Name != "" {
		capiMachine.Spec.Bootstrap = capiv1.Bootstrap{
			DataSecretName: &awsProviderConfig.UserDataSecret.Name,
		}
	}
	if m.Infrastructure == nil || m.Infrastructure.Status.InfrastructureName == "" {
		// Throw error
		errs = append(errs, fmt.Errorf("infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachine.Spec.ClusterName = m.Infrastructure.Status.InfrastructureName
	}

	if len(errs) > 0 {
		return capiv1.Machine{}, capav1.AWSMachineTemplate{}, warnings, utilerrors.NewAggregate(errs)
	}

	return capiMachine, capaMachineTemplate, warnings, nil
}

func (m AWSMachineSetAndInfra) ToMachineSetAndMachineTemplate() (capiv1.MachineSet, capav1.AWSMachineTemplate, []string, error) {
	var errs []error
	var warnings []string

	awsProviderConfig, err := AWSProviderSpecFromRawExtension(m.MachineSet.Spec.Template.Spec.ProviderSpec.Value)
	if err != nil {
		errs = append(errs, err)
	}

	capaSpec, warn, err := FromAWSProviderSpecAndInfra(&awsProviderConfig, m.Infrastructure).ToMachineTemplateSpec()
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	capaMachineTemplate, warn, err := awsMachineTemplateSpecToAWSMachineTemplate(capaSpec, nil, m.MachineSet.Name, capiNamespace)
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	capiMachineSet, warn, err := FromMachineSetToMachineSet(m.MachineSet)
	if err != nil {
		errs = append(errs, err)
	}
	warnings = append(warnings, warn...)

	// Plug into Core CAPI MachineSet fields that come from the MAPI ProviderConfig which belong here instead of the CAPI AWSMachineTemplate.
	if awsProviderConfig.Placement.AvailabilityZone != "" {
		capiMachineSet.Spec.Template.Spec.FailureDomain = ptr.To(awsProviderConfig.Placement.AvailabilityZone)
	}

	if awsProviderConfig.UserDataSecret != nil && awsProviderConfig.UserDataSecret.Name != "" {
		capiMachineSet.Spec.Template.Spec.Bootstrap = capiv1.Bootstrap{
			DataSecretName: &awsProviderConfig.UserDataSecret.Name,
		}
	}
	if m.Infrastructure == nil || m.Infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, fmt.Errorf("infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachineSet.Spec.Template.Spec.ClusterName = m.Infrastructure.Status.InfrastructureName
		capiMachineSet.Spec.ClusterName = m.Infrastructure.Status.InfrastructureName
	}

	if len(errs) > 0 {
		return capiv1.MachineSet{}, capav1.AWSMachineTemplate{}, warnings, utilerrors.NewAggregate(errs)
	}

	return capiMachineSet, capaMachineTemplate, warnings, nil
}

// (AWSProviderSpec).ToMachineTemplateSpec() implements the ProviderSpec conversion interface for the AWS provider.
// It converts AWSProviderSpec to AWSMachineTemplateSpec.
func (p AWSProviderSpecAndInfra) ToMachineTemplateSpec() (capav1.AWSMachineTemplateSpec, []string, error) {
	var errors []error
	var warnings []string

	rootVolume, nonRootVolumes := convertAWSBlockDeviceMappingSpecToCAPI(p.Spec.BlockDevices)

	spec := capav1.AWSMachineTemplateSpec{
		Template: capav1.AWSMachineTemplateResource{
			Spec: capav1.AWSMachineSpec{
				AMI: capav1.AMIReference{
					ID: p.Spec.AMI.ID,
					// The use of ARN and Filters to reference AMIs was present
					// in CAPA but has been deprecated and then removed
					// ref:https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3257
				},
				AdditionalSecurityGroups: convertAWSSecurityGroupstoCAPI(p.Spec.SecurityGroups),
				AdditionalTags:           convertAWSTagsToCAPI(p.Spec.Tags),
				// CloudInit. Not defined as we use ignition in OpenShift.
				IAMInstanceProfile: convertIAMInstanceProfiletoCAPI(p.Spec.IAMInstanceProfile),
				Ignition: &capav1.Ignition{
					Version:     "3.4",                                               // Hardcoded for OpenShift.
					StorageType: capav1.IgnitionStorageTypeOptionUnencryptedUserData, // Hardcoded for OpenShift.
				},
				// ImageLookupBaseOS. Not used in OpenShift.
				// ImageLookupFormat. Not used in OpenShift.
				// ImageLookupOrg. Not used in OpenShift.
				// TODO: what to do with instanceID? in MAPI that's not in AWSMachineProviderConfig but outside of it.
				// Is this propagated down by the CAPA controller automatically from the CAPI Machine
				// InstanceID. This is dynamically populated by the controller.
				InstanceMetadataOptions: convertMetadataServiceOptionstoCAPI(p.Spec.MetadataServiceOptions),
				InstanceType:            p.Spec.InstanceType,
				// NetworkInterfaces. Not used in OpenShift.
				NonRootVolumes:     nonRootVolumes,
				PlacementGroupName: p.Spec.PlacementGroupName,
				// TODO: what to do with providerID? in MAPI that's not in AWSMachineProviderConfig but outside of it.
				// Is this propagated down by the CAPA controller automatically from the CAPI Machine
				// ProviderID. This is dynamically populated by the controller.
				PublicIP:             p.Spec.PublicIP,
				RootVolume:           rootVolume,
				SSHKeyName:           p.Spec.KeyName,
				SpotMarketOptions:    convertAWSSpotMarketOptionsToCAPI(p.Spec.SpotMarketOptions),
				Subnet:               convertAWSResourceReferenceToCAPI(p.Spec.Subnet),
				Tenancy:              string(p.Spec.Placement.Tenancy),
				UncompressedUserData: ptr.To(true),
			},
		},
	}

	if len(errors) > 0 {
		return capav1.AWSMachineTemplateSpec{}, warnings, utilerrors.NewAggregate(errors)
	}

	return spec, warnings, nil
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

func awsMachineTemplateSpecToAWSMachineTemplate(spec capav1.AWSMachineTemplateSpec, status *capav1.AWSMachineTemplateStatus, name string, namespace string) (capav1.AWSMachineTemplate, []string, error) {
	if status == nil {
		status = &capav1.AWSMachineTemplateStatus{}
	}

	var warns []string
	var errs []error

	mt := capav1.AWSMachineTemplate{}
	mt.ObjectMeta = metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
	}
	mt.TypeMeta = metav1.TypeMeta{
		Kind:       awsTemplateKind,
		APIVersion: awsTemplateAPIVersion,
	}
	mt.Name = name
	mt.Namespace = namespace
	mt.Status = *status
	mt.Spec = spec

	if len(errs) > 0 {
		return capav1.AWSMachineTemplate{}, warns, utilerrors.NewAggregate(errs)
	}

	return mt, warns, nil
}

//////// Conversion helpers

func convertAWSTagsToCAPI(mapiTags []mapiv1.TagSpecification) capav1.Tags {
	capiTags := map[string]string{}
	for _, tag := range mapiTags {
		capiTags[tag.Name] = tag.Value
	}

	return capiTags
}

func convertMetadataServiceOptionstoCAPI(metad mapiv1.MetadataServiceOptions) *capav1.InstanceMetadataOptions {
	var httpTokens capav1.HTTPTokensState

	switch metad.Authentication {
	case mapiv1.MetadataServiceAuthenticationOptional:
		httpTokens = capav1.HTTPTokensStateOptional
	case mapiv1.MetadataServiceAuthenticationRequired:
		httpTokens = capav1.HTTPTokensStateRequired
	default:
		return &capav1.InstanceMetadataOptions{}
	}

	capiMetadataOpts := capav1.InstanceMetadataOptions{
		// HTTPEndpoint: not present in MAPI
		// HTTPPutResponseHopLimit: not present in MAPI
		// InstanceMetadataTags: not present in MAPI
		HTTPTokens: httpTokens,
	}

	return &capiMetadataOpts
}

func convertIAMInstanceProfiletoCAPI(mapiIAM *mapiv1.AWSResourceReference) string {
	if mapiIAM == nil || mapiIAM.ID == nil {
		return ""
	}

	return *mapiIAM.ID
}

func convertAWSSpotMarketOptionsToCAPI(mapiSpotMarketOptions *mapiv1.SpotMarketOptions) *capav1.SpotMarketOptions {
	if mapiSpotMarketOptions == nil {
		return nil
	}
	return &capav1.SpotMarketOptions{
		MaxPrice: mapiSpotMarketOptions.MaxPrice,
	}
}

func convertAWSSecurityGroupstoCAPI(sgs []mapiv1.AWSResourceReference) []capav1.AWSResourceReference {
	capiSGs := []capav1.AWSResourceReference{}
	for _, sg := range sgs {
		capiSGs = append(capiSGs, *convertAWSResourceReferenceToCAPI(sg))
	}
	return capiSGs
}

func convertAWSBlockDeviceMappingSpecToCAPI(mapiBlockDeviceMapping []mapiv1.BlockDeviceMappingSpec) (*capav1.Volume, []capav1.Volume) {
	rootVolume := &capav1.Volume{}
	nonRootVolumes := []capav1.Volume{}

	for _, mapping := range mapiBlockDeviceMapping {
		if mapping.DeviceName == nil {
			if mapping.EBS != nil && mapping.EBS.Iops != nil &&
				mapping.EBS.VolumeSize != nil &&
				mapping.EBS.VolumeType != nil &&
				mapping.EBS.Encrypted != nil { // TODO: is this ok?
				rootVolume = &capav1.Volume{
					Size:          *mapping.EBS.VolumeSize,
					Type:          capav1.VolumeType(*mapping.EBS.VolumeType),
					IOPS:          *mapping.EBS.Iops,
					Encrypted:     mapping.EBS.Encrypted,
					EncryptionKey: convertKMSKeyToCAPI(mapping.EBS.KMSKey),
				}
			}
			continue
		}
		if mapping.EBS != nil && mapping.EBS.Iops != nil &&
			mapping.EBS.VolumeSize != nil &&
			mapping.EBS.VolumeType != nil &&
			mapping.EBS.Encrypted != nil { // TODO: is this ok?
			nonRootVolumes = append(nonRootVolumes, capav1.Volume{
				DeviceName: *mapping.DeviceName,
				Size:       *mapping.EBS.VolumeSize,
				Type:       capav1.VolumeType(*mapping.EBS.VolumeType),
				IOPS:       *mapping.EBS.Iops,
				Encrypted:  mapping.EBS.Encrypted,
				// TODO: lossy: this will result in a lossy conversion KMSKey(ID, ARN) -> EncryptionKey (string).
				EncryptionKey: convertKMSKeyToCAPI(mapping.EBS.KMSKey),
			})
		}
	}

	return rootVolume, nonRootVolumes
}

func convertKMSKeyToCAPI(kmsKey mapiv1.AWSResourceReference) string {
	if kmsKey.ID != nil {
		return *kmsKey.ID
	}

	if kmsKey.ARN != nil {
		return *kmsKey.ARN
	}

	return ""
}

func convertAWSResourceReferenceToCAPI(mapiReference mapiv1.AWSResourceReference) *capav1.AWSResourceReference {
	return &capav1.AWSResourceReference{
		ID:      mapiReference.ID,
		Filters: convertAWSFiltersToCAPI(mapiReference.Filters),
	}
}

func convertAWSFiltersToCAPI(mapiFilters []mapiv1.Filter) []capav1.Filter {
	capiFilters := []capav1.Filter{}
	for _, filter := range mapiFilters {
		capiFilters = append(capiFilters, capav1.Filter{
			Name:   filter.Name,
			Values: filter.Values,
		})
	}
	return capiFilters
}

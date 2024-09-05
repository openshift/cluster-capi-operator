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
	"errors"
	"fmt"
	"regexp"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/yaml"
)

var (
	errInfrastructureInfrastructureNameCannotBeNil  = errors.New("infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty")
	errUnableToFindAMIReference                     = errors.New("unable to find a valid AMI resource reference")
	errUnableToConvertUnsupportedAMIFilterReference = errors.New("unable to convert AMI Filters reference. Not supported in CAPI")
	errUnableToConvertUnsupportedAMIARNReference    = errors.New("unable to convert AMI ARN reference. Not supported in CAPI")
	errUnableToFindInstanceID                       = errors.New("unable to find InstanceID in ProviderID")
	errUnableToConvertAWSBlockDeviceMapping         = errors.New("unable to convert AWSBlockDeviceMapping, unsupported NonEBS volume mapping")
)

// awsProviderSpecAndInfra stores the details of a Machine API AWSProviderSpec and Infra.
type awsProviderSpecAndInfra struct {
	Spec           *mapiv1.AWSMachineProviderConfig
	Infrastructure *configv1.Infrastructure
}

// awsMachineAndInfra stores the details of a Machine API AWSMachine and Infra.
type awsMachineAndInfra struct {
	Machine        *mapiv1.Machine
	Infrastructure *configv1.Infrastructure
}

// awsMachineSetAndInfra stores the details of a Machine API AWSMachine and Infra.
type awsMachineSetAndInfra struct {
	MachineSet     *mapiv1.MachineSet
	Infrastructure *configv1.Infrastructure
}

// FromAWSProviderSpecAndInfra wraps a Machine API AWSMachineProviderConfig into a mapi2capi AWSProviderSpec.
func FromAWSProviderSpecAndInfra(s *mapiv1.AWSMachineProviderConfig, i *configv1.Infrastructure) awsProviderSpecAndInfra {
	return awsProviderSpecAndInfra{Spec: s, Infrastructure: i}
}

// FromAWSMachineAndInfra wraps a Machine API Machine for AWS and the OCP Infrastructure object into a mapi2capi AWSProviderSpec.
func FromAWSMachineAndInfra(m *mapiv1.Machine, i *configv1.Infrastructure) awsMachineAndInfra {
	return awsMachineAndInfra{Machine: m, Infrastructure: i}
}

// FromAWSMachineSetAndInfra wraps a Machine API MachineSet for AWS and the OCP Infrastructure object into a mapi2capi AWSProviderSpec.
func FromAWSMachineSetAndInfra(m *mapiv1.MachineSet, i *configv1.Infrastructure) awsMachineSetAndInfra {
	return awsMachineSetAndInfra{MachineSet: m, Infrastructure: i}
}

// ToMachineAndMachineTemplate converts a mapi2capi AWSMachineAndInfra into a CAPI Machine and CAPA AWSMachineTemplate.
func (m awsMachineAndInfra) ToMachineAndMachineTemplate() (*capiv1.Machine, *capav1.AWSMachineTemplate, []string, error) {
	var (
		errs     []error
		warnings []string
	)

	awsProviderConfig, err := AWSProviderSpecFromRawExtension(m.Machine.Spec.ProviderSpec.Value)
	if err != nil {
		errs = append(errs, err)
	}

	capaSpec, warn, err := FromAWSProviderSpecAndInfra(&awsProviderConfig, m.Infrastructure).ToMachineTemplateSpec()
	if err != nil {
		errs = append(errs, err)
	}

	warnings = append(warnings, warn...)

	capaMachineTemplate := awsMachineTemplateSpecToAWSMachineTemplate(capaSpec, nil, m.Machine.Name, capiNamespace)

	capiMachine, err := fromMAPIMachineToCAPIMachine(m.Machine)
	if err != nil {
		errs = append(errs, err)
	}

	// Extract and plug InstanceID.
	if capiMachine.Spec.ProviderID != nil { // TODO: question: do we want to error if the ProviderID is not present?
		instanceID := instanceIDFromProviderID(*capiMachine.Spec.ProviderID)
		if instanceID == "" {
			errs = append(errs, errUnableToFindInstanceID)
		} else {
			capaMachineTemplate.Spec.Template.Spec.InstanceID = ptr.To(instanceID)
		}
	}

	// Plug into Core CAPI Machine fields that come from the MAPI ProviderConfig which belong here instead of the CAPI AWSMachineTemplate.
	if awsProviderConfig.Placement.AvailabilityZone != "" {
		capiMachine.Spec.FailureDomain = ptr.To(awsProviderConfig.Placement.AvailabilityZone)
	}

	if awsProviderConfig.UserDataSecret != nil && awsProviderConfig.UserDataSecret.Name != "" {
		capiMachine.Spec.Bootstrap = capiv1.Bootstrap{
			DataSecretName: &awsProviderConfig.UserDataSecret.Name,
		}
	}

	// Popluate the CAPI Machine ClusterName from the OCP Infrastructure object.
	if m.Infrastructure == nil || m.Infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, errInfrastructureInfrastructureNameCannotBeNil)
	} else {
		capiMachine.Spec.ClusterName = m.Infrastructure.Status.InfrastructureName
	}

	if len(errs) > 0 {
		return nil, nil, warnings, utilerrors.NewAggregate(errs)
	}

	return capiMachine, capaMachineTemplate, warnings, nil
}

// ToMachineSetAndMachineTemplate converts a mapi2capi AWSMachineSetAndInfra into a CAPI MachineSet and CAPA AWSMachineTemplate.
func (m awsMachineSetAndInfra) ToMachineSetAndMachineTemplate() (*capiv1.MachineSet, *capav1.AWSMachineTemplate, []string, error) {
	var (
		errs     []error
		warnings []string
	)

	awsProviderConfig, err := AWSProviderSpecFromRawExtension(m.MachineSet.Spec.Template.Spec.ProviderSpec.Value)
	if err != nil {
		errs = append(errs, err)
	}

	capaSpec, warn, err := FromAWSProviderSpecAndInfra(&awsProviderConfig, m.Infrastructure).ToMachineTemplateSpec()
	if err != nil {
		errs = append(errs, err)
	}

	warnings = append(warnings, warn...)

	capaMachineTemplate := awsMachineTemplateSpecToAWSMachineTemplate(capaSpec, nil, m.MachineSet.Name, capiNamespace)

	capiMachineSet := fromMachineSetToMachineSet(m.MachineSet)

	// Extract and plug InstanceID.
	if capiMachineSet.Spec.Template.Spec.ProviderID != nil { // TODO: question: do we want to error if the ProviderID is not present?
		instanceID := instanceIDFromProviderID(*capiMachineSet.Spec.Template.Spec.ProviderID)
		if instanceID == "" {
			errs = append(errs, errUnableToFindInstanceID)
		} else {
			capaMachineTemplate.Spec.Template.Spec.InstanceID = ptr.To(instanceID)
		}
	}

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
		errs = append(errs, errInfrastructureInfrastructureNameCannotBeNil)
	} else {
		capiMachineSet.Spec.Template.Spec.ClusterName = m.Infrastructure.Status.InfrastructureName
		capiMachineSet.Spec.ClusterName = m.Infrastructure.Status.InfrastructureName
	}

	if len(errs) > 0 {
		return nil, nil, warnings, utilerrors.NewAggregate(errs)
	}

	return capiMachineSet, capaMachineTemplate, warnings, nil
}

// ToMachineTemplateSpec implements the ProviderSpec conversion interface for the AWS provider,
// it converts AWSProviderSpec to AWSMachineTemplateSpec.
//
//nolint:funlen
func (p awsProviderSpecAndInfra) ToMachineTemplateSpec() (capav1.AWSMachineTemplateSpec, []string, error) {
	var (
		errs     []error
		warnings []string
	)

	rootVolume, nonRootVolumes, err := convertAWSBlockDeviceMappingSpecToCAPI(p.Spec.BlockDevices)
	if err != nil {
		errs = append(errs, err)
	}

	capiAdditionalSecurityGroups := convertAWSSecurityGroupstoCAPI(p.Spec.SecurityGroups)

	capiAdditionalAWSTags := convertAWSTagsToCAPI(p.Spec.Tags)

	capiIAMInstanceProfile := convertIAMInstanceProfiletoCAPI(p.Spec.IAMInstanceProfile)

	capiMetadataServiceOptions := convertMetadataServiceOptionstoCAPI(p.Spec.MetadataServiceOptions)

	capiSpotMarketOptions := convertAWSSpotMarketOptionsToCAPI(p.Spec.SpotMarketOptions)

	capiAWSResourceReference := convertAWSResourceReferenceToCAPI(p.Spec.Subnet)

	capiAWSAMIReference, err := convertAWSAMIResourceReferenceToCAPI(p.Spec.AMI)
	if err != nil {
		errs = append(errs, err)
	}

	spec := capav1.AWSMachineTemplateSpec{
		Template: capav1.AWSMachineTemplateResource{
			Spec: capav1.AWSMachineSpec{
				AMI:                      capiAWSAMIReference,
				AdditionalSecurityGroups: capiAdditionalSecurityGroups,
				AdditionalTags:           capiAdditionalAWSTags,
				IAMInstanceProfile:       capiIAMInstanceProfile,
				Ignition: &capav1.Ignition{
					Version:     "3.4",                                               // Hardcoded for OpenShift.
					StorageType: capav1.IgnitionStorageTypeOptionUnencryptedUserData, // Hardcoded for OpenShift.
				},

				// CloudInit. Not used in OpenShift (we only use Ignition).
				// ImageLookupBaseOS. Not used in OpenShift.
				// ImageLookupFormat. Not used in OpenShift.
				// ImageLookupOrg. Not used in OpenShift.
				// NetworkInterfaces. Not used in OpenShift.

				InstanceMetadataOptions: capiMetadataServiceOptions,
				InstanceType:            p.Spec.InstanceType,
				NonRootVolumes:          nonRootVolumes,
				PlacementGroupName:      p.Spec.PlacementGroupName,
				// ProviderID. This is populated when this is called in higher level funcs (ToMachine(), ToMachineSet()).
				// InstanceID. This is populated when this is called in higher level funcs (ToMachine(), ToMachineSet()).
				PublicIP:             p.Spec.PublicIP,
				RootVolume:           rootVolume,
				SSHKeyName:           p.Spec.KeyName,
				SpotMarketOptions:    capiSpotMarketOptions,
				Subnet:               capiAWSResourceReference,
				Tenancy:              string(p.Spec.Placement.Tenancy),
				UncompressedUserData: ptr.To(true),
			},
		},
	}

	if len(errs) > 0 {
		return capav1.AWSMachineTemplateSpec{}, warnings, utilerrors.NewAggregate(errs)
	}

	return spec, warnings, nil
}

// AWSProviderSpecFromRawExtension unmarshals a raw extension into an AWSMachineProviderSpec type.
func AWSProviderSpecFromRawExtension(rawExtension *runtime.RawExtension) (mapiv1.AWSMachineProviderConfig, error) {
	if rawExtension == nil {
		return mapiv1.AWSMachineProviderConfig{}, nil
	}

	spec := mapiv1.AWSMachineProviderConfig{}
	if err := yaml.Unmarshal(rawExtension.Raw, &spec); err != nil {
		return mapiv1.AWSMachineProviderConfig{}, fmt.Errorf("error unmarshalling providerSpec: %w", err)
	}

	return spec, nil
}

func awsMachineTemplateSpecToAWSMachineTemplate(spec capav1.AWSMachineTemplateSpec, status *capav1.AWSMachineTemplateStatus, name string, namespace string) *capav1.AWSMachineTemplate {
	if status == nil {
		status = &capav1.AWSMachineTemplateStatus{}
	}

	return &capav1.AWSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec:   spec,
		Status: *status,
	}
}

//////// Conversion helpers

func convertAWSAMIResourceReferenceToCAPI(amiRef mapiv1.AWSResourceReference) (capav1.AMIReference, error) {
	if amiRef.ARN != nil {
		return capav1.AMIReference{}, errUnableToConvertUnsupportedAMIARNReference
	}

	if len(amiRef.Filters) > 0 {
		return capav1.AMIReference{}, errUnableToConvertUnsupportedAMIFilterReference
	}

	if amiRef.ID != nil {
		return capav1.AMIReference{ID: amiRef.ID}, nil
	}

	return capav1.AMIReference{}, errUnableToFindAMIReference
}

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

	capiMetadataOpts := &capav1.InstanceMetadataOptions{
		// HTTPEndpoint: not present in MAPI, fallback to CAPI default.
		// HTTPPutResponseHopLimit: not present in MAPI, fallback to CAPI default.
		// InstanceMetadataTags: not present in MAPI, fallback to CAPI default.
		HTTPTokens: httpTokens,
	}

	return capiMetadataOpts
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
		ref := convertAWSResourceReferenceToCAPI(sg)

		capiSGs = append(capiSGs, *ref)
	}

	return capiSGs
}

func convertAWSBlockDeviceMappingSpecToCAPI(mapiBlockDeviceMapping []mapiv1.BlockDeviceMappingSpec) (*capav1.Volume, []capav1.Volume, error) {
	rootVolume := &capav1.Volume{}
	nonRootVolumes := []capav1.Volume{}

	for _, mapping := range mapiBlockDeviceMapping {
		// TODO: support also non EBS mappings.
		if mapping.EBS == nil {
			return nil, nil, errUnableToConvertAWSBlockDeviceMapping
		}

		capiKMSKey := convertKMSKeyToCAPI(mapping.EBS.KMSKey)

		if mapping.DeviceName == nil {
			if mapping.EBS != nil && mapping.EBS.Iops != nil &&
				mapping.EBS.VolumeSize != nil &&
				mapping.EBS.VolumeType != nil &&
				mapping.EBS.Encrypted != nil {
				rootVolume = &capav1.Volume{
					Size:          *mapping.EBS.VolumeSize,
					Type:          capav1.VolumeType(*mapping.EBS.VolumeType),
					IOPS:          *mapping.EBS.Iops,
					Encrypted:     mapping.EBS.Encrypted,
					EncryptionKey: capiKMSKey,
				}
			}

			continue
		}

		if mapping.EBS != nil && mapping.EBS.Iops != nil &&
			mapping.DeviceName != nil &&
			mapping.EBS.VolumeSize != nil &&
			mapping.EBS.VolumeType != nil &&
			mapping.EBS.Encrypted != nil {
			nonRootVolumes = append(nonRootVolumes, capav1.Volume{
				Size:          *mapping.EBS.VolumeSize,
				Type:          capav1.VolumeType(*mapping.EBS.VolumeType),
				IOPS:          *mapping.EBS.Iops,
				Encrypted:     mapping.EBS.Encrypted,
				EncryptionKey: capiKMSKey,
			})
		}
	}

	return rootVolume, nonRootVolumes, nil
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

// instanceIDFromProviderID extracts the instanceID from the ProviderID.
func instanceIDFromProviderID(s string) string {
	parts := strings.Split(s, "/")
	lastPart := parts[len(parts)-1]

	return regexp.MustCompile(`i-.*$`).FindString(lastPart)
}

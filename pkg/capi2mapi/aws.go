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
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mapiv1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var (
	errCAPIMachineAWSMachineTemplateAWSClusterCannotBeNil = errors.New("provided Machine, AWSMachineTemplate and AWSCluster can not be nil")
	errUnsupportedCAPATenancy                             = errors.New("unable to convert unsupported CAPA Tenancy")
	errUnsupportedHTTPTokensState                         = errors.New("unable to convert unsupported HTTPTokens State")
)

// machineAndAWSMachineTemplateAndAWSCluster stores the details of a Cluster API Machine and AWSMachineTemplate and AWSCluster.
type machineAndAWSMachineTemplateAndAWSCluster struct {
	Machine    *capiv1.Machine
	Template   *capav1.AWSMachineTemplate
	AWSCluster *capav1.AWSCluster
}

// machineSetAndAWSMachineTemplateAndAWSCluster stores the details of a Cluster API MachineSet and AWSMachineTemplate and AWSCluster.
type machineSetAndAWSMachineTemplateAndAWSCluster struct {
	MachineSet *capiv1.MachineSet
	Template   *capav1.AWSMachineTemplate
	AWSCluster *capav1.AWSCluster
}

// FromMachineAndAWSMachineTemplateAndAWSCluster wraps a CAPI Machine and CAPA AWSMachineTemplate and CAPA AWSCluster into a capi2mapi MachineAndAWSMachineTemplateAndAWSCluster.
func FromMachineAndAWSMachineTemplateAndAWSCluster(m *capiv1.Machine, mts *capav1.AWSMachineTemplate, ac *capav1.AWSCluster) machineAndAWSMachineTemplateAndAWSCluster {
	return machineAndAWSMachineTemplateAndAWSCluster{Machine: m, Template: mts, AWSCluster: ac}
}

// FromMachineSetAndAWSMachineTemplateAndAWSCluster wraps a CAPI MachineSet and CAPA AWSMachineTemplate and CAPA AWSCluster into a capi2mapi MachineSetAndAWSMachineTemplateAndAWSCluster.
func FromMachineSetAndAWSMachineTemplateAndAWSCluster(ms *capiv1.MachineSet, mts *capav1.AWSMachineTemplate, ac *capav1.AWSCluster) machineSetAndAWSMachineTemplateAndAWSCluster {
	return machineSetAndAWSMachineTemplateAndAWSCluster{MachineSet: ms, Template: mts, AWSCluster: ac}
}

// ToProviderSpec converts a capi2mapi MachineAndAWSMachineTemplateAndAWSCluster into a MAPI AWSMachineProviderConfig.
//
//nolint:funlen
func (m machineAndAWSMachineTemplateAndAWSCluster) ToProviderSpec() (*mapiv1.AWSMachineProviderConfig, []string, error) {
	if m.Machine == nil || m.Template == nil || m.AWSCluster == nil {
		return nil, nil, errCAPIMachineAWSMachineTemplateAWSClusterCannotBeNil
	}

	var (
		warnings []string
		errors   []error
	)

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
		// In the machineSets both "awsproviderconfig.openshift.io/v1beta1" and "machine.openshift.io/v1beta1" can be found.
		// Here we always settle on one of the two.
		APIVersion: "machine.openshift.io/v1beta1",
	}
	mapaProviderConfig.InstanceType = m.Template.Spec.Template.Spec.InstanceType

	mapiAWSTags := convertAWSTagsToMAPI(m.Template.Spec.Template.Spec.AdditionalTags)

	mapaProviderConfig.Tags = mapiAWSTags
	mapaProviderConfig.IAMInstanceProfile = &mapiv1.AWSResourceReference{
		ID: &m.Template.Spec.Template.Spec.IAMInstanceProfile,
	}
	mapaProviderConfig.KeyName = m.Template.Spec.Template.Spec.SSHKeyName
	mapaProviderConfig.PublicIP = m.Template.Spec.Template.Spec.PublicIP
	mapaProviderConfig.Placement = mapiv1.Placement{
		AvailabilityZone: ptr.Deref(m.Machine.Spec.FailureDomain, ""),
		Tenancy:          mapaTenancy,
		Region:           m.AWSCluster.Spec.Region,
	}

	mapiAWSSecurityGroups := convertAWSSecurityGroupstoMAPI(m.Template.Spec.Template.Spec.AdditionalSecurityGroups)

	mapaProviderConfig.SecurityGroups = mapiAWSSecurityGroups

	mapiAWSResourceReference := convertAWSResourceReferenceToMAPI(ptr.Deref(m.Template.Spec.Template.Spec.Subnet, capav1.AWSResourceReference{}))

	mapaProviderConfig.Subnet = mapiAWSResourceReference

	mapiAWSSpotMarketOptions := convertAWSSpotMarketOptionsToMAPI(m.Template.Spec.Template.Spec.SpotMarketOptions)

	mapaProviderConfig.SpotMarketOptions = mapiAWSSpotMarketOptions

	mapiAWSProviderConfig, warn, err := convertAWSBlockDeviceMappingSpecToMAPI(m.Template.Spec.Template.Spec.RootVolume, m.Template.Spec.Template.Spec.NonRootVolumes)
	if err != nil {
		errors = append(errors, err)
	}

	warnings = append(warnings, warn...)

	mapaProviderConfig.BlockDevices = mapiAWSProviderConfig

	mapiAWSMetadataOptions, warn, err := convertAWSMetadataOptionsToMAPI(m.Template.Spec.Template.Spec.InstanceMetadataOptions)
	if err != nil {
		errors = append(errors, err)
	}

	warnings = append(warnings, warn...)

	mapaProviderConfig.MetadataServiceOptions = mapiAWSMetadataOptions

	mapaProviderConfig.UserDataSecret = &corev1.LocalObjectReference{
		Name: ptr.Deref(m.Machine.Spec.Bootstrap.DataSecretName, "worker-user-data"),
	}

	// TODO: CredentialsSecret conversion.
	// mapaProviderConfig.CredentialsSecret

	if len(errors) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errors)
	}

	return &mapaProviderConfig, warnings, nil
}

// ToMachine converts a capi2mapi MachineAndAWSMachineTemplate into a MAPI Machine.
func (m machineAndAWSMachineTemplateAndAWSCluster) ToMachine() (*mapiv1.Machine, []string, error) {
	if m.Machine == nil || m.Template == nil || m.AWSCluster == nil {
		return nil, nil, errCAPIMachineAWSMachineTemplateAWSClusterCannotBeNil
	}

	var (
		errors   []error
		warnings []string
	)

	mapaSpec, warn, err := FromMachineAndAWSMachineTemplateAndAWSCluster(m.Machine, m.Template, m.AWSCluster).ToProviderSpec()
	if err != nil {
		errors = append(errors, err)
	}

	warnings = append(warnings, warn...)

	mapiMachine := fromMAPIMachineToCAPIMachine(m.Machine)

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

// ToMachineSet converts a capi2mapi MachineAndAWSMachineTemplate into a MAPI MachineSet.
func (m machineSetAndAWSMachineTemplateAndAWSCluster) ToMachineSet() (*mapiv1.MachineSet, []string, error) {
	if m.MachineSet == nil || m.Template == nil || m.AWSCluster == nil {
		return nil, nil, errCAPIMachineAWSMachineTemplateAWSClusterCannotBeNil
	}

	var (
		errors   []error
		warnings []string
	)

	mapaSpec, warn, err := FromMachineAndAWSMachineTemplateAndAWSCluster(
		&capiv1.Machine{
			Spec: m.MachineSet.Spec.Template.Spec,
			ObjectMeta: metav1.ObjectMeta{
				Annotations: m.MachineSet.ObjectMeta.Annotations,
			},
		}, m.Template, m.AWSCluster).ToProviderSpec()
	if err != nil {
		errors = append(errors, err)
	}

	warnings = append(warnings, warn...)

	mapiMachineSet := fromMachineSetToMachineSet(m.MachineSet)

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

// Conversion helpers.

// RawExtensionFromProviderSpec marshals the machine provider spec.
func RawExtensionFromProviderSpec(spec *mapiv1.AWSMachineProviderConfig) (*runtime.RawExtension, error) {
	if spec == nil {
		return &runtime.RawExtension{}, nil
	}

	rawBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("error marshalling providerSpec: %w", err)
	}

	return &runtime.RawExtension{
		Raw: rawBytes,
	}, nil
}

func convertAWSMetadataOptionsToMAPI(capiMetadataOpts *capav1.InstanceMetadataOptions) (mapiv1.MetadataServiceOptions, []string, error) {
	var (
		errors   []error
		warnings []string
	)

	if capiMetadataOpts == nil {
		return mapiv1.MetadataServiceOptions{}, nil, nil
	}

	var auth mapiv1.MetadataServiceAuthentication

	switch capiMetadataOpts.HTTPTokens {
	case "":
		//
	case capav1.HTTPTokensStateOptional:
		auth = mapiv1.MetadataServiceAuthenticationOptional
	case capav1.HTTPTokensStateRequired:
		auth = mapiv1.MetadataServiceAuthenticationRequired
	default:
		errors = append(errors, fmt.Errorf("%w: %q", errUnsupportedHTTPTokensState, capiMetadataOpts.HTTPTokens))
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
	filters := convertAWSFiltersToMAPI(capiReference.Filters)

	return mapiv1.AWSResourceReference{
		ID:      capiReference.ID,
		Filters: filters,
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
		mapiAWSResourceRef := convertAWSResourceReferenceToMAPI(sg)

		mapiSGs = append(mapiSGs, mapiAWSResourceRef)
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
		return "", fmt.Errorf("%w: %q", errUnsupportedCAPATenancy, capiTenancy)
	}
}

func convertAWSBlockDeviceMappingSpecToMAPI(rootVolume *capav1.Volume, nonRootVolumes []capav1.Volume) ([]mapiv1.BlockDeviceMappingSpec, []string, error) {
	var (
		warnings []string
		errors   []error
	)

	blockDeviceMapping := []mapiv1.BlockDeviceMappingSpec{}
	if rootVolume == nil {
		return blockDeviceMapping, nil, nil
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
			DeviceName: ptr.To(volume.DeviceName),
			EBS: &mapiv1.EBSBlockDeviceSpec{
				VolumeSize: ptr.To(volume.Size),
				VolumeType: ptr.To(string(rootVolume.Type)),
				Iops:       ptr.To(volume.IOPS),
				Encrypted:  volume.Encrypted,
				KMSKey:     convertKMSKeyToMAPI(volume.EncryptionKey),
			},
		})
	}

	if len(errors) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errors)
	}

	return blockDeviceMapping, warnings, nil
}

func convertKMSKeyToMAPI(kmsKey string) mapiv1.AWSResourceReference {
	if strings.HasPrefix(kmsKey, "arn:") {
		return mapiv1.AWSResourceReference{
			ARN: &kmsKey,
		}
	}

	return mapiv1.AWSResourceReference{
		ID: &kmsKey,
	}
}

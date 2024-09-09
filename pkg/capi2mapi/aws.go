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
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var (
	errCAPIMachineAWSMachineAWSClusterCannotBeNil            = errors.New("provided Machine, AWSMachine and AWSCluster can not be nil")
	errCAPIMachineSetAWSMachineTemplateAWSClusterCannotBeNil = errors.New("provided MachineSet, AWSMachineTemplate and AWSCluster can not be nil")
)

const (
	errUnsupportedCAPATenancy     = "unable to convert tenancy, unknown value"
	errUnsupportedHTTPTokensState = "unable to convert httpTokens state, unknown value" //nolint:gosec // This is an error message, not a credential
)

// machineAndAWSMachineAndAWSCluster stores the details of a Cluster API Machine and AWSMachine and AWSCluster.
type machineAndAWSMachineAndAWSCluster struct {
	machine    *capiv1.Machine
	awsMachine *capav1.AWSMachine
	awsCluster *capav1.AWSCluster
}

// machineSetAndAWSMachineTemplateAndAWSCluster stores the details of a Cluster API MachineSet and AWSMachineTemplate and AWSCluster.
type machineSetAndAWSMachineTemplateAndAWSCluster struct {
	machineSet *capiv1.MachineSet
	template   *capav1.AWSMachineTemplate
	awsCluster *capav1.AWSCluster
	*machineAndAWSMachineAndAWSCluster
}

// FromMachineAndAWSMachineAndAWSCluster wraps a CAPI Machine and CAPA AWSMachine and CAPA AWSCluster into a capi2mapi MachineAndInfrastructureMachine.
func FromMachineAndAWSMachineAndAWSCluster(m *capiv1.Machine, am *capav1.AWSMachine, ac *capav1.AWSCluster) MachineAndInfrastructureMachine {
	return &machineAndAWSMachineAndAWSCluster{machine: m, awsMachine: am, awsCluster: ac}
}

// FromMachineSetAndAWSMachineTemplateAndAWSCluster wraps a CAPI MachineSet and CAPA AWSMachineTemplate and CAPA AWSCluster into a capi2mapi MachineSetAndAWSMachineTemplateAndAWSCluster.
func FromMachineSetAndAWSMachineTemplateAndAWSCluster(ms *capiv1.MachineSet, mts *capav1.AWSMachineTemplate, ac *capav1.AWSCluster) MachineSetAndMachineTemplate {
	return &machineSetAndAWSMachineTemplateAndAWSCluster{
		machineSet: ms,
		template:   mts,
		awsCluster: ac,
		machineAndAWSMachineAndAWSCluster: &machineAndAWSMachineAndAWSCluster{
			machine: &capiv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      ms.Spec.Template.Labels,
					Annotations: ms.Spec.Template.Annotations,
				},
				Spec: ms.Spec.Template.Spec,
			},
			awsMachine: &capav1.AWSMachine{
				Spec: mts.Spec.Template.Spec,
			},
			awsCluster: ac,
		},
	}
}

// toProviderSpec converts a capi2mapi MachineAndAWSMachineTemplateAndAWSCluster into a MAPI AWSMachineProviderConfig.
//
//nolint:funlen
func (m machineAndAWSMachineAndAWSCluster) toProviderSpec() (*mapiv1.AWSMachineProviderConfig, []string, error) {
	if m.machine == nil || m.awsMachine == nil || m.awsCluster == nil {
		return nil, nil, errCAPIMachineAWSMachineAWSClusterCannotBeNil
	}

	var (
		warnings []string
		errors   field.ErrorList
	)

	fldPath := field.NewPath("spec")

	mapaTenancy, err := convertAWSTenancyToMAPI(fldPath.Child("tenancy"), m.awsMachine.Spec.Tenancy)
	if err != nil {
		errors = append(errors, err)
	}

	mapiAWSMetadataOptions, warn, errs := convertAWSMetadataOptionsToMAPI(fldPath.Child("instanceMetadataOptions"), m.awsMachine.Spec.InstanceMetadataOptions)
	if err != nil {
		errors = append(errors, errs...)
	}

	warnings = append(warnings, warn...)

	mapaProviderConfig := mapiv1.AWSMachineProviderConfig{
		TypeMeta: metav1.TypeMeta{
			Kind: "AWSMachineProviderConfig",
			// In the machineSets both "awsproviderconfig.openshift.io/v1beta1" and "machine.openshift.io/v1beta1" can be found.
			// Here we always settle on one of the two.
			APIVersion: "machine.openshift.io/v1beta1",
		},
		// ObjectMeta - Only present because it's needed to form part of the runtime.RawExtension, not actually used by MAPA.
		AMI: mapiv1.AWSResourceReference{
			// The use of ARN and Filters to reference AMIs was present
			// in CAPA but has been deprecated and then removed
			// ref: https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3257
			ID: m.awsMachine.Spec.AMI.ID,
		},
		InstanceType: m.awsMachine.Spec.InstanceType,
		Tags:         convertAWSTagsToMAPI(m.awsMachine.Spec.AdditionalTags),
		IAMInstanceProfile: &mapiv1.AWSResourceReference{
			ID: &m.awsMachine.Spec.IAMInstanceProfile,
		},
		UserDataSecret: &corev1.LocalObjectReference{
			Name: ptr.Deref(m.machine.Spec.Bootstrap.DataSecretName, "worker-user-data"),
		},
		// CredentialsSecret - TODO(OCPCLOUD-XXXX)
		KeyName: m.awsMachine.Spec.SSHKeyName,
		// DeviceIndex - TODO(OCPCLOUD-XXXX) Not currently supported in CAPA.
		PublicIP: m.awsMachine.Spec.PublicIP,
		// NetworkInterfaceType - TODO(OCPCLOUD-XXXX) Not currently supported in CAPA.
		SecurityGroups: convertAWSSecurityGroupstoMAPI(m.awsMachine.Spec.AdditionalSecurityGroups),
		Subnet:         convertAWSResourceReferenceToMAPI(ptr.Deref(m.awsMachine.Spec.Subnet, capav1.AWSResourceReference{})),
		Placement: mapiv1.Placement{
			AvailabilityZone: ptr.Deref(m.machine.Spec.FailureDomain, ""),
			Tenancy:          mapaTenancy,
			Region:           m.awsCluster.Spec.Region,
		},
		// LoadBalancers - TODO(OCPCLOUD-XXXX) Not supported for workers.
		BlockDevices:            convertAWSBlockDeviceMappingSpecToMAPI(m.awsMachine.Spec.RootVolume, m.awsMachine.Spec.NonRootVolumes),
		SpotMarketOptions:       convertAWSSpotMarketOptionsToMAPI(m.awsMachine.Spec.SpotMarketOptions),
		MetadataServiceOptions:  mapiAWSMetadataOptions,
		PlacementGroupName:      m.awsMachine.Spec.PlacementGroupName,
		PlacementGroupPartition: convertAWSPlacementGroupPartition(m.awsMachine.Spec.PlacementGroupPartition),
		CapacityReservationID:   ptr.Deref(m.awsMachine.Spec.CapacityReservationID, ""),
	}

	// TODO: CredentialsSecret conversion.
	// mapaProviderConfig.CredentialsSecret

	// Below this line are fields not used from the CAPI AWSMachine.

	// ProviderID - Populated at a different level.
	// IntsanceID - Ignore - Is a subset of providerID.
	// Ignition - Ignore - Only has a version field and we force this to a particular value.

	// There are quite a few unsupported fields, so break them out for now.
	errors = append(errors, handleUnsupportedAWSMachineFields(fldPath, m.awsMachine.Spec)...)

	if len(errors) > 0 {
		return nil, warnings, errors.ToAggregate()
	}

	return &mapaProviderConfig, warnings, nil
}

// ToMachine converts a capi2mapi MachineAndAWSMachineTemplate into a MAPI Machine.
func (m machineAndAWSMachineAndAWSCluster) ToMachine() (*mapiv1.Machine, []string, error) {
	if m.machine == nil || m.awsMachine == nil || m.awsCluster == nil {
		return nil, nil, errCAPIMachineAWSMachineAWSClusterCannotBeNil
	}

	var (
		errors   []error
		warnings []string
	)

	mapaSpec, warn, err := m.toProviderSpec()
	if err != nil {
		errors = append(errors, err)
	}

	warnings = append(warnings, warn...)

	mapiMachine, err := fromCAPIMachineToMAPIMachine(m.machine)
	if err != nil {
		errors = append(errors, err)
	}

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
	if m.machineSet == nil || m.template == nil || m.awsCluster == nil || m.machineAndAWSMachineAndAWSCluster == nil {
		return nil, nil, errCAPIMachineSetAWSMachineTemplateAWSClusterCannotBeNil
	}

	var (
		errors   []error
		warnings []string
	)

	// Run the full ToMachine conversion so that we can check for
	// any Machine level conversion errors in the spec translation.
	mapaMachine, warn, err := m.ToMachine()
	if err != nil {
		errors = append(errors, err)
	}

	warnings = append(warnings, warn...)

	mapiMachineSet, err := fromCAPIMachineSetToMAPIMachineSet(m.machineSet)
	if err != nil {
		errors = append(errors, err)
	}

	mapiMachineSet.Spec.Template.Spec.ProviderSpec.Value = mapaMachine.Spec.ProviderSpec.Value

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

func convertAWSMetadataOptionsToMAPI(fldPath *field.Path, capiMetadataOpts *capav1.InstanceMetadataOptions) (mapiv1.MetadataServiceOptions, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	if capiMetadataOpts == nil {
		return mapiv1.MetadataServiceOptions{}, nil, nil
	}

	var auth mapiv1.MetadataServiceAuthentication

	switch capiMetadataOpts.HTTPTokens {
	case "":
		// Defaults to optional on both sides.
	case capav1.HTTPTokensStateOptional:
		auth = mapiv1.MetadataServiceAuthenticationOptional
	case capav1.HTTPTokensStateRequired:
		auth = mapiv1.MetadataServiceAuthenticationRequired
	default:
		errors = append(errors, field.Invalid(fldPath.Child("httpTokens"), capiMetadataOpts.HTTPTokens, errUnsupportedHTTPTokensState))
	}

	if capiMetadataOpts.HTTPEndpoint != "enabled" {
		// This defaults to "enabled" in CAPI and on the AWS side, so if it's not "enabled", the user explicitly chose another option.
		// TODO(OCPCLOUD-XXXX): We should implement this within MAPI to create feature parity.
		errors = append(errors, field.Invalid(fldPath.Child("httpEndpoint"), capiMetadataOpts.HTTPEndpoint, "httpEndpoint values other than \"enabled\" are not supported"))
	}

	if capiMetadataOpts.HTTPPutResponseHopLimit != 1 {
		// This defaults to 1 in CAPI and on the AWS side, so if it's not 1, the user explicitly chose another option.
		// TODO(OCPCLOUD-XXXX): We should implement this within MAPI to create feature parity.
		errors = append(errors, field.Invalid(fldPath.Child("httpPutResponseHopLimit"), capiMetadataOpts.HTTPPutResponseHopLimit, "httpPutResponseHopLimit values other than 1 are not supported"))
	}

	if capiMetadataOpts.InstanceMetadataTags != "disabled" {
		// This defaults to "disabled" in CAPI and on the AWS side, so if it's not "disabled", the user explicitly chose another option.
		// TODO(OCPCLOUD-XXXX): We should implement this within MAPI to create feature parity.
		errors = append(errors, field.Invalid(fldPath.Child("instanceMetadataTags"), capiMetadataOpts.InstanceMetadataTags, "instanceMetadataTags values other than \"disabled\" are not supported"))
	}

	metadataOpts := mapiv1.MetadataServiceOptions{
		Authentication: auth,
	}

	if len(errors) > 0 {
		return mapiv1.MetadataServiceOptions{}, warnings, errors
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

func convertAWSTenancyToMAPI(fldPath *field.Path, capiTenancy string) (mapiv1.InstanceTenancy, *field.Error) {
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
		return "", field.Invalid(fldPath, capiTenancy, errUnsupportedCAPATenancy)
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

	return blockDeviceMapping
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

func convertAWSPlacementGroupPartition(in int64) *int32 {
	if in == 0 {
		return nil
	}

	return ptr.To(int32(in))
}

// handleUnsupportedAWSMachineFields returns an error for every present field in the AWSMachineSpec that
// we are currently, or indefinitely not supporting.
// TODO: These are protected by VAPs so should never actually cause an error here.
func handleUnsupportedAWSMachineFields(fldPath *field.Path, spec capav1.AWSMachineSpec) field.ErrorList {
	errs := field.ErrorList{}

	if spec.AMI.EKSOptimizedLookupType != nil {
		// TODO(OCPCLOUD-XXXX): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("ami", "eksOptimizedLookupType"), spec.AMI.EKSOptimizedLookupType, "eksOptimizedLookupType is not supported"))
	}

	if spec.ImageLookupFormat != "" {
		// TODO(OCPCLOUD-XXXX): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("imageLookupFormat"), spec.ImageLookupFormat, "imageLookupFormat is not supported"))
	}

	if spec.ImageLookupOrg != "" {
		// TODO(OCPCLOUD-XXXX): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("imageLookupOrg"), spec.ImageLookupOrg, "imageLookupOrg is not supported"))
	}

	if spec.ImageLookupBaseOS != "" {
		// TODO(OCPCLOUD-XXXX): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("imageLookupBaseOS"), spec.ImageLookupBaseOS, "imageLookupBaseOS is not supported"))
	}

	if len(spec.SecurityGroupOverrides) > 0 {
		// TODO(OCPCLOUD-XXXX): Needs more investigation, we are converting additional security groups to MAPI SGs, this overrides the built-ins, need to explore at the behavioural level.
		errs = append(errs, field.Invalid(fldPath.Child("securityGroupOverrides"), spec.SecurityGroupOverrides, "securityGroupOverrides are not supported"))
	}

	if len(spec.NetworkInterfaces) > 0 {
		// TODO(OCPCLOUD-XXXX): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("networkInterfaces"), spec.NetworkInterfaces, "networkInterfaces are not supported"))
	}

	if spec.UncompressedUserData != nil {
		// TODO(OCPCLOUD-XXXX): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("uncompressedUserData"), spec.UncompressedUserData, "uncompressedUserData is not supported"))
	}

	if (spec.CloudInit != capav1.CloudInit{}) {
		// TODO(OCPCLOUD-XXXX): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("cloudInit"), spec.CloudInit, "cloudInit is not supported"))
	}

	if spec.PrivateDNSName != nil {
		// TODO(OCPCLOUD-XXXX): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("privateDNSName"), spec.PrivateDNSName, "privateDNSName is not supported"))
	}

	return errs
}

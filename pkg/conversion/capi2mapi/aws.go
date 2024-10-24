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
	"strings"

	mapiv1 "github.com/openshift/api/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
					Labels:      ms.Spec.Template.ObjectMeta.Labels,
					Annotations: ms.Spec.Template.ObjectMeta.Annotations,
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
func (m machineAndAWSMachineAndAWSCluster) toProviderSpec() (*mapiv1.AWSMachineProviderConfig, []string, field.ErrorList) {
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
	if errs != nil {
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
		// UserDataSecret - Populated below.
		// CredentialsSecret - TODO(OCPCLOUD-2713)
		KeyName: m.awsMachine.Spec.SSHKeyName,
		// DeviceIndex - OCPCLOUD-2707: Value must always be zero. No other values are valid in MAPA even though the value is configurable.
		PublicIP:             m.awsMachine.Spec.PublicIP,
		NetworkInterfaceType: mapiv1.AWSENANetworkInterfaceType,                                          // TODO(OCPCLOUD-2708) This is the default value for MAPA, but other values are not configurable in CAPA.
		SecurityGroups:       convertAWSSecurityGroupstoMAPI(m.awsMachine.Spec.AdditionalSecurityGroups), // OCPCLOUD-2712: We need to ensure that this is the correct way to convert the security groups.
		Subnet:               convertAWSResourceReferenceToMAPI(ptr.Deref(m.awsMachine.Spec.Subnet, capav1.AWSResourceReference{})),
		Placement: mapiv1.Placement{
			AvailabilityZone: ptr.Deref(m.machine.Spec.FailureDomain, ""),
			Tenancy:          mapaTenancy,
			Region:           m.awsCluster.Spec.Region,
		},
		// LoadBalancers - TODO(OCPCLOUD-2709) Not supported for workers.
		BlockDevices:            convertAWSBlockDeviceMappingSpecToMAPI(m.awsMachine.Spec.RootVolume, m.awsMachine.Spec.NonRootVolumes),
		SpotMarketOptions:       convertAWSSpotMarketOptionsToMAPI(m.awsMachine.Spec.SpotMarketOptions),
		MetadataServiceOptions:  mapiAWSMetadataOptions,
		PlacementGroupName:      m.awsMachine.Spec.PlacementGroupName,
		PlacementGroupPartition: convertAWSPlacementGroupPartition(m.awsMachine.Spec.PlacementGroupPartition),
		CapacityReservationID:   ptr.Deref(m.awsMachine.Spec.CapacityReservationID, ""),
	}

	userDataSecretName := ptr.Deref(m.machine.Spec.Bootstrap.DataSecretName, "")
	if userDataSecretName != "" {
		mapaProviderConfig.UserDataSecret = &corev1.LocalObjectReference{
			Name: userDataSecretName,
		}
	}

	// Below this line are fields not used from the CAPI AWSMachine.

	// ProviderID - Populated at a different level.
	// IntsanceID - Ignore - Is a subset of providerID.
	// Ignition - Ignore - Only has a version field and we force this to a particular value.

	// There are quite a few unsupported fields, so break them out for now.
	errors = append(errors, handleUnsupportedAWSMachineFields(fldPath, m.awsMachine.Spec)...)

	if len(errors) > 0 {
		return nil, warnings, errors
	}

	return &mapaProviderConfig, warnings, nil
}

// ToMachine converts a capi2mapi MachineAndAWSMachineTemplate into a MAPI Machine.
func (m machineAndAWSMachineAndAWSCluster) ToMachine() (*mapiv1.Machine, []string, error) {
	if m.machine == nil || m.awsMachine == nil || m.awsCluster == nil {
		return nil, nil, errCAPIMachineAWSMachineAWSClusterCannotBeNil
	}

	var (
		errors   field.ErrorList
		warnings []string
	)

	mapaSpec, warn, err := m.toProviderSpec()
	if err != nil {
		errors = append(errors, err...)
	}

	awsRawExt, errRaw := RawExtensionFromProviderSpec(mapaSpec)
	if errRaw != nil {
		return nil, nil, fmt.Errorf("unable to convert AWS providerSpec to raw extension: %w", errRaw)
	}

	warnings = append(warnings, warn...)

	mapiMachine, err := fromCAPIMachineToMAPIMachine(m.machine)
	if err != nil {
		errors = append(errors, err...)
	}

	mapiMachine.Spec.ProviderSpec.Value = awsRawExt

	if len(errors) > 0 {
		return nil, warnings, errors.ToAggregate()
	}

	return mapiMachine, warnings, nil
}

// ToMachineSet converts a capi2mapi MachineAndAWSMachineTemplate into a MAPI MachineSet.
//
//nolint:dupl
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

	mapiMachineSet.Spec.Template.Spec = mapaMachine.Spec

	// Copy the labels and annotations from the Machine to the template.
	mapiMachineSet.Spec.Template.ObjectMeta.Annotations = mapaMachine.ObjectMeta.Annotations
	mapiMachineSet.Spec.Template.ObjectMeta.Labels = mapaMachine.ObjectMeta.Labels

	if len(errors) > 0 {
		return nil, warnings, utilerrors.NewAggregate(errors)
	}

	return mapiMachineSet, warnings, nil
}

// Conversion helpers.

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

	if capiMetadataOpts.HTTPEndpoint != "" && capiMetadataOpts.HTTPEndpoint != capav1.InstanceMetadataEndpointStateEnabled {
		// This defaults to "enabled" in CAPI and on the AWS side, so if it's not "enabled", the user explicitly chose another option.
		// TODO(OCPCLOUD-2710): We should implement this within MAPI to create feature parity.
		errors = append(errors, field.Invalid(fldPath.Child("httpEndpoint"), capiMetadataOpts.HTTPEndpoint, fmt.Sprintf("httpEndpoint values other than %q are not supported", capav1.InstanceMetadataEndpointStateEnabled)))
	}

	if capiMetadataOpts.HTTPPutResponseHopLimit != 0 && capiMetadataOpts.HTTPPutResponseHopLimit != 1 {
		// This defaults to 1 in CAPI and on the AWS side, so if it's not 1, the user explicitly chose another option.
		// TODO(OCPCLOUD-2710): We should implement this within MAPI to create feature parity.
		errors = append(errors, field.Invalid(fldPath.Child("httpPutResponseHopLimit"), capiMetadataOpts.HTTPPutResponseHopLimit, "httpPutResponseHopLimit values other than 1 are not supported"))
	}

	if capiMetadataOpts.InstanceMetadataTags != "" && capiMetadataOpts.InstanceMetadataTags != capav1.InstanceMetadataEndpointStateDisabled {
		// This defaults to "disabled" in CAPI and on the AWS side, so if it's not "disabled", the user explicitly chose another option.
		// TODO(OCPCLOUD-2710): We should implement this within MAPI to create feature parity.
		errors = append(errors, field.Invalid(fldPath.Child("instanceMetadataTags"), capiMetadataOpts.InstanceMetadataTags, fmt.Sprintf("instanceMetadataTags values other than %q are not supported", capav1.InstanceMetadataEndpointStateDisabled)))
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

	if rootVolume != nil && *rootVolume != (capav1.Volume{}) {
		blockDeviceMapping = append(blockDeviceMapping, volumeToBlockDeviceMappingSpec(*rootVolume))
	}

	for _, volume := range nonRootVolumes {
		blockDeviceMapping = append(blockDeviceMapping, volumeToBlockDeviceMappingSpec(volume))
	}

	return blockDeviceMapping
}

func volumeToBlockDeviceMappingSpec(volume capav1.Volume) mapiv1.BlockDeviceMappingSpec {
	bdm := mapiv1.BlockDeviceMappingSpec{
		EBS: &mapiv1.EBSBlockDeviceSpec{
			DeleteOnTermination: ptr.To(true), // This is forced to true for now as CAPI doesn't support changing it.
			VolumeSize:          ptr.To(volume.Size),
			Encrypted:           volume.Encrypted,
			KMSKey:              convertKMSKeyToMAPI(volume.EncryptionKey),
		},
	}

	if volume.DeviceName != "" {
		bdm.DeviceName = ptr.To(volume.DeviceName)
	}

	if volume.Type != "" {
		bdm.EBS.VolumeType = ptr.To(string(volume.Type))
	}

	if volume.IOPS != 0 {
		bdm.EBS.Iops = ptr.To(volume.IOPS)
	}

	return bdm
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
	// We know the value is between 0 and 7 based on API validation. Ignore gosec.
	//nolint:gosec
	return ptr.To(int32(in))
}

// handleUnsupportedAWSMachineFields returns an error for every present field in the AWSMachineSpec that
// we are currently, or indefinitely not supporting.
// TODO: These are protected by VAPs so should never actually cause an error here.
func handleUnsupportedAWSMachineFields(fldPath *field.Path, spec capav1.AWSMachineSpec) field.ErrorList {
	errs := field.ErrorList{}

	if spec.AMI.EKSOptimizedLookupType != nil {
		// TODO(OCPCLOUD-2711): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("ami", "eksOptimizedLookupType"), spec.AMI.EKSOptimizedLookupType, "eksOptimizedLookupType is not supported"))
	}

	if spec.ImageLookupFormat != "" {
		// TODO(OCPCLOUD-2711): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("imageLookupFormat"), spec.ImageLookupFormat, "imageLookupFormat is not supported"))
	}

	if spec.ImageLookupOrg != "" {
		// TODO(OCPCLOUD-2711): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("imageLookupOrg"), spec.ImageLookupOrg, "imageLookupOrg is not supported"))
	}

	if spec.ImageLookupBaseOS != "" {
		// TODO(OCPCLOUD-2711): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("imageLookupBaseOS"), spec.ImageLookupBaseOS, "imageLookupBaseOS is not supported"))
	}

	if len(spec.SecurityGroupOverrides) > 0 {
		// TODO(OCPCLOUD-2712): Needs more investigation, we are converting additional security groups to MAPI SGs, this overrides the built-ins, need to explore at the behavioural level.
		errs = append(errs, field.Invalid(fldPath.Child("securityGroupOverrides"), spec.SecurityGroupOverrides, "securityGroupOverrides are not supported"))
	}

	if len(spec.NetworkInterfaces) > 0 {
		// TODO(OCPCLOUD-2711): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("networkInterfaces"), spec.NetworkInterfaces, "networkInterfaces are not supported"))
	}

	if spec.UncompressedUserData != nil {
		// TODO(OCPCLOUD-2711): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("uncompressedUserData"), spec.UncompressedUserData, "uncompressedUserData is not supported"))
	}

	if (spec.CloudInit != capav1.CloudInit{}) {
		// TODO(OCPCLOUD-2711): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("cloudInit"), spec.CloudInit, "cloudInit is not supported"))
	}

	if spec.PrivateDNSName != nil {
		// TODO(OCPCLOUD-2711): Not required for our use case, add VAP to prevent usage.
		errs = append(errs, field.Invalid(fldPath.Child("privateDNSName"), spec.PrivateDNSName, "privateDNSName is not supported"))
	}

	if spec.Ignition != nil {
		if spec.Ignition.Proxy != nil {
			// TODO(OCPCLOUD-2711): Ignition proxy is not configurable in MAPI. Not required for our use case, add VAP to prevent usage.
			errs = append(errs, field.Invalid(fldPath.Child("ignition", "proxy"), spec.Ignition.Proxy, "ignition proxy is not supported"))
		}

		if spec.Ignition.TLS != nil {
			// TODO(OCPCLOUD-2711): Ignition TLS is not configurable in MAPI. Not required for our use case, add VAP to prevent usage.
			errs = append(errs, field.Invalid(fldPath.Child("ignition", "tls"), spec.Ignition.TLS, "ignition tls is not supported"))
		}
	}

	return errs
}

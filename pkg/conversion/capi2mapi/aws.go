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
package capi2mapi

import (
	"errors"
	"fmt"
	"math"
	"strings"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/consts"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capiutil "sigs.k8s.io/cluster-api/util"
)

var (
	errCAPIMachineAWSMachineAWSClusterCannotBeNil            = errors.New("provided Machine, AWSMachine and AWSCluster can not be nil")
	errCAPIMachineSetAWSMachineTemplateAWSClusterCannotBeNil = errors.New("provided MachineSet, AWSMachineTemplate and AWSCluster can not be nil")
	errNilLoadBalancer                                       = errors.New("nil load balancer")
	errUnsupportedLoadBalancerType                           = errors.New("unsupported load balancer type")
)

const (
	errUnsupportedCAPATenancy     = "unable to convert tenancy, unknown value"
	errUnsupportedCAPAMarketType  = "unable to convert market type, unknown value"
	errUnsupportedHTTPTokensState = "unable to convert httpTokens state, unknown value" //nolint:gosec // This is an error message, not a credential
	defaultIdentityName           = "default"
	defaultCredentialsSecretName  = "aws-cloud-credentials" //#nosec G101 -- False positive, not actually a credential.
)

// machineAndAWSMachineAndAWSCluster stores the details of a Cluster API Machine and AWSMachine and AWSCluster.
type machineAndAWSMachineAndAWSCluster struct {
	machine                               *clusterv1.Machine
	awsMachine                            *awsv1.AWSMachine
	awsCluster                            *awsv1.AWSCluster
	excludeMachineAPILabelsAndAnnotations bool
}

// machineSetAndAWSMachineTemplateAndAWSCluster stores the details of a Cluster API MachineSet and AWSMachineTemplate and AWSCluster.
type machineSetAndAWSMachineTemplateAndAWSCluster struct {
	machineSet *clusterv1.MachineSet
	template   *awsv1.AWSMachineTemplate
	awsCluster *awsv1.AWSCluster
	*machineAndAWSMachineAndAWSCluster
}

// FromMachineAndAWSMachineAndAWSCluster wraps a CAPI Machine and CAPA AWSMachine and CAPA AWSCluster into a capi2mapi MachineAndInfrastructureMachine.
func FromMachineAndAWSMachineAndAWSCluster(m *clusterv1.Machine, am *awsv1.AWSMachine, ac *awsv1.AWSCluster) MachineAndInfrastructureMachine {
	return &machineAndAWSMachineAndAWSCluster{machine: m, awsMachine: am, awsCluster: ac}
}

// FromMachineSetAndAWSMachineTemplateAndAWSCluster wraps a CAPI MachineSet and CAPA AWSMachineTemplate and CAPA AWSCluster into a capi2mapi MachineSetAndAWSMachineTemplateAndAWSCluster.
func FromMachineSetAndAWSMachineTemplateAndAWSCluster(ms *clusterv1.MachineSet, mts *awsv1.AWSMachineTemplate, ac *awsv1.AWSCluster) MachineSetAndMachineTemplate {
	return &machineSetAndAWSMachineTemplateAndAWSCluster{
		machineSet: ms,
		template:   mts,
		awsCluster: ac,
		machineAndAWSMachineAndAWSCluster: &machineAndAWSMachineAndAWSCluster{
			machine: &clusterv1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      ms.Spec.Template.ObjectMeta.Labels,
					Annotations: ms.Spec.Template.ObjectMeta.Annotations,
				},
				Spec: ms.Spec.Template.Spec,
			},
			awsMachine: &awsv1.AWSMachine{
				Spec: mts.Spec.Template.Spec,
			},
			awsCluster:                            ac,
			excludeMachineAPILabelsAndAnnotations: true,
		},
	}
}

// toProviderSpec converts a capi2mapi MachineAndAWSMachineTemplateAndAWSCluster into a MAPI AWSMachineProviderConfig.
//
//nolint:funlen
func (m machineAndAWSMachineAndAWSCluster) toProviderSpec() (*mapiv1beta1.AWSMachineProviderConfig, []string, field.ErrorList) {
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

	mapiAWSMarketType, err := convertAWSMarketTypeToMAPI(fldPath.Child("marketType"), m.awsMachine.Spec.MarketType)
	if err != nil {
		errors = append(errors, err)
	}

	mapiLoadBalancers, lbErrs := convertAWSClusterLoadBalancersToMAPI(fldPath, m.machine, m.awsCluster)
	if len(lbErrs) > 0 {
		errors = append(errors, lbErrs...)
	}

	warnings = append(warnings, warn...)

	mapaProviderConfig := mapiv1beta1.AWSMachineProviderConfig{
		TypeMeta: metav1.TypeMeta{
			Kind: "AWSMachineProviderConfig",
			// In the machineSets both "awsproviderconfig.openshift.io/v1beta1" and "machine.openshift.io/v1beta1" can be found.
			// Here we always settle on one of the two.
			APIVersion: "machine.openshift.io/v1beta1",
		},
		// ObjectMeta - Only present because it's needed to form part of the runtime.RawExtension, not actually used by MAPA.
		AMI: mapiv1beta1.AWSResourceReference{
			// The use of ARN and Filters to reference AMIs was present
			// in CAPA but has been deprecated and then removed
			// ref: https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3257
			ID: m.awsMachine.Spec.AMI.ID,
		},
		InstanceType: m.awsMachine.Spec.InstanceType,
		CPUOptions:   ConvertAWSCPUOptionsToMAPI(m.awsMachine.Spec.CPUOptions),
		Tags:         convertAWSTagsToMAPI(m.awsMachine.Spec.AdditionalTags),
		IAMInstanceProfile: &mapiv1beta1.AWSResourceReference{
			ID: &m.awsMachine.Spec.IAMInstanceProfile,
		},
		// UserDataSecret - Populated below.
		// CredentialsSecret - Handled below.
		KeyName: m.awsMachine.Spec.SSHKeyName,
		// DeviceIndex - OCPCLOUD-2707: Value must always be zero. No other values are valid in MAPA even though the value is configurable.
		PublicIP:             m.awsMachine.Spec.PublicIP,
		SecurityGroups:       convertAWSSecurityGroupstoMAPI(m.awsMachine.Spec.AdditionalSecurityGroups), // This is the way we want to convert security groups, as the AdditionalSecurity Groups are what gets added to MAPI SGs.
		NetworkInterfaceType: convertAWSNetworkInterfaceTypeToMAPI(m.awsMachine.Spec.NetworkInterfaceType),
		Subnet:               convertAWSResourceReferenceToMAPI(ptr.Deref(m.awsMachine.Spec.Subnet, awsv1.AWSResourceReference{})),
		Placement: mapiv1beta1.Placement{
			AvailabilityZone: m.machine.Spec.FailureDomain,
			Tenancy:          mapaTenancy,
			Region:           m.awsCluster.Spec.Region,
		},
		// HostPlacement: TODO: add conversion from CAPA HostAffinity and HostID to MAPI HostPlacement when the MAPI API is finalized.
		LoadBalancers: mapiLoadBalancers,
		// BlockDevices - Populated below.
		SpotMarketOptions:       convertAWSSpotMarketOptionsToMAPI(m.awsMachine.Spec.SpotMarketOptions),
		MetadataServiceOptions:  mapiAWSMetadataOptions,
		PlacementGroupName:      m.awsMachine.Spec.PlacementGroupName,
		PlacementGroupPartition: convertAWSPlacementGroupPartition(m.awsMachine.Spec.PlacementGroupPartition),
		CapacityReservationID:   ptr.Deref(m.awsMachine.Spec.CapacityReservationID, ""),
		MarketType:              mapiAWSMarketType,
	}

	secretRef, errs := handleAWSIdentityRef(fldPath.Child("identityRef"), m.awsCluster.Spec.IdentityRef)

	if len(errs) > 0 {
		errors = append(errors, errs...)
	} else {
		mapaProviderConfig.CredentialsSecret = secretRef
	}

	userDataSecretName := ptr.Deref(m.machine.Spec.Bootstrap.DataSecretName, "")
	if userDataSecretName != "" {
		mapaProviderConfig.UserDataSecret = &corev1.LocalObjectReference{
			Name: userDataSecretName,
		}
	}

	mapaProviderConfig.BlockDevices, errs = convertAWSVolumesToMAPI(fldPath, m.awsMachine.Spec.RootVolume, m.awsMachine.Spec.NonRootVolumes)
	if len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// Below this line are fields not used from the CAPI AWSMachine.

	// ProviderID - Populated at a different level.
	// InstanceID - Ignore - Is a subset of providerID.
	// Ignition - Ignore - Only has a version field and we force this to a particular value.

	if m.awsMachine.Spec.NetworkInterfaceType != "" && m.awsMachine.Spec.NetworkInterfaceType != awsv1.NetworkInterfaceTypeEFAWithENAInterface && m.awsMachine.Spec.NetworkInterfaceType != awsv1.NetworkInterfaceTypeENI {
		errors = append(errors, field.Invalid(fldPath.Child("networkInterfaceType"), m.awsMachine.Spec.NetworkInterfaceType, "networkInterface type must be one of interface, efa or omitted, unsupported value"))
	}
	// There are quite a few unsupported fields, so break them out for now.
	errors = append(errors, handleUnsupportedAWSMachineFields(fldPath, m.awsMachine.Spec)...)

	if len(errors) > 0 {
		return nil, warnings, errors
	}

	return &mapaProviderConfig, warnings, nil
}

func (m machineAndAWSMachineAndAWSCluster) toProviderStatus() *mapiv1beta1.AWSMachineProviderStatus {
	s := &mapiv1beta1.AWSMachineProviderStatus{
		InstanceState: ptr.To(string(ptr.Deref(m.awsMachine.Status.InstanceState, ""))),
		InstanceID:    m.awsMachine.Spec.InstanceID,
		Conditions:    convertCAPAMachineConditionsToMAPIMachineAWSProviderConditions(m.awsMachine),
	}

	return s
}

func convertCAPAMachineConditionsToMAPIMachineAWSProviderConditions(awsMachine *awsv1.AWSMachine) []metav1.Condition {
	if ptr.Deref(awsMachine.Status.InstanceState, "") == awsv1.InstanceStateRunning {
		// Set conditionSuccess
		return []metav1.Condition{{
			Type:    string(mapiv1beta1.MachineCreation),
			Status:  metav1.ConditionTrue,
			Reason:  mapiv1beta1.MachineCreationSucceededConditionReason,
			Message: "Machine successfully created",
			// LastTransitionTime will be set by the condition utilities.
		}}
	}

	// Set conditionFailed
	return []metav1.Condition{{
		Type:    string(mapiv1beta1.MachineCreation),
		Status:  metav1.ConditionFalse,
		Reason:  mapiv1beta1.MachineCreationFailedConditionReason,
		Message: "See AWSMachine conditions.",
		// LastTransitionTime will be set by the condition utilities.
	}}
}

// ToMachine converts a capi2mapi MachineAndAWSMachineTemplate into a MAPI Machine.
func (m machineAndAWSMachineAndAWSCluster) ToMachine() (*mapiv1beta1.Machine, []string, error) {
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

	awsSpecRawExt, errRaw := RawExtensionFromInterface(mapaSpec)
	if errRaw != nil {
		return nil, nil, fmt.Errorf("unable to convert AWS providerSpec to raw extension: %w", errRaw)
	}

	awsStatusRawExt, errRaw := RawExtensionFromInterface(m.toProviderStatus())
	if errRaw != nil {
		return nil, nil, fmt.Errorf("unable to convert AWS providerStatus to raw extension: %w", errRaw)
	}

	warnings = append(warnings, warn...)

	var additionalMachineAPIMetadataLabels, additionalMachineAPIMetadataAnnotations map[string]string
	if !m.excludeMachineAPILabelsAndAnnotations {
		additionalMachineAPIMetadataLabels = map[string]string{
			consts.MAPIMachineMetadataLabelInstanceType: m.awsMachine.Spec.InstanceType,
			consts.MAPIMachineMetadataLabelRegion:       m.awsCluster.Spec.Region,
			consts.MAPIMachineMetadataLabelZone:         m.machine.Spec.FailureDomain,
		}

		additionalMachineAPIMetadataAnnotations = map[string]string{
			consts.MAPIMachineMetadataAnnotationInstanceState: string(ptr.Deref(m.awsMachine.Status.InstanceState, "")),
		}
	}

	mapiMachine, err := fromCAPIMachineToMAPIMachine(m.machine, additionalMachineAPIMetadataLabels, additionalMachineAPIMetadataAnnotations)
	if err != nil {
		errors = append(errors, err...)
	}

	mapiMachine.Spec.ProviderSpec.Value = awsSpecRawExt
	mapiMachine.Status.ProviderStatus = awsStatusRawExt

	if len(errors) > 0 {
		return nil, warnings, errors.ToAggregate()
	}

	return mapiMachine, warnings, nil
}

// ToMachineSet converts a capi2mapi MachineAndAWSMachineTemplate into a MAPI MachineSet.
func (m machineSetAndAWSMachineTemplateAndAWSCluster) ToMachineSet() (*mapiv1beta1.MachineSet, []string, error) { //nolint:dupl
	if m.machineSet == nil || m.template == nil || m.awsCluster == nil || m.machineAndAWSMachineAndAWSCluster == nil {
		return nil, nil, errCAPIMachineSetAWSMachineTemplateAWSClusterCannotBeNil
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

	// Copy the labels and annotations from the Machine to the template.
	mapiMachineSet.Spec.Template.ObjectMeta.Annotations = mapiMachine.ObjectMeta.Annotations
	mapiMachineSet.Spec.Template.ObjectMeta.Labels = mapiMachine.ObjectMeta.Labels

	return mapiMachineSet, warnings, nil
}

// Conversion helpers.

func convertAWSMetadataOptionsToMAPI(fldPath *field.Path, capiMetadataOpts *awsv1.InstanceMetadataOptions) (mapiv1beta1.MetadataServiceOptions, []string, field.ErrorList) {
	var (
		errors   field.ErrorList
		warnings []string
	)

	if capiMetadataOpts == nil {
		return mapiv1beta1.MetadataServiceOptions{}, nil, nil
	}

	var auth mapiv1beta1.MetadataServiceAuthentication

	switch capiMetadataOpts.HTTPTokens {
	case "":
		// Defaults to optional on both sides.
	case awsv1.HTTPTokensStateOptional:
		auth = mapiv1beta1.MetadataServiceAuthenticationOptional
	case awsv1.HTTPTokensStateRequired:
		auth = mapiv1beta1.MetadataServiceAuthenticationRequired
	default:
		errors = append(errors, field.Invalid(fldPath.Child("httpTokens"), capiMetadataOpts.HTTPTokens, errUnsupportedHTTPTokensState))
	}

	if capiMetadataOpts.HTTPEndpoint != "" && capiMetadataOpts.HTTPEndpoint != awsv1.InstanceMetadataEndpointStateEnabled {
		// This defaults to "enabled" in CAPI and on the AWS side, so if it's not "enabled", the user explicitly chose another option.
		// TODO(OCPCLOUD-2710): We should implement this within MAPI to create feature parity.
		errors = append(errors, field.Invalid(fldPath.Child("httpEndpoint"), capiMetadataOpts.HTTPEndpoint, fmt.Sprintf("httpEndpoint values other than %q are not supported", awsv1.InstanceMetadataEndpointStateEnabled)))
	}

	if capiMetadataOpts.HTTPPutResponseHopLimit != 0 && capiMetadataOpts.HTTPPutResponseHopLimit != 1 {
		// This defaults to 1 in CAPI and on the AWS side, so if it's not 1, the user explicitly chose another option.
		// TODO(OCPCLOUD-2710): We should implement this within MAPI to create feature parity.
		errors = append(errors, field.Invalid(fldPath.Child("httpPutResponseHopLimit"), capiMetadataOpts.HTTPPutResponseHopLimit, "httpPutResponseHopLimit values other than 1 are not supported"))
	}

	if capiMetadataOpts.InstanceMetadataTags != "" && capiMetadataOpts.InstanceMetadataTags != awsv1.InstanceMetadataEndpointStateDisabled {
		// This defaults to "disabled" in CAPI and on the AWS side, so if it's not "disabled", the user explicitly chose another option.
		// TODO(OCPCLOUD-2710): We should implement this within MAPI to create feature parity.
		errors = append(errors, field.Invalid(fldPath.Child("instanceMetadataTags"), capiMetadataOpts.InstanceMetadataTags, fmt.Sprintf("instanceMetadataTags values other than %q are not supported", awsv1.InstanceMetadataEndpointStateDisabled)))
	}

	metadataOpts := mapiv1beta1.MetadataServiceOptions{
		Authentication: auth,
	}

	if len(errors) > 0 {
		return mapiv1beta1.MetadataServiceOptions{}, warnings, errors
	}

	return metadataOpts, warnings, nil
}

func convertAWSResourceReferenceToMAPI(capiReference awsv1.AWSResourceReference) mapiv1beta1.AWSResourceReference {
	filters := convertAWSFiltersToMAPI(capiReference.Filters)

	return mapiv1beta1.AWSResourceReference{
		ID:      capiReference.ID,
		Filters: filters,
	}
}

func convertAWSFiltersToMAPI(capiFilters []awsv1.Filter) []mapiv1beta1.Filter {
	mapiFilters := []mapiv1beta1.Filter{}
	for _, filter := range capiFilters {
		mapiFilters = append(mapiFilters, mapiv1beta1.Filter{
			Name:   filter.Name,
			Values: filter.Values,
		})
	}

	return mapiFilters
}

func convertAWSTagsToMAPI(capiTags awsv1.Tags) []mapiv1beta1.TagSpecification {
	mapiTags := []mapiv1beta1.TagSpecification{}
	for key, value := range capiTags {
		mapiTags = append(mapiTags, mapiv1beta1.TagSpecification{
			Name:  key,
			Value: value,
		})
	}

	return mapiTags
}

func convertAWSSecurityGroupstoMAPI(sgs []awsv1.AWSResourceReference) []mapiv1beta1.AWSResourceReference {
	mapiSGs := []mapiv1beta1.AWSResourceReference{}

	for _, sg := range sgs {
		mapiAWSResourceRef := convertAWSResourceReferenceToMAPI(sg)

		mapiSGs = append(mapiSGs, mapiAWSResourceRef)
	}

	return mapiSGs
}

func convertAWSSpotMarketOptionsToMAPI(capiSpotMarketOptions *awsv1.SpotMarketOptions) *mapiv1beta1.SpotMarketOptions {
	if capiSpotMarketOptions == nil {
		return nil
	}

	return &mapiv1beta1.SpotMarketOptions{
		MaxPrice: capiSpotMarketOptions.MaxPrice,
	}
}

func convertAWSTenancyToMAPI(fldPath *field.Path, capiTenancy string) (mapiv1beta1.InstanceTenancy, *field.Error) {
	switch capiTenancy {
	case "default":
		return mapiv1beta1.DefaultTenancy, nil
	case "dedicated":
		return mapiv1beta1.DedicatedTenancy, nil
	case "host":
		return mapiv1beta1.HostTenancy, nil
	case "":
		return "", nil
	default:
		return "", field.Invalid(fldPath, capiTenancy, errUnsupportedCAPATenancy)
	}
}

func convertAWSMarketTypeToMAPI(fldPath *field.Path, marketType awsv1.MarketType) (mapiv1beta1.MarketType, *field.Error) {
	switch marketType {
	case awsv1.MarketTypeOnDemand:
		return mapiv1beta1.MarketTypeOnDemand, nil
	case awsv1.MarketTypeSpot:
		return mapiv1beta1.MarketTypeSpot, nil
	case awsv1.MarketTypeCapacityBlock:
		return mapiv1beta1.MarketTypeCapacityBlock, nil
	case "":
		return "", nil
	default:
		return "", field.Invalid(fldPath, marketType, errUnsupportedCAPAMarketType)
	}
}

func convertAWSVolumesToMAPI(fldPath *field.Path, rootVolume *awsv1.Volume, nonRootVolumes []awsv1.Volume) ([]mapiv1beta1.BlockDeviceMappingSpec, field.ErrorList) {
	var (
		blockDeviceMapping []mapiv1beta1.BlockDeviceMappingSpec
		errors             field.ErrorList
	)

	if rootVolume != nil && *rootVolume != (awsv1.Volume{}) {
		bdm, err := convertAWSVolumeToMAPI(fldPath.Child("rootVolume"), *rootVolume)
		if err != nil {
			errors = append(errors, err)
		} else {
			blockDeviceMapping = append(blockDeviceMapping, bdm)
		}
	}

	for i, volume := range nonRootVolumes {
		bdm, err := convertAWSVolumeToMAPI(fldPath.Child("nonRootVolumes").Index(i), volume)
		if err != nil {
			errors = append(errors, err)
		} else {
			blockDeviceMapping = append(blockDeviceMapping, bdm)
		}
	}

	return blockDeviceMapping, errors
}

func convertAWSVolumeToMAPI(fldPath *field.Path, volume awsv1.Volume) (mapiv1beta1.BlockDeviceMappingSpec, *field.Error) {
	bdm := mapiv1beta1.BlockDeviceMappingSpec{
		EBS: &mapiv1beta1.EBSBlockDeviceSpec{
			VolumeSize: ptr.To(volume.Size),
			Encrypted:  volume.Encrypted,
			KMSKey:     convertAWSKMSKeyToMAPI(volume.EncryptionKey),
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

	if volume.Throughput != nil {
		if *volume.Throughput > math.MaxInt32 {
			return mapiv1beta1.BlockDeviceMappingSpec{}, field.Invalid(fldPath.Child("throughput"), *volume.Throughput, "throughput exceeds maximum int32 value")
		}
		//nolint:gosec
		bdm.EBS.ThroughputMib = ptr.To(int32(*volume.Throughput))
	}

	return bdm, nil
}

func convertAWSKMSKeyToMAPI(kmsKey string) mapiv1beta1.AWSResourceReference {
	if strings.HasPrefix(kmsKey, "arn:") {
		return mapiv1beta1.AWSResourceReference{
			ARN: &kmsKey,
		}
	}

	return mapiv1beta1.AWSResourceReference{
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

func convertAWSNetworkInterfaceTypeToMAPI(networkInterfaceType awsv1.NetworkInterfaceType) mapiv1beta1.AWSNetworkInterfaceType {
	switch networkInterfaceType {
	case awsv1.NetworkInterfaceTypeEFAWithENAInterface:
		return mapiv1beta1.AWSEFANetworkInterfaceType
	case awsv1.NetworkInterfaceTypeENI:
		return mapiv1beta1.AWSENANetworkInterfaceType
	}

	return ""
}

// handleUnsupportedAWSMachineFields returns an error for every present field in the AWSMachineSpec that
// we are currently, or indefinitely not supporting.
// These are protected by VAPs so should never actually cause an error here.
func handleUnsupportedAWSMachineFields(fldPath *field.Path, spec awsv1.AWSMachineSpec) field.ErrorList {
	errs := field.ErrorList{}

	if spec.AMI.EKSOptimizedLookupType != nil {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("ami", "eksOptimizedLookupType"), spec.AMI.EKSOptimizedLookupType, "eksOptimizedLookupType is not supported"))
	}

	if spec.ImageLookupFormat != "" {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("imageLookupFormat"), spec.ImageLookupFormat, "imageLookupFormat is not supported"))
	}

	if spec.ImageLookupOrg != "" {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("imageLookupOrg"), spec.ImageLookupOrg, "imageLookupOrg is not supported"))
	}

	if spec.ImageLookupBaseOS != "" {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("imageLookupBaseOS"), spec.ImageLookupBaseOS, "imageLookupBaseOS is not supported"))
	}

	if len(spec.SecurityGroupOverrides) > 0 {
		// We do not support SecurityGroupOverrides being used, because the externally managed annotation that we add updates the behaviour to stop this.
		errs = append(errs, field.Invalid(fldPath.Child("securityGroupOverrides"), spec.SecurityGroupOverrides, "securityGroupOverrides are not supported"))
	}

	if len(spec.NetworkInterfaces) > 0 {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("networkInterfaces"), spec.NetworkInterfaces, "networkInterfaces are not supported"))
	}

	if spec.UncompressedUserData != nil {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("uncompressedUserData"), spec.UncompressedUserData, "uncompressedUserData is not supported"))
	}

	if (spec.CloudInit != awsv1.CloudInit{}) {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("cloudInit"), spec.CloudInit, "cloudInit is not supported"))
	}

	if spec.PrivateDNSName != nil {
		// Not required for our use case.
		errs = append(errs, field.Invalid(fldPath.Child("privateDNSName"), spec.PrivateDNSName, "privateDNSName is not supported"))
	}

	if spec.Ignition != nil {
		if spec.Ignition.Proxy != nil {
			// Ignition proxy is not configurable in MAPI. Not required for our use case.
			errs = append(errs, field.Invalid(fldPath.Child("ignition", "proxy"), spec.Ignition.Proxy, "ignition proxy is not supported"))
		}

		if spec.Ignition.TLS != nil {
			// Ignition TLS is not configurable in MAPI. Not required for our use case.
			errs = append(errs, field.Invalid(fldPath.Child("ignition", "tls"), spec.Ignition.TLS, "ignition tls is not supported"))
		}
	}

	return errs
}

// handleAWSIdentityRef returns errors if the configuration IdentityRef is different from OCP defaults, and the default credential reference otherwise.
// We only support the ControllerIdentityKind, which is the upstream default, when converting.
// This default is what will happen when no IdentityRef is defined, so support both hard-coded values and the empty reference.
func handleAWSIdentityRef(fldPath *field.Path, identityRef *awsv1.AWSIdentityReference) (*corev1.LocalObjectReference, field.ErrorList) {
	errs := field.ErrorList{}

	ref := &corev1.LocalObjectReference{
		Name: defaultCredentialsSecretName,
	}

	// An unset identityref will use the default values.
	// This also protects against nil lookups below.
	if identityRef == nil {
		return ref, nil
	}

	if identityRef.Kind != awsv1.ControllerIdentityKind && identityRef.Kind != "" {
		errs = append(errs, field.Invalid(fldPath.Child("kind"), identityRef.Kind, fmt.Sprintf("kind %q cannot be converted to CredentialsSecret. Please see https://access.redhat.com/articles/7116313 for more details.", identityRef.Kind)))
	}

	if identityRef.Name != defaultIdentityName && identityRef.Name != "" {
		errs = append(errs, field.Invalid(fldPath.Child("name"), identityRef.Name, fmt.Sprintf("name %q must be %q when using an AWSClusterControllerIdentity. Please see https://access.redhat.com/articles/7116313 for more details.", identityRef.Name, defaultIdentityName)))
	}

	if len(errs) > 0 {
		return nil, errs
	}

	// Assume we're using the defaults.
	return ref, nil
}

// convertAWSClusterLoadBalancersToMAPI convert CAPI LoadBalancers from the AWSCluster spec to MAPI LoadBalancerReferences on the Machine.
func convertAWSClusterLoadBalancersToMAPI(fldPath *field.Path, machine *clusterv1.Machine, awsCluster *awsv1.AWSCluster) ([]mapiv1beta1.LoadBalancerReference, field.ErrorList) {
	var loadBalancers []mapiv1beta1.LoadBalancerReference

	errs := field.ErrorList{}

	if !capiutil.IsControlPlaneMachine(machine) {
		// No loadbalancer on non-control plane machines.
		return nil, nil
	}

	internalLoadBalancerRef, err := ConvertAWSLoadBalancerToMAPI(awsCluster.Spec.ControlPlaneLoadBalancer)
	if err != nil {
		errs = append(errs, field.Invalid(fldPath.Child("controlPlaneLoadBalancer"), awsCluster.Spec.ControlPlaneLoadBalancer, fmt.Errorf("failed to convert load balancer: %w", err).Error()))
	} else {
		loadBalancers = append(loadBalancers, internalLoadBalancerRef)
	}

	if awsCluster.Spec.SecondaryControlPlaneLoadBalancer != nil {
		externalLoadBalancerRef, err := ConvertAWSLoadBalancerToMAPI(awsCluster.Spec.SecondaryControlPlaneLoadBalancer)
		if err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("secondaryControlPlaneLoadBalancer"), awsCluster.Spec.SecondaryControlPlaneLoadBalancer, fmt.Errorf("failed to convert load balancer: %w", err).Error()))
		} else {
			loadBalancers = append(loadBalancers, externalLoadBalancerRef)
		}
	}

	return loadBalancers, errs
}

// ConvertAWSLoadBalancerToMAPI converts CAPI AWSLoadBalancerSpec to MAPI LoadBalancerReference.
func ConvertAWSLoadBalancerToMAPI(loadBalancer *awsv1.AWSLoadBalancerSpec) (mapiv1beta1.LoadBalancerReference, error) {
	if loadBalancer == nil {
		return mapiv1beta1.LoadBalancerReference{}, errNilLoadBalancer
	}

	switch loadBalancer.LoadBalancerType {
	case awsv1.LoadBalancerTypeClassic, awsv1.LoadBalancerTypeELB:
		return mapiv1beta1.LoadBalancerReference{
			Name: ptr.Deref(loadBalancer.Name, ""),
			Type: mapiv1beta1.ClassicLoadBalancerType,
		}, nil
	case awsv1.LoadBalancerTypeNLB:
		return mapiv1beta1.LoadBalancerReference{
			Name: ptr.Deref(loadBalancer.Name, ""),
			Type: mapiv1beta1.NetworkLoadBalancerType,
		}, nil
	default:
		return mapiv1beta1.LoadBalancerReference{}, errUnsupportedLoadBalancerType
	}
}

// ConvertAWSCPUOptionsToMAPI converts CAPI CPUOptions to MAPI CPUOptions.
func ConvertAWSCPUOptionsToMAPI(cpuOptions awsv1.CPUOptions) *mapiv1beta1.CPUOptions {
	mapiCPUOptions := &mapiv1beta1.CPUOptions{}

	switch cpuOptions.ConfidentialCompute {
	case awsv1.AWSConfidentialComputePolicyDisabled:
		mapiCPUOptions.ConfidentialCompute = ptr.To(mapiv1beta1.AWSConfidentialComputePolicyDisabled)
	case awsv1.AWSConfidentialComputePolicySEVSNP:
		mapiCPUOptions.ConfidentialCompute = ptr.To(mapiv1beta1.AWSConfidentialComputePolicySEVSNP)
	}

	if *mapiCPUOptions == (mapiv1beta1.CPUOptions{}) {
		return nil
	}

	return mapiCPUOptions
}

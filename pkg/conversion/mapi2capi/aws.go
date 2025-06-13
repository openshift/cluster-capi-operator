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
	"reflect"
	"regexp"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"

	"github.com/openshift/cluster-capi-operator/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	// DefaultCredentialsSecretName is the name of the default secret containing AWS cloud credentials.
	DefaultCredentialsSecretName = "aws-cloud-credentials" //#nosec G101 -- This is a false positive.
)

var (
	errUnexpectedObjectTypeForMachine = errors.New("unexpected type for capaMachineObj")
)

const (
	awsMachineKind         = "AWSMachine"
	awsMachineTemplateKind = "AWSMachineTemplate"

	errUnsupportedMAPIMarketType = "unable to convert market type, unknown value"
)

// awsMachineAndInfra stores the details of a Machine API AWSMachine and Infra.
type awsMachineAndInfra struct {
	machine        *mapiv1.Machine
	infrastructure *configv1.Infrastructure
}

// awsMachineSetAndInfra stores the details of a Machine API AWSMachine set and Infra.
type awsMachineSetAndInfra struct {
	machineSet     *mapiv1.MachineSet
	infrastructure *configv1.Infrastructure
	*awsMachineAndInfra
}

// FromAWSMachineAndInfra wraps a Machine API Machine for AWS and the OCP Infrastructure object into a mapi2capi AWSProviderSpec.
func FromAWSMachineAndInfra(m *mapiv1.Machine, i *configv1.Infrastructure) Machine {
	return &awsMachineAndInfra{machine: m, infrastructure: i}
}

// FromAWSMachineSetAndInfra wraps a Machine API MachineSet for AWS and the OCP Infrastructure object into a mapi2capi AWSProviderSpec.
func FromAWSMachineSetAndInfra(m *mapiv1.MachineSet, i *configv1.Infrastructure) MachineSet {
	return &awsMachineSetAndInfra{
		machineSet:     m,
		infrastructure: i,
		awsMachineAndInfra: &awsMachineAndInfra{
			machine: &mapiv1.Machine{
				Spec: m.Spec.Template.Spec,
			},
			infrastructure: i,
		},
	}
}

// ToMachineAndInfrastructureMachine is used to generate a CAPI Machine and the corresponding InfrastructureMachine
// from the stored MAPI Machine and Infrastructure objects.
func (m *awsMachineAndInfra) ToMachineAndInfrastructureMachine() (*capiv1.Machine, client.Object, []string, error) {
	capiMachine, capaMachine, warnings, errs := m.toMachineAndInfrastructureMachine()

	if len(errs) > 0 {
		return nil, nil, warnings, errs.ToAggregate()
	}

	return capiMachine, capaMachine, warnings, nil
}

//nolint:funlen
func (m *awsMachineAndInfra) toMachineAndInfrastructureMachine() (*capiv1.Machine, client.Object, []string, field.ErrorList) {
	var (
		errs     field.ErrorList
		warnings []string
	)

	awsProviderConfig, err := AWSProviderSpecFromRawExtension(m.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, nil, nil, field.ErrorList{field.Invalid(field.NewPath("spec", "providerSpec", "value"), m.machine.Spec.ProviderSpec.Value, err.Error())}
	}

	capaMachine, warn, machineErrs := m.toAWSMachine(awsProviderConfig)
	if machineErrs != nil {
		errs = append(errs, machineErrs...)
	}

	warnings = append(warnings, warn...)

	capiMachine, machineErrs := fromMAPIMachineToCAPIMachine(m.machine, capav1.GroupVersion.String(), awsMachineKind)
	if machineErrs != nil {
		errs = append(errs, machineErrs...)
	}

	// Extract and plug InstanceID and ProviderID on CAPA, if the providerID is present on CAPI (instance has been provisioned).
	if capiMachine.Spec.ProviderID != nil {
		instanceID := instanceIDFromProviderID(*capiMachine.Spec.ProviderID)
		if instanceID == "" {
			errs = append(errs, field.Invalid(field.NewPath("spec", "providerID"), capiMachine.Spec.ProviderID, "unable to find InstanceID in ProviderID"))
		} else {
			capaMachine.Spec.InstanceID = ptr.To(instanceID)
			capaMachine.Spec.ProviderID = capiMachine.Spec.ProviderID
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

	// Populate the CAPI Machine ClusterName from the OCP Infrastructure object.
	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachine.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachine.Labels[capiv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	// The InfraMachine should always have the same labels and annotations as the Machine.
	// See https://github.com/kubernetes-sigs/cluster-api/blob/f88d7ae5155700c2cc367b31ddcc151c9ad579e4/internal/controllers/machineset/machineset_controller.go#L578-L579
	capiMachineAnnotations := capiMachine.GetAnnotations()
	if len(capiMachineAnnotations) > 0 {
		capaMachine.SetAnnotations(capiMachineAnnotations)
	}

	capiMachineLabels := capiMachine.GetLabels()
	if len(capiMachineLabels) > 0 {
		capaMachine.SetLabels(capiMachineLabels)
	}

	return capiMachine, capaMachine, warnings, errs
}

// ToMachineSetAndMachineTemplate converts a mapi2capi AWSMachineSetAndInfra into a CAPI MachineSet and CAPA AWSMachineTemplate.
//
//nolint:dupl
func (m *awsMachineSetAndInfra) ToMachineSetAndMachineTemplate() (*capiv1.MachineSet, client.Object, []string, error) {
	var (
		errs     []error
		warnings []string
	)

	capiMachine, capaMachineObj, warn, errList := m.toMachineAndInfrastructureMachine()
	if errList != nil {
		errs = append(errs, errList.ToAggregate().Errors()...)
	}

	warnings = append(warnings, warn...)

	capaMachine, ok := capaMachineObj.(*capav1.AWSMachine)
	if !ok {
		panic(fmt.Errorf("%w: %T", errUnexpectedObjectTypeForMachine, capaMachineObj))
	}

	capaMachineTemplate, err := awsMachineToAWSMachineTemplate(capaMachine, m.machineSet.Name, capiNamespace)
	if err != nil {
		errs = append(errs, err)
	}

	capiMachineSet, machineSetErrs := fromMAPIMachineSetToCAPIMachineSet(m.machineSet)
	if machineSetErrs != nil {
		errs = append(errs, machineSetErrs.Errors()...)
	}

	capiMachineSet.Spec.Template.Spec = capiMachine.Spec

	// We have to merge these two maps so that labels and annotations added to the template objectmeta are persisted
	// along with the labels and annotations from the machine objectmeta.
	capiMachineSet.Spec.Template.ObjectMeta.Labels = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Labels, capiMachine.Labels)
	capiMachineSet.Spec.Template.ObjectMeta.Annotations = util.MergeMaps(capiMachineSet.Spec.Template.ObjectMeta.Annotations, capiMachine.Annotations)

	// Override the reference so that it matches the AWSMachineTemplate.
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Kind = awsMachineTemplateKind
	capiMachineSet.Spec.Template.Spec.InfrastructureRef.Name = capaMachineTemplate.Name

	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachineSet.Spec.Template.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachineSet.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachineSet.Labels[capiv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
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
func (m *awsMachineAndInfra) toAWSMachine(providerSpec mapiv1.AWSMachineProviderConfig) (*capav1.AWSMachine, []string, field.ErrorList) {
	fldPath := field.NewPath("spec", "providerSpec", "value")

	var (
		errs     field.ErrorList
		warnings []string
	)

	rootVolume, nonRootVolumes, warn, blockErrs := convertAWSBlockDeviceMappingSpecToCAPI(fldPath.Child("blockDevices"), providerSpec.BlockDevices)
	if blockErrs != nil {
		errs = append(errs, blockErrs...)
	}

	warnings = append(warnings, warn...)

	capiAWSAMIReference, err := convertAWSAMIResourceReferenceToCAPI(fldPath.Child("ami"), providerSpec.AMI)
	if err != nil {
		errs = append(errs, err)
	}

	instanceMetadataOptions, err := convertMetadataServiceOptionstoCAPI(fldPath.Child("metadataServiceOptions"), providerSpec.MetadataServiceOptions)
	if err != nil {
		errs = append(errs, err)
	}

	capiAWSMarketType, err := convertAWSMarketTypeToCAPI(fldPath.Child("marketType"), providerSpec.MarketType)
	if err != nil {
		errs = append(errs, err)
	}

	spec := capav1.AWSMachineSpec{
		AMI:                      capiAWSAMIReference,
		AdditionalSecurityGroups: convertAWSSecurityGroupstoCAPI(providerSpec.SecurityGroups),
		AdditionalTags:           convertAWSTagsToCAPI(providerSpec.Tags),
		IAMInstanceProfile:       convertIAMInstanceProfiletoCAPI(providerSpec.IAMInstanceProfile),
		Ignition: &capav1.Ignition{
			Version:     "3.4",                                               // TODO(OCPCLOUD-2719): Should this be extracted from the ignition in the user data secret?
			StorageType: capav1.IgnitionStorageTypeOptionUnencryptedUserData, // Hardcoded for OpenShift.
		},

		// CloudInit. Not used in OpenShift (we only use Ignition).
		// ImageLookupBaseOS. Not used in OpenShift.
		// ImageLookupFormat. Not used in OpenShift.
		// ImageLookupOrg. Not used in OpenShift.
		// NetworkInterfaces. Not used in OpenShift.

		NetworkInterfaceType:    convertNetworkInterfaceType(providerSpec.NetworkInterfaceType),
		InstanceMetadataOptions: instanceMetadataOptions,
		InstanceType:            providerSpec.InstanceType,
		NonRootVolumes:          nonRootVolumes,
		PlacementGroupName:      providerSpec.PlacementGroupName,
		PlacementGroupPartition: int64(ptr.Deref(providerSpec.PlacementGroupPartition, 0)),
		// ProviderID. This is populated when this is called in higher level funcs (ToMachine(), ToMachineSet()).
		// InstanceID. This is populated when this is called in higher level funcs (ToMachine(), ToMachineSet()).
		PublicIP:          providerSpec.PublicIP,
		RootVolume:        rootVolume,
		SSHKeyName:        providerSpec.KeyName,
		SpotMarketOptions: convertAWSSpotMarketOptionsToCAPI(providerSpec.SpotMarketOptions),
		Subnet:            convertAWSResourceReferenceToCAPI(providerSpec.Subnet),
		Tenancy:           string(providerSpec.Placement.Tenancy),
		// UncompressedUserData: Not used in OpenShift.
		MarketType: capiAWSMarketType,
	}

	if providerSpec.CapacityReservationID != "" {
		spec.CapacityReservationID = &providerSpec.CapacityReservationID
	}

	// Unused fields - Below this line are fields not used from the MAPI AWSMachineProviderConfig.

	// TypeMeta - Only for the purpose of the raw extension, not used for any functionality.

	// Only take action when a non-default credentials secret is being used in MAPI.
	// If the user is using the default, then their CAPI secret will already be configured and no action is necessary.
	if providerSpec.CredentialsSecret != nil &&
		providerSpec.CredentialsSecret.Name != DefaultCredentialsSecretName {
		// Not convertable; need a custom identity ref
		errs = append(errs, field.Invalid(fldPath.Child("credentialsSecret"), providerSpec.CredentialsSecret.Name, fmt.Sprintf("credential secret does not match the default of %q, manual conversion is necessary. Please see https://access.redhat.com/articles/7116313 for more details.", DefaultCredentialsSecretName)))
	}

	if m.infrastructure.Status.PlatformStatus != nil &&
		m.infrastructure.Status.PlatformStatus.AWS != nil &&
		m.infrastructure.Status.PlatformStatus.AWS.Region != "" &&
		providerSpec.Placement.Region != m.infrastructure.Status.PlatformStatus.AWS.Region {
		// Assuming that the platform status has a region, we expect all MachineSets to match that region, if they don't, this is an error on the users part.
		errs = append(errs, field.Invalid(fldPath.Child("placement", "region"), providerSpec.Placement.Region, fmt.Sprintf("placement.region should match infrastructure status value %q", m.infrastructure.Status.PlatformStatus.AWS.Region)))
	}

	if !reflect.DeepEqual(providerSpec.ObjectMeta, metav1.ObjectMeta{}) {
		// We don't support setting the object metadata in the provider spec.
		// It's only present for the purpose of the raw extension and doesn't have any functionality.
		errs = append(errs, field.Invalid(fldPath.Child("metadata"), providerSpec.ObjectMeta, "metadata is not supported"))
	}

	if providerSpec.DeviceIndex != 0 {
		// In MAPA, valid machines only have a DeviceIndex value of 0 or unset. Since only a single network interface is supported, which must have a device index of 0.
		// If a machine is created with a DeviceIndex value other than 0, it will be in a failed state.
		// For more context, see OCPCLOUD-2707.
		errs = append(errs, field.Invalid(fldPath.Child("deviceIndex"), providerSpec.DeviceIndex, "deviceIndex must be 0 or unset"))
	}

	if providerSpec.NetworkInterfaceType != "" && providerSpec.NetworkInterfaceType != mapiv1.AWSENANetworkInterfaceType && providerSpec.NetworkInterfaceType != mapiv1.AWSEFANetworkInterfaceType {
		errs = append(errs, field.Invalid(fldPath.Child("networkInterfaceType"), providerSpec.NetworkInterfaceType, "networkInterface type must be one of ENA, EFA or omitted, unsupported value"))
	}

	if len(providerSpec.LoadBalancers) > 0 {
		// TODO(OCPCLOUD-2709): CAPA only applies load balancers to the control plane nodes. We should always reject LBs on non-control plane and work out how to connect the control plane LBs correctly otherwise.
		errs = append(errs, field.Invalid(fldPath.Child("loadBalancers"), providerSpec.LoadBalancers, "loadBalancers are not supported"))
	}

	return &capav1.AWSMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: capav1.GroupVersion.String(),
			Kind:       "AWSMachine",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.machine.Name,
			Namespace: capiNamespace,
		},
		Spec: spec,
	}, warnings, errs
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

func awsMachineToAWSMachineTemplate(awsMachine *capav1.AWSMachine, name string, namespace string) (*capav1.AWSMachineTemplate, error) {
	nameWithHash, err := util.GenerateInfraMachineTemplateNameWithSpecHash(name, awsMachine.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate infrastructure machine template name with spec hash: %w", err)
	}

	return &capav1.AWSMachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: capav1.GroupVersion.String(),
			Kind:       "AWSMachineTemplate",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameWithHash,
			Namespace: namespace,
		},
		Spec: capav1.AWSMachineTemplateSpec{
			Template: capav1.AWSMachineTemplateResource{
				Spec: awsMachine.Spec,
			},
		},
	}, nil
}

//////// Conversion helpers

func convertAWSAMIResourceReferenceToCAPI(fldPath *field.Path, amiRef mapiv1.AWSResourceReference) (capav1.AMIReference, *field.Error) {
	if amiRef.ARN != nil {
		return capav1.AMIReference{}, field.Invalid(fldPath.Child("arn"), amiRef.ARN, "unable to convert AMI ARN reference. Not supported in CAPI")
	}

	if len(amiRef.Filters) > 0 {
		return capav1.AMIReference{}, field.Invalid(fldPath.Child("filters"), amiRef.Filters, "unable to convert AMI Filters reference. Not supported in CAPI")
	}

	if amiRef.ID != nil {
		return capav1.AMIReference{ID: amiRef.ID}, nil
	}

	return capav1.AMIReference{}, field.Invalid(fldPath, amiRef, "unable to find a valid AMI resource reference")
}

func convertAWSTagsToCAPI(mapiTags []mapiv1.TagSpecification) capav1.Tags {
	if mapiTags == nil {
		return nil
	}

	capiTags := map[string]string{}
	for _, tag := range mapiTags {
		capiTags[tag.Name] = tag.Value
	}

	return capiTags
}

func convertAWSMarketTypeToCAPI(fldPath *field.Path, marketType mapiv1.MarketType) (capav1.MarketType, *field.Error) {
	switch marketType {
	case mapiv1.MarketTypeOnDemand:
		return capav1.MarketTypeOnDemand, nil
	case mapiv1.MarketTypeSpot:
		return capav1.MarketTypeSpot, nil
	case mapiv1.MarketTypeCapacityBlock:
		return capav1.MarketTypeCapacityBlock, nil
	case "":
		return "", nil
	default:
		return "", field.Invalid(fldPath, marketType, errUnsupportedMAPIMarketType)
	}
}

func convertMetadataServiceOptionstoCAPI(fldPath *field.Path, metad mapiv1.MetadataServiceOptions) (*capav1.InstanceMetadataOptions, *field.Error) {
	var httpTokens capav1.HTTPTokensState

	switch metad.Authentication {
	case mapiv1.MetadataServiceAuthenticationOptional:
		httpTokens = capav1.HTTPTokensStateOptional
	case mapiv1.MetadataServiceAuthenticationRequired:
		httpTokens = capav1.HTTPTokensStateRequired
	case "":
		httpTokens = capav1.HTTPTokensStateOptional // TODO(docs): CAPA defaults to optional (in the openAPI spec validation) if the field is empty, lossy translation to document.
	default:
		return &capav1.InstanceMetadataOptions{}, field.Invalid(fldPath.Child("authentication"), metad.Authentication, "unsupported authentication value")
	}

	capiMetadataOpts := &capav1.InstanceMetadataOptions{
		HTTPEndpoint: capav1.InstanceMetadataEndpointStateEnabled, // not present in MAPI, fallback to CAPI default.
		// HTTPPutResponseHopLimit: not present in MAPI, fallback to CAPI default.
		InstanceMetadataTags:    capav1.InstanceMetadataEndpointStateDisabled, // not present in MAPI, fallback to CAPI default.
		HTTPTokens:              httpTokens,
		HTTPPutResponseHopLimit: 1, // TODO(docs): CAPA defaults to 1 (in the openAPI spec validation) if the field is empty, lossy translation to document.
	}

	return capiMetadataOpts, nil
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

func convertAWSBlockDeviceMappingSpecToCAPI(fldPath *field.Path, mapiBlockDeviceMapping []mapiv1.BlockDeviceMappingSpec) (*capav1.Volume, []capav1.Volume, []string, field.ErrorList) {
	// We do not want to preallocate this as we need it nil if no elements are added.
	//nolint:prealloc
	var nonRootVolumes []capav1.Volume

	rootVolume := &capav1.Volume{}
	errs := field.ErrorList{}
	warnings := []string{}

	for i, mapping := range mapiBlockDeviceMapping {
		if mapping.NoDevice != nil {
			// Field exists in the API but is never used within the codebase.
			errs = append(errs, field.Invalid(fldPath.Index(i).Child("noDevice"), mapping.NoDevice, "noDevice is not supported"))
		}

		if mapping.VirtualName != nil {
			// Field exists in the API but is never used within the codebase.
			errs = append(errs, field.Invalid(fldPath.Index(i).Child("virtualName"), mapping.VirtualName, "virtualName is not supported"))
		}

		if mapping.EBS == nil {
			// MAPA ignores any disk that is missing the EBS configuration.
			// See https://github.com/openshift/machine-api-provider-aws/blob/a7b3d12db988bd2bebbabd6c2e80147511b949e7/pkg/actuators/machine/instances.go#L287-L289.
			warnings = append(warnings, field.Invalid(fldPath.Index(i).Child("ebs"), mapping.EBS, "missing ebs configuration for block device").Error())
			continue
		}

		if mapping.DeviceName == nil {
			volume, warn, err := blockDeviceMappingSpecToVolume(fldPath.Index(i), mapping, true)
			errs = append(errs, err...)
			warnings = append(warnings, warn...)

			rootVolume = &volume

			continue
		}

		volume, warn, err := blockDeviceMappingSpecToVolume(fldPath.Index(i), mapping, false)
		errs = append(errs, err...)
		warnings = append(warnings, warn...)

		nonRootVolumes = append(nonRootVolumes, volume)
	}

	return rootVolume, nonRootVolumes, warnings, errs
}

func blockDeviceMappingSpecToVolume(fldPath *field.Path, bdm mapiv1.BlockDeviceMappingSpec, rootVolume bool) (capav1.Volume, []string, field.ErrorList) {
	errs := field.ErrorList{}
	warnings := []string{}

	if bdm.EBS == nil {
		return capav1.Volume{}, warnings, field.ErrorList{field.Invalid(fldPath.Child("ebs"), bdm.EBS, "missing ebs configuration for block device")}
	}

	capiKMSKey := convertKMSKeyToCAPI(bdm.EBS.KMSKey)

	if rootVolume && !ptr.Deref(bdm.EBS.DeleteOnTermination, true) {
		warnings = append(warnings, field.Invalid(fldPath.Child("ebs", "deleteOnTermination"), bdm.EBS.DeleteOnTermination, "root volume must be deleted on termination, ignoring invalid value false").Error())
	} else if !rootVolume && !ptr.Deref(bdm.EBS.DeleteOnTermination, true) {
		// TODO(OCPCLOUD-2717): We should support a non-true value for non-root volumes for feature parity.
		errs = append(errs, field.Invalid(fldPath.Child("ebs", "deleteOnTermination"), bdm.EBS.DeleteOnTermination, "non-root volumes must be deleted on termination, unsupported value false"))
	}

	if len(errs) > 0 {
		return capav1.Volume{}, warnings, errs
	}

	return capav1.Volume{
		DeviceName:    ptr.Deref(bdm.DeviceName, ""),
		Size:          ptr.Deref(bdm.EBS.VolumeSize, 120), // The installer uses 120GiB by default as of 4.19.
		Type:          capav1.VolumeType(ptr.Deref(bdm.EBS.VolumeType, "")),
		IOPS:          ptr.Deref(bdm.EBS.Iops, 0),
		Encrypted:     bdm.EBS.Encrypted,
		EncryptionKey: capiKMSKey,
	}, warnings, nil
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

func convertNetworkInterfaceType(networkInterfaceType mapiv1.AWSNetworkInterfaceType) capav1.NetworkInterfaceType {
	switch networkInterfaceType {
	case mapiv1.AWSENANetworkInterfaceType:
		return capav1.NetworkInterfaceTypeENI
	case mapiv1.AWSEFANetworkInterfaceType:
		return capav1.NetworkInterfaceTypeEFAWithENAInterface
	}

	return ""
}

// instanceIDFromProviderID extracts the instanceID from the ProviderID.
func instanceIDFromProviderID(s string) string {
	parts := strings.Split(s, "/")
	lastPart := parts[len(parts)-1]

	return regexp.MustCompile(`i-.*$`).FindString(lastPart)
}

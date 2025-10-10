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
	"fmt"
	"reflect"
	"regexp"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"

	"github.com/openshift/cluster-capi-operator/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	// DefaultCredentialsSecretName is the name of the default secret containing AWS cloud credentials.
	DefaultCredentialsSecretName = "aws-cloud-credentials" //#nosec G101 -- This is a false positive.
)

const (
	awsMachineKind         = "AWSMachine"
	awsMachineTemplateKind = "AWSMachineTemplate"

	errUnsupportedMAPIMarketType = "unable to convert market type, unknown value"
)

// awsMachineAndInfra stores the details of a Machine API AWSMachine and Infra.
type awsMachineAndInfra struct {
	machine        *mapiv1beta1.Machine
	infrastructure *configv1.Infrastructure
}

// awsMachineSetAndInfra stores the details of a Machine API AWSMachine set and Infra.
type awsMachineSetAndInfra struct {
	machineSet     *mapiv1beta1.MachineSet
	infrastructure *configv1.Infrastructure
	*awsMachineAndInfra
}

// FromAWSMachineAndInfra wraps a Machine API Machine for AWS and the OCP Infrastructure object into a mapi2capi AWSProviderSpec.
func FromAWSMachineAndInfra(m *mapiv1beta1.Machine, i *configv1.Infrastructure) Machine {
	return &awsMachineAndInfra{machine: m, infrastructure: i}
}

// FromAWSMachineSetAndInfra wraps a Machine API MachineSet for AWS and the OCP Infrastructure object into a mapi2capi AWSProviderSpec.
func FromAWSMachineSetAndInfra(m *mapiv1beta1.MachineSet, i *configv1.Infrastructure) MachineSet {
	return &awsMachineSetAndInfra{
		machineSet:     m,
		infrastructure: i,
		awsMachineAndInfra: &awsMachineAndInfra{
			machine: &mapiv1beta1.Machine{
				Spec: m.Spec.Template.Spec,
			},
			infrastructure: i,
		},
	}
}

// ToMachineAndInfrastructureMachine is used to generate a CAPI Machine and the corresponding InfrastructureMachine
// from the stored MAPI Machine and Infrastructure objects.
func (m *awsMachineAndInfra) ToMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, error) {
	capiMachine, capaMachine, warnings, errs := m.toMachineAndInfrastructureMachine()

	if len(errs) > 0 {
		return nil, nil, warnings, errs.ToAggregate()
	}

	return capiMachine, capaMachine, warnings, nil
}

func (m *awsMachineAndInfra) toMachineAndInfrastructureMachine() (*clusterv1.Machine, client.Object, []string, field.ErrorList) {
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

	capiMachine, machineErrs := fromMAPIMachineToCAPIMachine(m.machine, awsv1.GroupVersion.String(), awsMachineKind)
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
		capiMachine.Spec.Bootstrap = clusterv1.Bootstrap{
			DataSecretName: &awsProviderConfig.UserDataSecret.Name,
		}
	}

	// Populate the CAPI Machine ClusterName from the OCP Infrastructure object.
	if m.infrastructure == nil || m.infrastructure.Status.InfrastructureName == "" {
		errs = append(errs, field.Invalid(field.NewPath("infrastructure", "status", "infrastructureName"), m.infrastructure.Status.InfrastructureName, "infrastructure cannot be nil and infrastructure.Status.InfrastructureName cannot be empty"))
	} else {
		capiMachine.Spec.ClusterName = m.infrastructure.Status.InfrastructureName
		capiMachine.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
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
func (m *awsMachineSetAndInfra) ToMachineSetAndMachineTemplate() (*clusterv1.MachineSet, client.Object, []string, error) {
	var (
		errs     []error
		warnings []string
	)

	capiMachine, capaMachineObj, warn, errList := m.toMachineAndInfrastructureMachine()
	if errList != nil {
		errs = append(errs, errList.ToAggregate().Errors()...)
	}

	warnings = append(warnings, warn...)

	capaMachine, ok := capaMachineObj.(*awsv1.AWSMachine)
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
		capiMachineSet.Labels[clusterv1.ClusterNameLabel] = m.infrastructure.Status.InfrastructureName
	}

	if len(errs) > 0 {
		return nil, nil, warnings, utilerrors.NewAggregate(errs)
	}

	return capiMachineSet, capaMachineTemplate, warnings, nil
}

// toAWSMachine implements the ProviderSpec conversion interface for the AWS provider,
// it converts AWSMachineProviderConfig to AWSMachine.
//
//nolint:funlen
func (m *awsMachineAndInfra) toAWSMachine(providerSpec mapiv1beta1.AWSMachineProviderConfig) (*awsv1.AWSMachine, []string, field.ErrorList) {
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

	spec := awsv1.AWSMachineSpec{
		AMI:                      capiAWSAMIReference,
		AdditionalSecurityGroups: convertAWSSecurityGroupstoCAPI(providerSpec.SecurityGroups),
		AdditionalTags:           convertAWSTagsToCAPI(providerSpec.Tags),
		IAMInstanceProfile:       convertIAMInstanceProfiletoCAPI(providerSpec.IAMInstanceProfile),
		Ignition: &awsv1.Ignition{
			StorageType: awsv1.IgnitionStorageTypeOptionUnencryptedUserData, // Hardcoded for OpenShift.
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

	if providerSpec.NetworkInterfaceType != "" && providerSpec.NetworkInterfaceType != mapiv1beta1.AWSENANetworkInterfaceType && providerSpec.NetworkInterfaceType != mapiv1beta1.AWSEFANetworkInterfaceType {
		errs = append(errs, field.Invalid(fldPath.Child("networkInterfaceType"), providerSpec.NetworkInterfaceType, "networkInterface type must be one of ENA, EFA or omitted, unsupported value"))
	}

	if len(providerSpec.LoadBalancers) > 0 {
		// Load balancers are only supported for control plane machines
		if !util.IsControlPlaneMAPIMachine(m.machine) {
			errs = append(errs, field.Invalid(fldPath.Child("loadBalancers"), providerSpec.LoadBalancers, "loadBalancers are not supported for non-control plane machines"))
		}
	}

	return &awsv1.AWSMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: awsv1.GroupVersion.String(),
			Kind:       awsMachineKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.machine.Name,
			Namespace: capiNamespace,
		},
		Spec: spec,
	}, warnings, errs
}

// AWSProviderSpecFromRawExtension unmarshals a raw extension into an AWSMachineProviderConfig type.
func AWSProviderSpecFromRawExtension(rawExtension *runtime.RawExtension) (mapiv1beta1.AWSMachineProviderConfig, error) {
	if rawExtension == nil {
		return mapiv1beta1.AWSMachineProviderConfig{}, nil
	}

	spec := mapiv1beta1.AWSMachineProviderConfig{}
	if err := yaml.Unmarshal(rawExtension.Raw, &spec); err != nil {
		return mapiv1beta1.AWSMachineProviderConfig{}, fmt.Errorf("error unmarshalling providerSpec: %w", err)
	}

	return spec, nil
}

func awsMachineToAWSMachineTemplate(awsMachine *awsv1.AWSMachine, name string, namespace string) (*awsv1.AWSMachineTemplate, error) {
	nameWithHash, err := util.GenerateInfraMachineTemplateNameWithSpecHash(name, awsMachine.Spec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate infrastructure machine template name with spec hash: %w", err)
	}

	return &awsv1.AWSMachineTemplate{
		TypeMeta: metav1.TypeMeta{
			APIVersion: awsv1.GroupVersion.String(),
			Kind:       awsMachineTemplateKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameWithHash,
			Namespace: namespace,
		},
		Spec: awsv1.AWSMachineTemplateSpec{
			Template: awsv1.AWSMachineTemplateResource{
				Spec: awsMachine.Spec,
			},
		},
	}, nil
}

//////// Conversion helpers

func convertAWSAMIResourceReferenceToCAPI(fldPath *field.Path, amiRef mapiv1beta1.AWSResourceReference) (awsv1.AMIReference, *field.Error) {
	if amiRef.ARN != nil {
		return awsv1.AMIReference{}, field.Invalid(fldPath.Child("arn"), amiRef.ARN, "unable to convert AMI ARN reference. Not supported in CAPI")
	}

	if len(amiRef.Filters) > 0 {
		return awsv1.AMIReference{}, field.Invalid(fldPath.Child("filters"), amiRef.Filters, "unable to convert AMI Filters reference. Not supported in CAPI")
	}

	if amiRef.ID != nil {
		return awsv1.AMIReference{ID: amiRef.ID}, nil
	}

	return awsv1.AMIReference{}, field.Invalid(fldPath, amiRef, "unable to find a valid AMI resource reference")
}

func convertAWSTagsToCAPI(mapiTags []mapiv1beta1.TagSpecification) awsv1.Tags {
	if mapiTags == nil {
		return nil
	}

	capiTags := map[string]string{}
	for _, tag := range mapiTags {
		capiTags[tag.Name] = tag.Value
	}

	return capiTags
}

func convertAWSMarketTypeToCAPI(fldPath *field.Path, marketType mapiv1beta1.MarketType) (awsv1.MarketType, *field.Error) {
	switch marketType {
	case mapiv1beta1.MarketTypeOnDemand:
		return awsv1.MarketTypeOnDemand, nil
	case mapiv1beta1.MarketTypeSpot:
		return awsv1.MarketTypeSpot, nil
	case mapiv1beta1.MarketTypeCapacityBlock:
		return awsv1.MarketTypeCapacityBlock, nil
	case "":
		return "", nil
	default:
		return "", field.Invalid(fldPath, marketType, errUnsupportedMAPIMarketType)
	}
}

func convertMetadataServiceOptionstoCAPI(fldPath *field.Path, metad mapiv1beta1.MetadataServiceOptions) (*awsv1.InstanceMetadataOptions, *field.Error) {
	var httpTokens awsv1.HTTPTokensState

	switch metad.Authentication {
	case mapiv1beta1.MetadataServiceAuthenticationOptional:
		httpTokens = awsv1.HTTPTokensStateOptional
	case mapiv1beta1.MetadataServiceAuthenticationRequired:
		httpTokens = awsv1.HTTPTokensStateRequired
	case "":
		httpTokens = awsv1.HTTPTokensStateOptional // TODO(docs): CAPA defaults to optional (in the openAPI spec validation) if the field is empty, lossy translation to document.
	default:
		return &awsv1.InstanceMetadataOptions{}, field.Invalid(fldPath.Child("authentication"), metad.Authentication, "unsupported authentication value")
	}

	capiMetadataOpts := &awsv1.InstanceMetadataOptions{
		HTTPEndpoint: awsv1.InstanceMetadataEndpointStateEnabled, // not present in MAPI, fallback to CAPI default.
		// HTTPPutResponseHopLimit: not present in MAPI, fallback to CAPI default.
		InstanceMetadataTags:    awsv1.InstanceMetadataEndpointStateDisabled, // not present in MAPI, fallback to CAPI default.
		HTTPTokens:              httpTokens,
		HTTPPutResponseHopLimit: 1, // TODO(docs): CAPA defaults to 1 (in the openAPI spec validation) if the field is empty, lossy translation to document.
	}

	return capiMetadataOpts, nil
}

func convertIAMInstanceProfiletoCAPI(mapiIAM *mapiv1beta1.AWSResourceReference) string {
	if mapiIAM == nil || mapiIAM.ID == nil {
		return ""
	}

	return *mapiIAM.ID
}

func convertAWSSpotMarketOptionsToCAPI(mapiSpotMarketOptions *mapiv1beta1.SpotMarketOptions) *awsv1.SpotMarketOptions {
	if mapiSpotMarketOptions == nil {
		return nil
	}

	return &awsv1.SpotMarketOptions{
		MaxPrice: mapiSpotMarketOptions.MaxPrice,
	}
}

func convertAWSSecurityGroupstoCAPI(sgs []mapiv1beta1.AWSResourceReference) []awsv1.AWSResourceReference {
	capiSGs := []awsv1.AWSResourceReference{}

	for _, sg := range sgs {
		ref := convertAWSResourceReferenceToCAPI(sg)

		capiSGs = append(capiSGs, *ref)
	}

	return capiSGs
}

func convertAWSBlockDeviceMappingSpecToCAPI(fldPath *field.Path, mapiBlockDeviceMapping []mapiv1beta1.BlockDeviceMappingSpec) (*awsv1.Volume, []awsv1.Volume, []string, field.ErrorList) {
	// We do not want to preallocate this as we need it nil if no elements are added.
	//nolint:prealloc
	var nonRootVolumes []awsv1.Volume

	rootVolume := &awsv1.Volume{}
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

func blockDeviceMappingSpecToVolume(fldPath *field.Path, bdm mapiv1beta1.BlockDeviceMappingSpec, rootVolume bool) (awsv1.Volume, []string, field.ErrorList) {
	errs := field.ErrorList{}
	warnings := []string{}

	if bdm.EBS == nil {
		return awsv1.Volume{}, warnings, field.ErrorList{field.Invalid(fldPath.Child("ebs"), bdm.EBS, "missing ebs configuration for block device")}
	}

	capiKMSKey := convertKMSKeyToCAPI(bdm.EBS.KMSKey)

	if rootVolume && !ptr.Deref(bdm.EBS.DeprecatedDeleteOnTermination, true) {
		warnings = append(warnings, field.Invalid(fldPath.Child("ebs", "deleteOnTermination"), bdm.EBS.DeprecatedDeleteOnTermination, "root volume must be deleted on termination, ignoring invalid value false").Error())
	} else if !rootVolume && !ptr.Deref(bdm.EBS.DeprecatedDeleteOnTermination, true) {
		// TODO(OCPCLOUD-2717): We should support a non-true value for non-root volumes for feature parity.
		errs = append(errs, field.Invalid(fldPath.Child("ebs", "deleteOnTermination"), bdm.EBS.DeprecatedDeleteOnTermination, "non-root volumes must be deleted on termination, unsupported value false"))
	}

	if len(errs) > 0 {
		return awsv1.Volume{}, warnings, errs
	}

	return awsv1.Volume{
		DeviceName:    ptr.Deref(bdm.DeviceName, ""),
		Size:          ptr.Deref(bdm.EBS.VolumeSize, 120), // The installer uses 120GiB by default as of 4.19.
		Type:          awsv1.VolumeType(ptr.Deref(bdm.EBS.VolumeType, "")),
		IOPS:          ptr.Deref(bdm.EBS.Iops, 0),
		Encrypted:     bdm.EBS.Encrypted,
		EncryptionKey: capiKMSKey,
	}, warnings, nil
}

func convertKMSKeyToCAPI(kmsKey mapiv1beta1.AWSResourceReference) string {
	if kmsKey.ID != nil {
		return *kmsKey.ID
	}

	if kmsKey.ARN != nil {
		return *kmsKey.ARN
	}

	return ""
}

func convertAWSResourceReferenceToCAPI(mapiReference mapiv1beta1.AWSResourceReference) *awsv1.AWSResourceReference {
	return &awsv1.AWSResourceReference{
		ID:      mapiReference.ID,
		Filters: convertAWSFiltersToCAPI(mapiReference.Filters),
	}
}

func convertAWSFiltersToCAPI(mapiFilters []mapiv1beta1.Filter) []awsv1.Filter {
	capiFilters := []awsv1.Filter{}
	for _, filter := range mapiFilters {
		capiFilters = append(capiFilters, awsv1.Filter{
			Name:   filter.Name,
			Values: filter.Values,
		})
	}

	return capiFilters
}

func convertNetworkInterfaceType(networkInterfaceType mapiv1beta1.AWSNetworkInterfaceType) awsv1.NetworkInterfaceType {
	switch networkInterfaceType {
	case mapiv1beta1.AWSENANetworkInterfaceType:
		return awsv1.NetworkInterfaceTypeENI
	case mapiv1beta1.AWSEFANetworkInterfaceType:
		return awsv1.NetworkInterfaceTypeEFAWithENAInterface
	}

	return ""
}

// instanceIDFromProviderID extracts the instanceID from the ProviderID.
func instanceIDFromProviderID(s string) string {
	parts := strings.Split(s, "/")
	lastPart := parts[len(parts)-1]

	return regexp.MustCompile(`i-.*$`).FindString(lastPart)
}

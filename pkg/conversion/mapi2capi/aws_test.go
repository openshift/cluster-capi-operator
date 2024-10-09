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
	"encoding/json"
	"fmt"

	mapiv1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	configv1 "github.com/openshift/api/config/v1"
	configbuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
)

var (
	awsDefaultRegion    = "eu-west-3"
	awsNonDefaultRegion = "us-west-2"

	// Machines configurations.
	awsBaseProviderSpec   = machinebuilder.AWSProviderSpec().WithLoadBalancers(nil)
	awsMAPIMachineBase    = machinebuilder.Machine().WithProviderSpecBuilder(awsBaseProviderSpec)
	awsMAPIMachineWithLBs = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithLoadBalancers(
		[]mapiv1.LoadBalancerReference{{Name: "a", Type: mapiv1.ClassicLoadBalancerType}}))
	awsMAPIMachineWithNonZeroDeviceIndex = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithDeviceIndex(1))
	awsMAPIMachineWithMismatchedRegion   = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithRegion(awsNonDefaultRegion))
	awsMAPIMachineWithMetadata           = awsMAPIMachineBase.WithProviderSpec(mapiv1.ProviderSpec{
		Value: mustConvertAWSProviderSpecToRawExtension(&mapiv1.AWSMachineProviderConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "test"}}),
	})
	awsMAPIMachineWithInvalidNIC = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithNetworkInterfaceType("unsupported"))
	awsMAPIMachineWithAMIARN     = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithAMI(mapiv1.AWSResourceReference{
		ARN: ptr.To("arn:aws:ec2:us-east-1::image/ami-1234567890abcdef0"),
	}))
	awsMAPIMachineWithAMIFilter = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithAMI(mapiv1.AWSResourceReference{
		Filters: []mapiv1.Filter{{Name: "name", Values: []string{"test"}}},
	}))
	awsMAPIMachineWithMissingAMI      = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithAMI(mapiv1.AWSResourceReference{}))
	awsMAPIMachineWithInvalidMetadata = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithMetadataServiceOptions(mapiv1.MetadataServiceOptions{
		Authentication: "unsupported",
	}))
	awsMAPIMachineWithMissingVolumeSize = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithBlockDevices([]mapiv1.BlockDeviceMappingSpec{
		{EBS: &mapiv1.EBSBlockDeviceSpec{}},
	}))
	awsMAPIMachineWithNonRootVolumeNotDeleted = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithBlockDevices([]mapiv1.BlockDeviceMappingSpec{
		{DeviceName: ptr.To("/dev/sdb"), EBS: &mapiv1.EBSBlockDeviceSpec{VolumeSize: ptr.To(int64(10)), DeleteOnTermination: ptr.To(false)}},
	}))
	awsMAPIMachineWithNoDevice = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithBlockDevices([]mapiv1.BlockDeviceMappingSpec{
		{NoDevice: ptr.To("test"), EBS: &mapiv1.EBSBlockDeviceSpec{VolumeSize: ptr.To(int64(10))}},
	}))
	awsMAPIMachineWithVirtualName = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithBlockDevices([]mapiv1.BlockDeviceMappingSpec{
		{VirtualName: ptr.To("test"), EBS: &mapiv1.EBSBlockDeviceSpec{VolumeSize: ptr.To(int64(10))}},
	}))
	awsMAPIMachineWithVirtualNameAndRootVolNotDeletedOnTerm = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithBlockDevices([]mapiv1.BlockDeviceMappingSpec{
		{VirtualName: ptr.To("test"), EBS: &mapiv1.EBSBlockDeviceSpec{VolumeSize: ptr.To(int64(10)), DeleteOnTermination: ptr.To(false)}},
	}))
	awsMAPIMachineWithVirtualNameAndMissingEBSConfig = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithBlockDevices([]mapiv1.BlockDeviceMappingSpec{
		{VirtualName: ptr.To("test")},
	}))
	awsMAPIMachineWithMissingEBSConfig = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithBlockDevices([]mapiv1.BlockDeviceMappingSpec{
		{DeviceName: ptr.To("/dev/sdb")},
	}))
	awsMAPIMachineWithRootVolumeNotDeleted = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithBlockDevices([]mapiv1.BlockDeviceMappingSpec{
		{EBS: &mapiv1.EBSBlockDeviceSpec{VolumeSize: ptr.To(int64(10)), DeleteOnTermination: ptr.To(false)}},
	}))
	awsMAPIMachineWithNoDeviceAndVirtualName = awsMAPIMachineBase.WithProviderSpecBuilder(awsBaseProviderSpec.WithBlockDevices([]mapiv1.BlockDeviceMappingSpec{
		{VirtualName: ptr.To("test"), NoDevice: ptr.To("test"), EBS: &mapiv1.EBSBlockDeviceSpec{VolumeSize: ptr.To(int64(10))}},
	}))

	// MachineSets configurations.
	awsMAPIMachineSetBase = machinebuilder.MachineSet().WithProviderSpecBuilder(awsBaseProviderSpec)

	// OCP Infrastructure configurations.
	infra = &configv1.Infrastructure{
		Spec:   configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{InfrastructureName: "sample-cluster-name"},
	}
	infraWithRegion = configbuilder.Infrastructure().AsAWS("sample-cluster-name", awsDefaultRegion)
)

var _ = DescribeTable("mapi2capi AWS convert MAPI Machine",
	func(m *mapiv1.Machine, i *configv1.Infrastructure, errorsMatcher types.GomegaMatcher, warningsMatcher types.GomegaMatcher) {
		_, _, warns, err := FromAWSMachineAndInfra(m, i).ToMachineAndInfrastructureMachine()
		Expect(err).To(errorsMatcher, "should have been able to convert providerSpec to MachineTemplateSpec")
		Expect(warns).To(warningsMatcher, "should have not warned while converting providerSpec to MachineTemplateSpec")
	},
	// Base Case.
	Entry("With a Base configuration", awsMAPIMachineBase.Build(), infra, BeNil(), BeEmpty()),
	// Only Error.
	Entry("With LoadBalancers", awsMAPIMachineWithLBs.Build(), infra, MatchError(ContainSubstring(errLoadbalancersNotSupported)), BeEmpty()),
	Entry("With DeviceIndex non-zero", awsMAPIMachineWithNonZeroDeviceIndex.Build(), infra, MatchError(ContainSubstring(errDeviceIndexMustBeZero)), BeEmpty()),
	Entry("With mismatched region", awsMAPIMachineWithMismatchedRegion.Build(), infraWithRegion.Build(), MatchError(ContainSubstring(errPlacementRegionShouldMatchInfrastructureStatusValue)), BeEmpty()),
	Entry("With metadata in provider spec", awsMAPIMachineWithMetadata.Build(), infra, MatchError(ContainSubstring(errMetadataNotSupported)), BeEmpty()),
	Entry("With unsupported network interface type", awsMAPIMachineWithInvalidNIC.Build(), infra, MatchError(ContainSubstring(errNetworkInterfaceUnsupportedValue)), BeEmpty()),
	Entry("With AMI ARN reference", awsMAPIMachineWithAMIARN.Build(), infra, MatchError(ContainSubstring(errUnableToConvertUnsupportedAMIARNReference)), BeEmpty()),
	Entry("With AMI filters", awsMAPIMachineWithAMIFilter.Build(), infra, MatchError(ContainSubstring(errUnableToConvertUnsupportedAMIFilterReference)), BeEmpty()),
	Entry("With missing AMI reference", awsMAPIMachineWithMissingAMI.Build(), infra, MatchError(ContainSubstring(errUnableToFindAMIReference)), BeEmpty()),
	Entry("With unsupported Metadata Authentication", awsMAPIMachineWithInvalidMetadata.Build(), infra, MatchError(ContainSubstring(errUnsupportedAuthenticationValue)), BeEmpty()),
	Entry("With missing Volume size for EBS", awsMAPIMachineWithMissingVolumeSize.Build(), infra, MatchError(ContainSubstring(errVolumeSizeRequiredButMissing)), BeEmpty()),
	Entry("With non-root Volume not deleted on termination", awsMAPIMachineWithNonRootVolumeNotDeleted.Build(), infra, MatchError(ContainSubstring(errNonRootVolumesMustBeDeletedOnTerminationUnsupportedValueFalse)), BeEmpty()),
	Entry("With NoDevice specified", awsMAPIMachineWithNoDevice.Build(), infra, MatchError(ContainSubstring(errNoDeviceIsNotSupported)), BeEmpty()),
	Entry("With VirtualName specified", awsMAPIMachineWithVirtualName.Build(), infra, MatchError(ContainSubstring(errVirtualNameNotSupported)), BeEmpty()),
	// Error + Warning.
	Entry("With VirtualName specified and missing EBS configuration", awsMAPIMachineWithVirtualNameAndMissingEBSConfig.Build(), infra, MatchError(ContainSubstring(errVirtualNameNotSupported)), ConsistOf(ContainSubstring(warnMissingBlockDeviceConf))),
	Entry("With VirtualName specified and root Volume not deleted on termination", awsMAPIMachineWithVirtualNameAndRootVolNotDeletedOnTerm.Build(), infra, MatchError(ContainSubstring(errVirtualNameNotSupported)), ConsistOf(ContainSubstring(warnRootVolumeMustBeDeletedOnTerminationIgnoringValueFalse))),
	// Double Errors.
	Entry("With NoDevice and VirtualName specified", awsMAPIMachineWithNoDeviceAndVirtualName.Build(), infra, ConsistOf(MatchError(ContainSubstring(errNoDeviceIsNotSupported)), MatchError(ContainSubstring(errVirtualNameNotSupported))), BeEmpty()),
	// Only Warnings.
	Entry("With missing EBS configuration", awsMAPIMachineWithMissingEBSConfig.Build(), infra, BeNil(), ConsistOf(ContainSubstring(warnMissingBlockDeviceConf))),
	Entry("With root Volume not deleted on termination", awsMAPIMachineWithRootVolumeNotDeleted.Build(), infra, BeNil(), ConsistOf(ContainSubstring(warnRootVolumeMustBeDeletedOnTerminationIgnoringValueFalse))),
)

var _ = DescribeTable("mapi2capi AWS convert MAPI MachineSet",
	func(ms *mapiv1.MachineSet, i *configv1.Infrastructure, errorsMatcher types.GomegaMatcher, warningsMatcher types.GomegaMatcher) {
		_, _, warns, err := FromAWSMachineSetAndInfra(ms, i).ToMachineSetAndMachineTemplate()
		Expect(err).To(errorsMatcher, "should have been able to convert MAPI MachineSet to CAPI MachineSet")
		Expect(warns).To(warningsMatcher, "should have not warned while converting MAPI MachineSet to CAPI MachineSet")
	},

	Entry("With a Base configuration", awsMAPIMachineSetBase.Build(), infra, BeNil(), BeEmpty()),
)

func mustConvertAWSProviderSpecToRawExtension(spec *mapiv1.AWSMachineProviderConfig) *runtime.RawExtension {
	if spec == nil {
		return &runtime.RawExtension{}
	}

	rawBytes, err := json.Marshal(spec)
	if err != nil {
		panic(fmt.Sprintf("unable to convert (marshal) test AWSProviderSpec to runtime.RawExtension: %v", err))
	}

	return &runtime.RawExtension{
		Raw: rawBytes,
	}
}

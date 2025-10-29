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

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	configbuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
)

var _ = Describe("mapi2capi AWS conversion", func() {
	var (
		testValue                          = ptr.To("test")
		blockDeviceMappingWithVirtualName  = &mapiv1beta1.BlockDeviceMappingSpec{VirtualName: testValue}
		blockDeviceMappingWithoutEBSConfig = &mapiv1beta1.BlockDeviceMappingSpec{DeviceName: ptr.To("/dev/sdb")}

		awsBaseProviderSpec   = machinebuilder.AWSProviderSpec().WithLoadBalancers(nil)
		awsMAPIMachineBase    = machinebuilder.Machine().WithProviderSpecBuilder(awsBaseProviderSpec)
		awsMAPIMachineSetBase = machinebuilder.MachineSet().WithProviderSpecBuilder(awsBaseProviderSpec)

		infraWithRegion = configbuilder.Infrastructure().AsAWS("sample-cluster-name", "eu-west-3").Build()
		infra           = &configv1.Infrastructure{
			Spec:   configv1.InfrastructureSpec{},
			Status: configv1.InfrastructureStatus{InfrastructureName: "sample-cluster-name"},
		}
	)

	type awsMAPI2CAPIConversionInput struct {
		machineBuilder   machinebuilder.MachineBuilder
		infra            *configv1.Infrastructure
		expectedErrors   []string
		expectedWarnings []string
	}

	type awsMAPI2CAPIMachinesetConversionInput struct {
		machineSetBuilder machinebuilder.MachineSetBuilder
		infra             *configv1.Infrastructure
		expectedErrors    []string
		expectedWarnings  []string
	}

	var mustConvertAWSProviderSpecToRawExtension = func(spec *mapiv1beta1.AWSMachineProviderConfig) *runtime.RawExtension {
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

	var _ = DescribeTable("mapi2capi AWS convert MAPI Machine",
		func(in awsMAPI2CAPIConversionInput) {
			_, _, warns, err := FromAWSMachineAndInfra(in.machineBuilder.Build(), in.infra).ToMachineAndInfrastructureMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors), "should match expected errors while converting an AWS MAPI Machine to CAPI")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings), "should match expected warnings while converting an AWS MAPI Machine to CAPI")
		},

		// Base Case.
		Entry("With a Base configuration", awsMAPI2CAPIConversionInput{
			machineBuilder:   awsMAPIMachineBase,
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		// Only Error.
		Entry("With LoadBalancers on worker machine", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithLoadBalancers(
					[]mapiv1beta1.LoadBalancerReference{{Name: "a", Type: mapiv1beta1.ClassicLoadBalancerType}},
				),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.loadBalancers: Invalid value: [{\"name\":\"a\",\"type\":\"classic\"}]: loadBalancers are not supported for non-control plane machines",
			},
			expectedWarnings: []string{},
		}),
		Entry("With LoadBalancers on control plane machine with role label", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithLabels(map[string]string{
				"machine.openshift.io/cluster-api-machine-role": "master",
			}).WithOwnerReferences(
				[]metav1.OwnerReference{
					{
						APIVersion: mapiv1beta1.SchemeGroupVersion.String(),
						Kind:       "ControlPlaneMachineSet",
						Name:       "cluster",
						UID:        "cpms-uid",
					},
				},
			).WithProviderSpecBuilder(
				awsBaseProviderSpec.WithLoadBalancers(
					[]mapiv1beta1.LoadBalancerReference{
						{Name: "ci-ln-d2q97ct-76ef8-httpb-int", Type: mapiv1beta1.NetworkLoadBalancerType},
						{Name: "ci-ln-d2q97ct-76ef8-httpb-ext", Type: mapiv1beta1.NetworkLoadBalancerType},
					},
				),
			),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
		Entry("With LoadBalancers on control plane machine with CPMS owner reference; no role label", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.
				WithOwnerReferences([]metav1.OwnerReference{
					{
						APIVersion: mapiv1beta1.SchemeGroupVersion.String(),
						Kind:       "ControlPlaneMachineSet",
						Name:       "cluster",
						UID:        "cpms-uid",
					},
				}).
				WithProviderSpecBuilder(
					awsBaseProviderSpec.WithLoadBalancers(
						[]mapiv1beta1.LoadBalancerReference{
							{Name: "ci-ln-d2q97ct-76ef8-httpb-int", Type: mapiv1beta1.NetworkLoadBalancerType},
							{Name: "ci-ln-d2q97ct-76ef8-httpb-ext", Type: mapiv1beta1.NetworkLoadBalancerType},
						},
					),
				),
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
		Entry("With DeviceIndex non-zero", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithDeviceIndex(1),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.deviceIndex: Invalid value: 1: deviceIndex must be 0 or unset",
			},
			expectedWarnings: []string{},
		}),
		Entry("With mismatched region", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithRegion("us-west-2"),
			),
			infra: infraWithRegion,
			expectedErrors: []string{
				"spec.providerSpec.value.placement.region: Invalid value: \"us-west-2\": placement.region should match infrastructure status value \"eu-west-3\"",
			},
			expectedWarnings: []string{},
		}),
		Entry("With metadata in provider spec", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpec(mapiv1beta1.ProviderSpec{
				Value: mustConvertAWSProviderSpecToRawExtension(&mapiv1beta1.AWSMachineProviderConfig{
					ObjectMeta: metav1.ObjectMeta{Name: "test"},
					AMI:        mapiv1beta1.AWSResourceReference{ARN: ptr.To("arn:aws:ec2:us-east-1::image/ami-1234567890abcdef0")},
				}),
			}),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.metadata: Invalid value: {\"name\":\"test\"}: metadata is not supported",
				"spec.providerSpec.value.ami.arn: Invalid value: \"arn:aws:ec2:us-east-1::image/ami-1234567890abcdef0\": unable to convert AMI ARN reference. Not supported in CAPI",
			},
			expectedWarnings: []string{},
		}),
		Entry("With unsupported network interface type", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithNetworkInterfaceType("unsupported-value"),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.networkInterfaceType: Invalid value: \"unsupported-value\": networkInterface type must be one of ENA, EFA or omitted, unsupported value",
			},
			expectedWarnings: []string{},
		}),
		Entry("With AMI ARN reference", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithAMI(mapiv1beta1.AWSResourceReference{
					ARN: ptr.To("arn:aws:ec2:us-east-1::image/ami-1234567890abcdef0"),
				}),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.ami.arn: Invalid value: \"arn:aws:ec2:us-east-1::image/ami-1234567890abcdef0\": unable to convert AMI ARN reference. Not supported in CAPI",
			},
			expectedWarnings: []string{},
		}),
		Entry("With AMI filters", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithAMI(mapiv1beta1.AWSResourceReference{
					Filters: []mapiv1beta1.Filter{{Name: "name", Values: []string{"test"}}},
				}),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.ami.filters: Invalid value: [{\"name\":\"name\",\"values\":[\"test\"]}]: unable to convert AMI Filters reference. Not supported in CAPI",
			},
			expectedWarnings: []string{},
		}),
		Entry("With missing AMI reference", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithAMI(
					mapiv1beta1.AWSResourceReference{},
				),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.ami: Invalid value: {}: unable to find a valid AMI resource reference",
			},
			expectedWarnings: []string{},
		}),
		Entry("With unsupported Metadata Authentication", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithMetadataServiceOptions(mapiv1beta1.MetadataServiceOptions{
					Authentication: "unsupported",
				}),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.metadataServiceOptions.authentication: Invalid value: \"unsupported\": unsupported authentication value",
			},
			expectedWarnings: []string{},
		}),
		Entry("With missing Volume size for EBS", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithBlockDevices([]mapiv1beta1.BlockDeviceMappingSpec{{
					EBS: &mapiv1beta1.EBSBlockDeviceSpec{},
				}}),
			),
			infra:            infra,
			expectedErrors:   []string{}, // No error is expected anymore, because volumeSize is now set to a default amount if nil.
			expectedWarnings: []string{},
		}),
		Entry("With non-root Volume not deleted on termination", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithBlockDevices([]mapiv1beta1.BlockDeviceMappingSpec{{
					DeviceName: ptr.To("/dev/sdb"),
					EBS: &mapiv1beta1.EBSBlockDeviceSpec{
						VolumeSize:                    ptr.To(int64(10)),
						DeprecatedDeleteOnTermination: ptr.To(false), // Explicitly set to trigger validation error
					},
				}}),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.blockDevices[0].ebs.deleteOnTermination: Invalid value: false: non-root volumes must be deleted on termination, unsupported value false",
			},
			expectedWarnings: []string{},
		}),
		Entry("With NoDevice specified", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithBlockDevices([]mapiv1beta1.BlockDeviceMappingSpec{{
					NoDevice: testValue,
					EBS:      &mapiv1beta1.EBSBlockDeviceSpec{VolumeSize: ptr.To(int64(10))},
				}}),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.blockDevices[0].noDevice: Invalid value: \"test\": noDevice is not supported",
			},
			expectedWarnings: []string{},
		}),
		Entry("With VirtualName specified", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithBlockDevices([]mapiv1beta1.BlockDeviceMappingSpec{{
					VirtualName: testValue,
					EBS:         &mapiv1beta1.EBSBlockDeviceSpec{VolumeSize: ptr.To(int64(10))},
				}}),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.blockDevices[0].virtualName: Invalid value: \"test\": virtualName is not supported",
			},
			expectedWarnings: []string{},
		}),
		Entry("With non-default CredentialsSecret", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithCredentialsSecret(&corev1.LocalObjectReference{
					Name: "invalid",
				}),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.credentialsSecret: Invalid value: \"invalid\": credential secret does not match the default of \"aws-cloud-credentials\", manual conversion is necessary",
			},
			expectedWarnings: []string{},
		}),
		// Error + Warning.
		Entry("With VirtualName specified and missing EBS configuration", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithBlockDevices([]mapiv1beta1.BlockDeviceMappingSpec{*blockDeviceMappingWithVirtualName}),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.blockDevices[0].virtualName: Invalid value: \"test\": virtualName is not supported",
			},
			expectedWarnings: []string{
				"spec.providerSpec.value.blockDevices[0].ebs: Invalid value: null: missing ebs configuration for block device",
			},
		}),
		Entry("With VirtualName specified and root Volume not deleted on termination", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithBlockDevices([]mapiv1beta1.BlockDeviceMappingSpec{{
					VirtualName: testValue,
					EBS: &mapiv1beta1.EBSBlockDeviceSpec{
						VolumeSize:                    ptr.To(int64(10)),
						DeprecatedDeleteOnTermination: ptr.To(false), // Explicitly set to trigger validation error
					},
				}}),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.blockDevices[0].virtualName: Invalid value: \"test\": virtualName is not supported",
			},
			expectedWarnings: []string{
				"spec.providerSpec.value.blockDevices[0].ebs.deleteOnTermination: Invalid value: false: root volume must be deleted on termination, ignoring invalid value false",
			},
		}),
		// Double Errors.
		Entry("With NoDevice and VirtualName specified", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithBlockDevices([]mapiv1beta1.BlockDeviceMappingSpec{{
					VirtualName: testValue,
					NoDevice:    testValue,
					EBS:         &mapiv1beta1.EBSBlockDeviceSpec{VolumeSize: ptr.To(int64(10))},
				}}),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.blockDevices[0].noDevice: Invalid value: \"test\": noDevice is not supported",
				"spec.providerSpec.value.blockDevices[0].virtualName: Invalid value: \"test\": virtualName is not supported",
			},
			expectedWarnings: []string{},
		}),

		// Only Warnings.
		Entry("With missing EBS configuration", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithBlockDevices([]mapiv1beta1.BlockDeviceMappingSpec{*blockDeviceMappingWithoutEBSConfig}),
			),
			infra:          infra,
			expectedErrors: []string{},
			expectedWarnings: []string{
				"spec.providerSpec.value.blockDevices[0].ebs: Invalid value: null: missing ebs configuration for block device",
			},
		}),
		Entry("With root Volume not deleted on termination", awsMAPI2CAPIConversionInput{
			machineBuilder: awsMAPIMachineBase.WithProviderSpecBuilder(
				awsBaseProviderSpec.WithBlockDevices([]mapiv1beta1.BlockDeviceMappingSpec{{
					EBS: &mapiv1beta1.EBSBlockDeviceSpec{
						VolumeSize:                    ptr.To(int64(10)),
						DeprecatedDeleteOnTermination: ptr.To(false), // Explicitly set to trigger validation error
					},
				}}),
			),
			infra:          infra,
			expectedErrors: []string{},
			expectedWarnings: []string{
				"spec.providerSpec.value.blockDevices[0].ebs.deleteOnTermination: Invalid value: false: root volume must be deleted on termination, ignoring invalid value false",
			},
		}),
	)

	var _ = DescribeTable("mapi2capi AWS convert MAPI MachineSet",
		func(in awsMAPI2CAPIMachinesetConversionInput) {
			_, _, warns, err := FromAWSMachineSetAndInfra(in.machineSetBuilder.Build(), in.infra).ToMachineSetAndMachineTemplate()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors), "should match expected errors while converting an AWS MAPI MachineSet to CAPI")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings), "should match expected warnings while converting an AWS MAPI MachineSet to CAPI")
		},

		Entry("With a Base configuration", awsMAPI2CAPIMachinesetConversionInput{
			machineSetBuilder: awsMAPIMachineSetBase,
			infra:             infra,
			expectedErrors:    []string{},
			expectedWarnings:  []string{},
		}),
	)

})

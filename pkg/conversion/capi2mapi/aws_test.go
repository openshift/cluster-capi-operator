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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	capibuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	capabuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
	"k8s.io/utils/ptr"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
)

var _ = Describe("capi2mapi AWS conversion", func() {
	var (
		awsCAPIMachineBase    = capibuilder.Machine()
		awsCAPIAWSMachineBase = capabuilder.AWSMachine()
		awsCAPIAWSClusterBase = capabuilder.AWSCluster()
	)

	type awsCAPI2MAPIMachineConversionInput struct {
		machineBuilder    capibuilder.MachineBuilder
		awsMachineBuilder capabuilder.AWSMachineBuilder
		awsClusterBuilder capabuilder.AWSClusterBuilder
		expectedErrors    []string
		expectedWarnings  []string
	}

	type awsCAPI2MAPIMachinesetConversionInput struct {
		machineSetBuilder         capibuilder.MachineSetBuilder
		awsMachineTemplateBuilder capabuilder.AWSMachineTemplateBuilder
		awsClusterBuilder         capabuilder.AWSClusterBuilder
		expectedErrors            []string
		expectedWarnings          []string
	}

	var _ = DescribeTable("capi2mapi AWS convert CAPI Machine/InfraMachine/InfraCluster to a MAPI Machine",
		func(in awsCAPI2MAPIMachineConversionInput) {
			_, warns, err := FromMachineAndAWSMachineAndAWSCluster(
				in.machineBuilder.Build(),
				in.awsMachineBuilder.Build(),
				in.awsClusterBuilder.Build(),
			).ToMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting AWS CAPI resources to MAPI Machine")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting AWS CAPI resources to MAPI Machine")
		},

		// Base Case.
		Entry("With a Base configuration", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase,
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{},
			expectedWarnings:  []string{},
		}),
		Entry("With unsupported EKSOptimizedLookupType", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: capabuilder.AWSMachine().
				WithAMI(capav1.AMIReference{
					EKSOptimizedLookupType: ptr.To(capav1.EKSAMILookupType("unsupported")),
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.ami.eksOptimizedLookupType: Invalid value: \"unsupported\": eksOptimizedLookupType is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported ImageLookupFormat", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.WithImageLookupFormat("unsupported"),
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{"spec.imageLookupFormat: Invalid value: \"unsupported\": imageLookupFormat is not supported"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported ImageLookupOrg", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.WithImageLookupOrg("unsupported"),
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{"spec.imageLookupOrg: Invalid value: \"unsupported\": imageLookupOrg is not supported"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported ImageLookupBaseOS", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.WithImageLookupBaseOS("unsupported"),
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{"spec.imageLookupBaseOS: Invalid value: \"unsupported\": imageLookupBaseOS is not supported"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported SecurityGroupOverrides", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.WithSecurityGroupOverrides(map[capav1.SecurityGroupRole]string{"sg-1": "sg-2"}),
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{"spec.securityGroupOverrides: Invalid value: map[v1beta2.SecurityGroupRole]string{\"sg-1\":\"sg-2\"}: securityGroupOverrides are not supported"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported NetworkInterfaces", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithNetworkInterfaces([]string{"eni-12345", "eni-67890"}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.networkInterfaces: Invalid value: []string{\"eni-12345\", \"eni-67890\"}: networkInterfaces are not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported NetworkInterfaceType", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.WithNetworkInterfaceType("unsupported-networkInterfaceType"),
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{"spec.networkInterfaceType: Invalid value: \"unsupported-networkInterfaceType\": networkInterface type must be one of interface, efa or omitted, unsupported value"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported UncompressedUserData", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.WithUncompressedUserData(ptr.To(true)),
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{"spec.uncompressedUserData: Invalid value: true: uncompressedUserData is not supported"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported CloudInit", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.WithCloudInit(capav1.CloudInit{InsecureSkipSecretsManager: true}),
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{"spec.cloudInit: Invalid value: v1beta2.CloudInit{InsecureSkipSecretsManager:true, SecretCount:0, SecretPrefix:\"\", SecureSecretsBackend:\"\"}: cloudInit is not supported"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported PrivateDNSName", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.WithPrivateDNSName(&capav1.PrivateDNSName{}),
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{"spec.privateDNSName: Invalid value: v1beta2.PrivateDNSName{EnableResourceNameDNSAAAARecord:(*bool)(nil), EnableResourceNameDNSARecord:(*bool)(nil), HostnameType:(*string)(nil)}: privateDNSName is not supported"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported Ignition Proxy", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithIgnition(&capav1.Ignition{
					Proxy: &capav1.IgnitionProxy{},
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.ignition.proxy: Invalid value: v1beta2.IgnitionProxy{HTTPProxy:(*string)(nil), HTTPSProxy:(*string)(nil), NoProxy:[]v1beta2.IgnitionNoProxy(nil)}: ignition proxy is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported Ignition TLS", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithIgnition(&capav1.Ignition{
					TLS: &capav1.IgnitionTLS{
						CASources: []capav1.IgnitionCASource{"a", "b"},
					},
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.ignition.tls: Invalid value: v1beta2.IgnitionTLS{CASources:[]v1beta2.IgnitionCASource{\"a\", \"b\"}}: ignition tls is not supported"},
			expectedWarnings: []string{},
		}),
		Entry("With unsupported httpEndpoint", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithInstanceMetadataOptions(&capav1.InstanceMetadataOptions{
					HTTPEndpoint: capav1.InstanceMetadataEndpointStateDisabled,
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.instanceMetadataOptions.httpEndpoint: Invalid value: \"disabled\": httpEndpoint values other than \"enabled\" are not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported httpPutResponseHopLimit", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithInstanceMetadataOptions(&capav1.InstanceMetadataOptions{
					HTTPPutResponseHopLimit: 2,
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.instanceMetadataOptions.httpPutResponseHopLimit: Invalid value: 2: httpPutResponseHopLimit values other than 1 are not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported httpTokens", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithInstanceMetadataOptions(&capav1.InstanceMetadataOptions{
					HTTPTokens: "unsupported",
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.instanceMetadataOptions.httpTokens: Invalid value: \"unsupported\": unable to convert httpTokens state, unknown value"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported instanceMetadataTags", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithInstanceMetadataOptions(&capav1.InstanceMetadataOptions{
					InstanceMetadataTags: capav1.InstanceMetadataEndpointStateEnabled,
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.instanceMetadataOptions.instanceMetadataTags: Invalid value: \"enabled\": instanceMetadataTags values other than \"disabled\" are not supported"},
			expectedWarnings: []string{},
		}),

		// Test case for multiple metadata-related fields
		Entry("With multiple unsupported metadata options", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithInstanceMetadataOptions(&capav1.InstanceMetadataOptions{
					HTTPEndpoint:            capav1.InstanceMetadataEndpointStateDisabled,
					HTTPPutResponseHopLimit: 2,
					HTTPTokens:              "unsupported",
					InstanceMetadataTags:    capav1.InstanceMetadataEndpointStateEnabled,
				}),
			machineBuilder: awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.instanceMetadataOptions.httpTokens: Invalid value: \"unsupported\": unable to convert httpTokens state, unknown value",
				"spec.instanceMetadataOptions.httpEndpoint: Invalid value: \"disabled\": httpEndpoint values other than \"enabled\" are not supported",
				"spec.instanceMetadataOptions.httpPutResponseHopLimit: Invalid value: 2: httpPutResponseHopLimit values other than 1 are not supported",
				"spec.instanceMetadataOptions.instanceMetadataTags: Invalid value: \"enabled\": instanceMetadataTags values other than \"disabled\" are not supported"},
			expectedWarnings: []string{},
		}),
	)

	var _ = DescribeTable("capi2mapi AWS convert CAPI MachineSet/InfraMachineTemplate/InfraCluster to MAPI MachineSet",
		func(in awsCAPI2MAPIMachinesetConversionInput) {
			_, warns, err := FromMachineSetAndAWSMachineTemplateAndAWSCluster(
				in.machineSetBuilder.Build(),
				in.awsMachineTemplateBuilder.Build(),
				in.awsClusterBuilder.Build(),
			).ToMachineSet()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting AWS CAPI resources to MAPI MachineSet")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting AWS CAPI resources to MAPI MachineSet")
		},

		// Base Case.
		Entry("With a Base configuration", awsCAPI2MAPIMachinesetConversionInput{
			awsClusterBuilder:         awsCAPIAWSClusterBase,
			awsMachineTemplateBuilder: capabuilder.AWSMachineTemplate(),
			machineSetBuilder:         capibuilder.MachineSet(),
			expectedErrors:            []string{},
			expectedWarnings:          []string{},
		}),
	)
})

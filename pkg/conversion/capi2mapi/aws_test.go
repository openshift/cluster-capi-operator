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
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	capibuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta2"
	capabuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
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
		Entry("With HostAffinity default", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("default")),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity host and HostID (17 characters)", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("host")).
				WithHostID(ptr.To("h-1234567890abcdef0")),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity host and HostID (8 characters)", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("host")).
				WithHostID(ptr.To("h-12345678")),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity default and HostID (8 characters)", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("default")).
				WithHostID(ptr.To("h-abcdef12")),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity default and HostID (17 characters)", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("default")).
				WithHostID(ptr.To("h-fedcba9876543210f")),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity host and invalid HostID (too short)", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("host")).
				WithHostID(ptr.To("h-1234567")),
			machineBuilder: awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.dedicatedHost.id: Invalid value: \"h-1234567\": id must start with 'h-' followed by 8 or 17 lowercase hexadecimal characters (0-9 and a-f)",
			},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity host and invalid HostID (wrong length - 9 characters)", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("host")).
				WithHostID(ptr.To("h-123456789")),
			machineBuilder: awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.dedicatedHost.id: Invalid value: \"h-123456789\": id must start with 'h-' followed by 8 or 17 lowercase hexadecimal characters (0-9 and a-f)",
			},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity host and invalid HostID (wrong length - 16 characters)", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("host")).
				WithHostID(ptr.To("h-1234567890abcdef")),
			machineBuilder: awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.dedicatedHost.id: Invalid value: \"h-1234567890abcdef\": id must start with 'h-' followed by 8 or 17 lowercase hexadecimal characters (0-9 and a-f)",
			},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity host and invalid HostID (uppercase characters)", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("host")).
				WithHostID(ptr.To("h-1234567890ABCDEF0")),
			machineBuilder: awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.dedicatedHost.id: Invalid value: \"h-1234567890ABCDEF0\": id must start with 'h-' followed by 8 or 17 lowercase hexadecimal characters (0-9 and a-f)",
			},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity host and invalid HostID (missing h- prefix)", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("host")).
				WithHostID(ptr.To("12345678")),
			machineBuilder: awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.dedicatedHost.id: Invalid value: \"12345678\": id must start with 'h-' followed by 8 or 17 lowercase hexadecimal characters (0-9 and a-f)",
			},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity host and invalid HostID (non-hex characters)", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("host")).
				WithHostID(ptr.To("h-1234567g")),
			machineBuilder: awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.dedicatedHost.id: Invalid value: \"h-1234567g\": id must start with 'h-' followed by 8 or 17 lowercase hexadecimal characters (0-9 and a-f)",
			},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity host but missing HostID and DynamicHostAllocation", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("host")),
			machineBuilder: awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.dedicatedHost: Required value: either id or dynamicHostAllocation is required when hostAffinity is host",
			},
			expectedWarnings: []string{},
		}),
		Entry("With HostAffinity default and invalid HostID format", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("default")).
				WithHostID(ptr.To("h-invalid")),
			machineBuilder: awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.dedicatedHost.id: Invalid value: \"h-invalid\": id must start with 'h-' followed by 8 or 17 lowercase hexadecimal characters (0-9 and a-f)",
			},
			expectedWarnings: []string{},
		}),
		Entry("With unsupported HostAffinity", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithHostAffinity(ptr.To("unsupported")),
			machineBuilder: awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.hostAffinity: Invalid value: \"unsupported\": unable to convert hostAffinity, unknown value",
			},
			expectedWarnings: []string{},
		}),
		Entry("With unsupported EKSOptimizedLookupType", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: capabuilder.AWSMachine().
				WithAMI(awsv1.AMIReference{
					EKSOptimizedLookupType: ptr.To(awsv1.EKSAMILookupType("unsupported")),
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
			awsMachineBuilder: awsCAPIAWSMachineBase.WithSecurityGroupOverrides(map[awsv1.SecurityGroupRole]string{"sg-1": "sg-2"}),
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{"spec.securityGroupOverrides: Invalid value: {\"sg-1\":\"sg-2\"}: securityGroupOverrides are not supported"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported NetworkInterfaces", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithNetworkInterfaces([]string{"eni-12345", "eni-67890"}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.networkInterfaces: Invalid value: [\"eni-12345\",\"eni-67890\"]: networkInterfaces are not supported"},
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
			awsMachineBuilder: awsCAPIAWSMachineBase.WithCloudInit(awsv1.CloudInit{InsecureSkipSecretsManager: true}),
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{"spec.cloudInit: Invalid value: {\"insecureSkipSecretsManager\":true}: cloudInit is not supported"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported PrivateDNSName", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.WithPrivateDNSName(&awsv1.PrivateDNSName{}),
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors:    []string{"spec.privateDNSName: Invalid value: {}: privateDNSName is not supported"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported Ignition Proxy", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithIgnition(&awsv1.Ignition{
					Proxy: &awsv1.IgnitionProxy{},
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.ignition.proxy: Invalid value: {}: ignition proxy is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported Ignition TLS", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithIgnition(&awsv1.Ignition{
					TLS: &awsv1.IgnitionTLS{
						CASources: []awsv1.IgnitionCASource{"a", "b"},
					},
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.ignition.tls: Invalid value: {\"certificateAuthorities\":[\"a\",\"b\"]}: ignition tls is not supported"},
			expectedWarnings: []string{},
		}),
		Entry("With unsupported httpEndpoint", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithInstanceMetadataOptions(&awsv1.InstanceMetadataOptions{
					HTTPEndpoint: awsv1.InstanceMetadataEndpointStateDisabled,
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.instanceMetadataOptions.httpEndpoint: Invalid value: \"disabled\": httpEndpoint values other than \"enabled\" are not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported httpPutResponseHopLimit", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithInstanceMetadataOptions(&awsv1.InstanceMetadataOptions{
					HTTPPutResponseHopLimit: 2,
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.instanceMetadataOptions.httpPutResponseHopLimit: Invalid value: 2: httpPutResponseHopLimit values other than 1 are not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported httpTokens", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithInstanceMetadataOptions(&awsv1.InstanceMetadataOptions{
					HTTPTokens: "unsupported",
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.instanceMetadataOptions.httpTokens: Invalid value: \"unsupported\": unable to convert httpTokens state, unknown value"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported instanceMetadataTags", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithInstanceMetadataOptions(&awsv1.InstanceMetadataOptions{
					InstanceMetadataTags: awsv1.InstanceMetadataEndpointStateEnabled,
				}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.instanceMetadataOptions.instanceMetadataTags: Invalid value: \"enabled\": instanceMetadataTags values other than \"disabled\" are not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported role identityRef", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase.
				WithIdentityRef(&awsv1.AWSIdentityReference{
					Kind: awsv1.ClusterRoleIdentityKind,
					Name: "invalid",
				}),
			awsMachineBuilder: awsCAPIAWSMachineBase,
			machineBuilder:    awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.identityRef.kind: Invalid value: \"AWSClusterRoleIdentity\": kind \"AWSClusterRoleIdentity\" cannot be converted to CredentialsSecret",
				"spec.identityRef.name: Invalid value: \"invalid\": name \"invalid\" must be \"default\" when using an AWSClusterControllerIdentity",
			},
			expectedWarnings: []string{},
		}),

		// Test case for multiple metadata-related fields
		Entry("With multiple unsupported metadata options", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.
				WithInstanceMetadataOptions(&awsv1.InstanceMetadataOptions{
					HTTPEndpoint:            awsv1.InstanceMetadataEndpointStateDisabled,
					HTTPPutResponseHopLimit: 2,
					HTTPTokens:              "unsupported",
					InstanceMetadataTags:    awsv1.InstanceMetadataEndpointStateEnabled,
				}),
			machineBuilder: awsCAPIMachineBase,
			expectedErrors: []string{
				"spec.instanceMetadataOptions.httpTokens: Invalid value: \"unsupported\": unable to convert httpTokens state, unknown value",
				"spec.instanceMetadataOptions.httpEndpoint: Invalid value: \"disabled\": httpEndpoint values other than \"enabled\" are not supported",
				"spec.instanceMetadataOptions.httpPutResponseHopLimit: Invalid value: 2: httpPutResponseHopLimit values other than 1 are not supported",
				"spec.instanceMetadataOptions.instanceMetadataTags: Invalid value: \"enabled\": instanceMetadataTags values other than \"disabled\" are not supported"},
			expectedWarnings: []string{},
		}),
		Entry("With ControlPlaneLoadBalancer and a worker machine", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase.WithControlPlaneLoadBalancer(&awsv1.AWSLoadBalancerSpec{
				Name:             ptr.To("test-control-plane-lb"),
				LoadBalancerType: awsv1.LoadBalancerTypeClassic,
			}),
			awsMachineBuilder: awsCAPIAWSMachineBase,
			machineBuilder:    awsCAPIMachineBase, // Worker machine (no control plane role)
			expectedErrors:    []string{},
			expectedWarnings:  []string{},
		}),
		Entry("With ControlPlaneLoadBalancer and a control plane machine", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase.WithControlPlaneLoadBalancer(&awsv1.AWSLoadBalancerSpec{
				Name:             ptr.To("test-control-plane-lb"),
				LoadBalancerType: awsv1.LoadBalancerTypeClassic,
			}),
			awsMachineBuilder: awsCAPIAWSMachineBase,
			machineBuilder: awsCAPIMachineBase.WithLabels(map[string]string{
				"node-role.kubernetes.io/master": "",
			}),
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
		Entry("With ControlPlaneLoadBalancer NLB and a control plane machine", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase.WithControlPlaneLoadBalancer(&awsv1.AWSLoadBalancerSpec{
				Name:             ptr.To("test-nlb"),
				LoadBalancerType: awsv1.LoadBalancerTypeNLB,
			}),
			awsMachineBuilder: awsCAPIAWSMachineBase,
			machineBuilder: awsCAPIMachineBase.WithLabels(map[string]string{
				"cluster.x-k8s.io/control-plane": "",
			}),
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),
		Entry("With both ControlPlaneLoadBalancer and SecondaryControlPlaneLoadBalancer and a control plane machine", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase.WithControlPlaneLoadBalancer(&awsv1.AWSLoadBalancerSpec{
				Name:             ptr.To("test-nlb"),
				LoadBalancerType: awsv1.LoadBalancerTypeNLB,
			}).WithSecondaryControlPlaneLoadBalancer(&awsv1.AWSLoadBalancerSpec{
				Name:             ptr.To("test-external-lb"),
				LoadBalancerType: awsv1.LoadBalancerTypeClassic,
			}),
			awsMachineBuilder: awsCAPIAWSMachineBase,
			machineBuilder: awsCAPIMachineBase.WithLabels(map[string]string{
				"node-role.kubernetes.io/master": "",
			}),
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With root volume throughput exceeding int32 max", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.WithRootVolume(&awsv1.Volume{
				Throughput: ptr.To(int64(math.MaxInt32) + 1),
				Size:       100,
			}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.rootVolume.throughput: Invalid value: 2147483648: throughput exceeds maximum int32 value"},
			expectedWarnings: []string{},
		}),

		Entry("With non-root volume throughput exceeding int32 max", awsCAPI2MAPIMachineConversionInput{
			awsClusterBuilder: awsCAPIAWSClusterBase,
			awsMachineBuilder: awsCAPIAWSMachineBase.WithNonRootVolumes([]awsv1.Volume{
				{
					Throughput: ptr.To(int64(math.MaxInt32) + 1),
					Size:       100,
					DeviceName: "/dev/sdb",
				},
			}),
			machineBuilder:   awsCAPIMachineBase,
			expectedErrors:   []string{"spec.nonRootVolumes[0].throughput: Invalid value: 2147483648: throughput exceeds maximum int32 value"},
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

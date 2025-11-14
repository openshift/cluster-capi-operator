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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	clusterv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	openstackv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
	"k8s.io/utils/ptr"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
)

var _ = Describe("capi2mapi OpenStack conversion", func() {
	var (
		openstackCAPIMachineBase          = clusterv1resourcebuilder.Machine()
		openstackCAPIOpenStackMachineBase = openstackv1resourcebuilder.OpenStackMachine().WithFlavor(
			ptr.To("m1.tiny"),
		).WithImage(
			openstackv1.ImageParam{Filter: &openstackv1.ImageFilter{Name: ptr.To("rhcos")}},
		).WithPorts(
			[]openstackv1.PortOpts{
				{
					Network: &openstackv1.NetworkParam{
						Filter: &openstackv1.NetworkFilter{Name: "provider-net"},
					},
				},
			},
		).WithServerGroup(
			&openstackv1.ServerGroupParam{
				Filter: &openstackv1.ServerGroupFilter{Name: ptr.To("server-group-a")},
			},
		)
		openstackCAPIOpenStackClusterBase = openstackv1resourcebuilder.OpenStackCluster()
	)

	type openstackCAPI2MAPIMachineConversionInput struct {
		machineBuilder          clusterv1resourcebuilder.MachineBuilder
		openstackMachineBuilder openstackv1resourcebuilder.OpenStackMachineBuilder
		openstackClusterBuilder openstackv1resourcebuilder.OpenStackClusterBuilder
		expectedErrors          []string
		expectedWarnings        []string
	}

	type openstackCAPI2MAPIMachinesetConversionInput struct {
		machineSetBuilder               clusterv1resourcebuilder.MachineSetBuilder
		openstackMachineTemplateBuilder openstackv1resourcebuilder.OpenStackMachineTemplateBuilder
		openstackClusterBuilder         openstackv1resourcebuilder.OpenStackClusterBuilder
		expectedErrors                  []string
		expectedWarnings                []string
	}

	var _ = DescribeTable("capi2mapi OpenStack convert CAPI Machine/InfraMachine/InfraCluster to a MAPI Machine",
		func(in openstackCAPI2MAPIMachineConversionInput) {
			_, warns, err := FromMachineAndOpenStackMachineAndOpenStackCluster(
				in.machineBuilder.Build(),
				in.openstackMachineBuilder.Build(),
				in.openstackClusterBuilder.Build(),
			).ToMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting OpenStack CAPI resources to MAPI Machine")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting OpenStack CAPI resources to MAPI Machine")
		},

		// Base Case.
		Entry("passes with a base configuration", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase,
			machineBuilder:          openstackCAPIMachineBase,
			expectedErrors:          []string{},
			expectedWarnings:        []string{},
		}),

		// Only Error.
		Entry("fails with an flavor requested by ID instead of name", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase.WithFlavor(
				nil,
			).WithFlavorID(
				ptr.To("3f1e51b0-bfc8-4fc6-a4e2-8b54ffdaf740"),
			),
			machineBuilder: openstackCAPIMachineBase,
			expectedErrors: []string{
				"spec.flavorID: Invalid value: \"3f1e51b0-bfc8-4fc6-a4e2-8b54ffdaf740\": MAPO only supports defining flavors via names",
			},
			expectedWarnings: []string{},
		}),
		Entry("fails with an image with unsupported fields (a)", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase.WithImage(
				openstackv1.ImageParam{ID: ptr.To("a23ab56e-3890-4730-a624-055c236d8ed7")},
			),
			machineBuilder: openstackCAPIMachineBase,
			expectedErrors: []string{
				"spec.image.id: Invalid value: \"a23ab56e-3890-4730-a624-055c236d8ed7\": MAPO only supports defining images by names",
			},
			expectedWarnings: []string{},
		}),
		Entry("fails with an image with unsupported fields (b)", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase.WithImage(
				openstackv1.ImageParam{ImageRef: &openstackv1.ResourceReference{Name: "my-orc-image"}},
			),
			machineBuilder: openstackCAPIMachineBase,
			expectedErrors: []string{
				"spec.image.imageRef: Invalid value: {\"name\":\"my-orc-image\"}: MAPO only supports defining images by names",
			},
			expectedWarnings: []string{},
		}),
		Entry("fails with an image with missing fields", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase.WithImage(
				openstackv1.ImageParam{Filter: nil},
			),
			machineBuilder: openstackCAPIMachineBase,
			expectedErrors: []string{
				"spec.image.filter: Required value: MAPO only supports defining images by names",
			},
			expectedWarnings: []string{},
		}),
		Entry("fails with a port without valid network identifiers", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase.WithPorts(
				[]openstackv1.PortOpts{
					{Network: &openstackv1.NetworkParam{}},
				},
			),
			machineBuilder: openstackCAPIMachineBase,
			expectedErrors: []string{
				"spec.ports[0].network: Required value: A port must have a reference to a network",
			},
			expectedWarnings: []string{},
		}),
		Entry("fails with a port fixed IP subnet without valid identifiers", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase.WithPorts(
				[]openstackv1.PortOpts{
					{
						FixedIPs: []openstackv1.FixedIP{
							{
								IPAddress: ptr.To("192.168.30.5"),
							},
							{
								Subnet:    &openstackv1.SubnetParam{},
								IPAddress: ptr.To("192.168.30.3"),
							},
							{
								Subnet: &openstackv1.SubnetParam{
									ID:     ptr.To("bab26261-89e1-41c9-bb22-55c4fbc44d0c"),
									Filter: &openstackv1.SubnetFilter{Name: "my-subnet"},
								},
								IPAddress: ptr.To("192.168.30.3"),
							},
						},
						Network: &openstackv1.NetworkParam{ID: ptr.To("d1cab4fb-de0f-4d18-b8af-ecb7f89cf21e")},
					},
				},
			),
			machineBuilder: openstackCAPIMachineBase,
			expectedErrors: []string{
				"spec.ports[0].fixedIPs[0].subnet.id: Required value: MAPO only supports defining subnets via IDs",
				"spec.ports[0].fixedIPs[1].subnet.id: Required value: MAPO only supports defining subnets via IDs",
				"spec.ports[0].fixedIPs[2].subnet.filter: Invalid value: {\"name\":\"my-subnet\"}: MAPO only supports defining subnets via IDs",
			},
			expectedWarnings: []string{},
		}),
		Entry("fails with a port security group requested by name instead of ID", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase.WithPorts(
				[]openstackv1.PortOpts{
					{
						Network: &openstackv1.NetworkParam{ID: ptr.To("d1cab4fb-de0f-4d18-b8af-ecb7f89cf21e")},
						SecurityGroups: []openstackv1.SecurityGroupParam{
							{
								Filter: &openstackv1.SecurityGroupFilter{Name: "my-security-group"},
							},
						},
					},
				},
			),
			machineBuilder: openstackCAPIMachineBase,
			expectedErrors: []string{
				"MAPO only supports defining port security groups by ID",
			},
			expectedWarnings: []string{},
		}),
		Entry("fails with a security group without valid identifiers", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase.WithSecurityGroups(
				[]openstackv1.SecurityGroupParam{
					{},
				},
			),
			machineBuilder: openstackCAPIMachineBase,
			expectedErrors: []string{
				"spec.securityGroups[0]: Invalid value: {}: A security group must be referenced by a UUID or filter",
			},
			expectedWarnings: []string{},
		}),
		Entry("fails with a server group without valid identifiers", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase.WithServerGroup(
				&openstackv1.ServerGroupParam{},
			),
			machineBuilder: openstackCAPIMachineBase,
			expectedErrors: []string{
				"spec.serverGroup: Invalid value: {}: A server group must be referenced by a UUID or filter",
			},
			expectedWarnings: []string{},
		}),

		// Only Warnings.
		Entry("warns with an image with ignored fields", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase.WithImage(
				openstackv1.ImageParam{
					Filter: &openstackv1.ImageFilter{
						Name: ptr.To("my-image"),
						Tags: []string{"tag-a", "tag-b"},
					},
				},
			),
			machineBuilder: openstackCAPIMachineBase,
			expectedErrors: []string{},
			expectedWarnings: []string{
				"spec.image.filter.tags: Invalid value: [\"tag-a\",\"tag-b\"]: MAPO does not support filtering image by tags",
			},
		}),
		Entry("warns with a port using unsupported fields", openstackCAPI2MAPIMachineConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineBuilder: openstackCAPIOpenStackMachineBase.WithPorts(
				[]openstackv1.PortOpts{
					{
						Network: &openstackv1.NetworkParam{ID: ptr.To("d1cab4fb-de0f-4d18-b8af-ecb7f89cf21e")},
						ResolvedPortSpecFields: openstackv1.ResolvedPortSpecFields{
							HostID:                ptr.To("my-host"),
							PropagateUplinkStatus: ptr.To(true),
							ValueSpecs:            []openstackv1.ValueSpec{{}},
						},
					},
				},
			),
			machineBuilder: openstackCAPIMachineBase,
			expectedErrors: []string{},
			expectedWarnings: []string{
				"spec.ports[0].hostID: Invalid value: \"my-host\": The hostID field has no equivalent in MAPO and is not supported",
				"spec.ports[0].propagateUplinkStatus: Invalid value: true: The propagateUplinkStatus field has no equivalent in MAPO and is not supported",
				"spec.ports[0].valueSpecs: Invalid value: [{\"name\":\"\",\"key\":\"\",\"value\":\"\"}]: The valueSpecs field has no equivalent in MAPO and is not supported",
			},
		}),
	)

	var _ = DescribeTable("capi2mapi OpenStack convert CAPI MachineSet/InfraMachineTemplate/InfraCluster to MAPI MachineSet",
		func(in openstackCAPI2MAPIMachinesetConversionInput) {
			_, warns, err := FromMachineSetAndOpenStackMachineTemplateAndOpenStackCluster(
				in.machineSetBuilder.Build(),
				in.openstackMachineTemplateBuilder.Build(),
				in.openstackClusterBuilder.Build(),
			).ToMachineSet()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting OpenStack CAPI resources to MAPI MachineSet")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting OpenStack CAPI resources to MAPI MachineSet")
		},

		// Base Case.
		Entry("passes with a base configuration", openstackCAPI2MAPIMachinesetConversionInput{
			openstackClusterBuilder: openstackCAPIOpenStackClusterBase,
			openstackMachineTemplateBuilder: openstackv1resourcebuilder.OpenStackMachineTemplate().WithFlavor(
				ptr.To("m1.tiny"),
			).WithImage(
				openstackv1.ImageParam{Filter: &openstackv1.ImageFilter{Name: ptr.To("rhcos")}},
			).WithPorts(
				[]openstackv1.PortOpts{
					{
						Network: &openstackv1.NetworkParam{
							Filter: &openstackv1.NetworkFilter{Name: "provider-net"},
						},
					},
				},
			).WithServerGroup(
				&openstackv1.ServerGroupParam{
					Filter: &openstackv1.ServerGroupFilter{Name: ptr.To("server-group-a")},
				},
			),
			machineSetBuilder: clusterv1resourcebuilder.MachineSet(),
			expectedErrors:    []string{},
			expectedWarnings:  []string{},
		}),
	)
})

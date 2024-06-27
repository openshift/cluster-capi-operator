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
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"

	mapiv1 "github.com/openshift/api/machine/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
)

var _ = Describe("mapi2capi OpenStack conversion", func() {
	var (
		openstackBaseProviderSpec   = machinebuilder.OpenStackProviderSpec()
		openstackMAPIMachineBase    = machinebuilder.Machine().WithProviderSpecBuilder(openstackBaseProviderSpec)
		openstackMAPIMachineSetBase = machinebuilder.MachineSet().WithProviderSpecBuilder(openstackBaseProviderSpec)

		infra = &configv1.Infrastructure{
			Spec:   configv1.InfrastructureSpec{},
			Status: configv1.InfrastructureStatus{InfrastructureName: "sample-cluster-name"},
		}
	)

	type openstackMAPI2CAPIConversionInput struct {
		machineBuilder   machinebuilder.MachineBuilder
		infra            *configv1.Infrastructure
		expectedErrors   []string
		expectedWarnings []string
	}

	type openstackMAPI2CAPIMachinesetConversionInput struct {
		machineSetBuilder machinebuilder.MachineSetBuilder
		infra             *configv1.Infrastructure
		expectedErrors    []string
		expectedWarnings  []string
	}

	var _ = DescribeTable("mapi2capi OpenStack convert MAPI Machine",
		func(in openstackMAPI2CAPIConversionInput) {
			_, _, warns, err := FromOpenStackMachineAndInfra(in.machineBuilder.Build(), in.infra).ToMachineAndInfrastructureMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors), "should match expected errors while converting an OpenStack MAPI Machine to CAPI")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings), "should match expected warnings while converting an OpenStack MAPI Machine to CAPI")
		},

		// Base Case.
		Entry("With a Base configuration", openstackMAPI2CAPIConversionInput{
			machineBuilder:   openstackMAPIMachineBase,
			infra:            infra,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		// Only Error.
		Entry("fails with additional block device with nil volume", openstackMAPI2CAPIConversionInput{
			machineBuilder: openstackMAPIMachineBase.WithProviderSpecBuilder(
				openstackBaseProviderSpec.WithAdditionalBlockDevices(
					[]mapiv1.AdditionalBlockDevice{
						{
							Storage: mapiv1.BlockDeviceStorage{
								Type: "Volume", Volume: nil,
							},
						},
					},
				),
			),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.additionalBlockDevices[0].volume: Required value: volume is required, but is missing",
			},
			expectedWarnings: []string{},
		}),

		// Only Warnings.
		Entry("warns with a network with deprecated fields", openstackMAPI2CAPIConversionInput{
			machineBuilder: openstackMAPIMachineBase.WithProviderSpecBuilder(
				openstackBaseProviderSpec.WithNetworks(
					[]mapiv1.NetworkParam{
						{
							FixedIp: "192.168.6.8",
							UUID:    "8c57e4e2-9c79-4a5e-8e21-58064574518f",
							Filter:  mapiv1.Filter{},
						},
					},
				),
			),
			infra:          infra,
			expectedErrors: []string{},
			expectedWarnings: []string{
				"spec.providerSpec.value.networks[0].fixedIP: Invalid value: \"192.168.6.8\": fixedIp is ignored by MAPO, ignoring",
			},
		}),
		Entry("warns with network subnets with deprecated fields", openstackMAPI2CAPIConversionInput{
			machineBuilder: openstackMAPIMachineBase.WithProviderSpecBuilder(
				openstackBaseProviderSpec.WithNetworks(
					[]mapiv1.NetworkParam{
						{
							UUID:   "8c57e4e2-9c79-4a5e-8e21-58064574518f",
							Filter: mapiv1.Filter{},
							Subnets: []mapiv1.SubnetParam{
								{
									UUID: "2dcb9441-f4ce-469d-a422-6769815b4966",
									Filter: mapiv1.SubnetFilter{
										NetworkID: "8c57e4e2-9c79-4a5e-8e21-58064574518f",
									},
								},
							},
						},
						{
							Filter: mapiv1.Filter{},
							Subnets: []mapiv1.SubnetParam{
								{
									UUID: "1d849606-4c13-4f8e-809c-9997393c1285",
									Filter: mapiv1.SubnetFilter{
										NetworkID: "78a20e17-96e8-42a7-8301-c33b9a78daa4",
									},
								},
							},
						},
					},
				),
			),
			infra:          infra,
			expectedErrors: []string{},
			expectedWarnings: []string{
				"spec.providerSpec.value.networks[0].subnets[0].filter.networkId: Invalid value: \"8c57e4e2-9c79-4a5e-8e21-58064574518f\": networkId is ignored by MAPO, ignoring",
				"spec.providerSpec.value.networks[1].subnets[0].filter.networkId: Invalid value: \"78a20e17-96e8-42a7-8301-c33b9a78daa4\": networkId is ignored by MAPO, ignoring",
			},
		}),
		Entry("warns with a port with deprecated fields", openstackMAPI2CAPIConversionInput{
			machineBuilder: openstackMAPIMachineBase.WithProviderSpecBuilder(
				openstackBaseProviderSpec.WithPorts(
					[]mapiv1.PortOpts{
						{
							DeprecatedHostID: "compute-b",
						},
					},
				),
			),
			infra:          infra,
			expectedErrors: []string{},
			expectedWarnings: []string{
				"spec.providerSpec.value.ports[0].hostID: Invalid value: \"compute-b\": hostID is ignored by MAPO, ignoring",
			},
		}),
		Entry("warns with a root volume using deprecated fields", openstackMAPI2CAPIConversionInput{
			machineBuilder: openstackMAPIMachineBase.WithProviderSpecBuilder(
				openstackBaseProviderSpec.WithRootVolume(
					&mapiv1.RootVolume{
						Size: 10,
						// https://wiki.openstack.org/wiki/BlockDeviceConfig#device_type
						DeprecatedDeviceType: "disk",
						// https://wiki.openstack.org/wiki/BlockDeviceConfig#source_type
						DeprecatedSourceType: "snapshot",
						SourceUUID:           "06e57877-84e5-4ccc-970f-988dd9273a20",
					},
				),
			),
			infra:          infra,
			expectedErrors: []string{},
			expectedWarnings: []string{
				"spec.providerSpec.value.rootVolume.deviceType: Invalid value: \"disk\": deviceType is silently ignored by MAPO and will not be converted",
				"spec.providerSpec.value.rootVolume.sourceType: Invalid value: \"snapshot\": sourceType is silently ignored by MAPO and will not be converted",
				"spec.providerSpec.value.rootVolume.sourceUUID: Invalid value: \"06e57877-84e5-4ccc-970f-988dd9273a20\": sourceUUID is superseded by spec.image in MAPO and will be ignored here",
			},
		}),
	)

	var _ = DescribeTable("mapi2capi OpenStack convert MAPI MachineSet",
		func(in openstackMAPI2CAPIMachinesetConversionInput) {
			_, _, warns, err := FromOpenStackMachineSetAndInfra(in.machineSetBuilder.Build(), in.infra).ToMachineSetAndMachineTemplate()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors), "should match expected errors while converting an OpenStack MAPI MachineSet to CAPI")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings), "should match expected warnings while converting an OpenStack MAPI MachineSet to CAPI")
		},

		Entry("With a Base configuration", openstackMAPI2CAPIMachinesetConversionInput{
			machineSetBuilder: openstackMAPIMachineSetBase,
			infra:             infra,
			expectedErrors:    []string{},
			expectedWarnings:  []string{},
		}),
	)

})

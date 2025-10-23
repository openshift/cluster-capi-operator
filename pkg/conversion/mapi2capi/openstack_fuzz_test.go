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
package mapi2capi_test

import (
	"bytes"
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/uuid"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1alpha1 "github.com/openshift/api/machine/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/randfill"

	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	conversiontest "github.com/openshift/cluster-capi-operator/pkg/conversion/test/fuzz"
)

const (
	openstackProviderSpecKind = "OpenstackProviderSpec"

	latin = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz01233456789"
)

var _ = Describe("OpenStack Fuzz (mapi2capi)", func() {
	infra := &configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	infraCluster := &openstackv1.OpenStackCluster{
		Spec: openstackv1.OpenStackClusterSpec{},
	}

	Context("OpenStackMachine Conversion", func() {
		fromMachineAndOpenStackMachineAndOpenStackCluster := func(machine *clusterv1.Machine, infraMachine client.Object, infraCluster client.Object) capi2mapi.MachineAndInfrastructureMachine {
			openstackMachine, ok := infraMachine.(*openstackv1.OpenStackMachine)
			Expect(ok).To(BeTrue(), "input infra machine should be of type %T, got %T", &openstackv1.OpenStackMachine{}, infraMachine)

			openstackCluster, ok := infraCluster.(*openstackv1.OpenStackCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &openstackv1.OpenStackCluster{}, infraCluster)

			return capi2mapi.FromMachineAndOpenStackMachineAndOpenStackCluster(machine, openstackMachine, openstackCluster)
		}

		f := &openstackProviderFuzzer{}

		conversiontest.MAPI2CAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			mapi2capi.FromOpenStackMachineAndInfra,
			fromMachineAndOpenStackMachineAndOpenStackCluster,
			conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
			conversiontest.MAPIMachineFuzzerFuncs(&mapiv1alpha1.OpenstackProviderSpec{}, nil, openstackProviderIDFuzzer),
			f.FuzzerFuncsMachine,
		)
	})

	Context("OpenStackMachineSet Conversion", func() {
		fromMachineSetAndOpenStackMachineTemplateAndOpenStackCluster := func(machineSet *clusterv1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) capi2mapi.MachineSetAndMachineTemplate {
			openstackMachineTemplate, ok := infraMachineTemplate.(*openstackv1.OpenStackMachineTemplate)
			Expect(ok).To(BeTrue(), "input infra machine template should be of type %T, got %T", &openstackv1.OpenStackMachineTemplate{}, infraMachineTemplate)

			openstackCluster, ok := infraCluster.(*openstackv1.OpenStackCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &openstackv1.OpenStackCluster{}, infraCluster)

			return capi2mapi.FromMachineSetAndOpenStackMachineTemplateAndOpenStackCluster(machineSet, openstackMachineTemplate, openstackCluster)
		}

		f := &openstackProviderFuzzer{}

		conversiontest.MAPI2CAPIMachineSetRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			mapi2capi.FromOpenStackMachineSetAndInfra,
			fromMachineSetAndOpenStackMachineTemplateAndOpenStackCluster,
			conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
			conversiontest.MAPIMachineFuzzerFuncs(&mapiv1alpha1.OpenstackProviderSpec{}, nil, openstackProviderIDFuzzer),
			conversiontest.MAPIMachineSetFuzzerFuncs(),
			f.FuzzerFuncsMachineSet,
		)
	})
})

func openstackProviderIDFuzzer(c randfill.Continue) string {
	return "openstack://" + uuid.NewString()
}

type openstackProviderFuzzer struct {
	conversiontest.MAPIMachineFuzzer
}

func (f *openstackProviderFuzzer) fuzzProviderSpec(providerSpec *mapiv1alpha1.OpenstackProviderSpec, c randfill.Continue) {
	c.FillNoCustom(providerSpec)

	// The type meta is always set to these values by the conversion.
	providerSpec.APIVersion = mapiv1alpha1.GroupVersion.String()
	providerSpec.Kind = openstackProviderSpecKind

	// Clear fields that are not supported in the provider spec.
	providerSpec.ObjectMeta = metav1.ObjectMeta{}
	providerSpec.FloatingIP = ""
	providerSpec.PrimarySubnet = ""
	providerSpec.SshUserName = ""

	// Clear namespace fields, since these are intentionally not copied
	if providerSpec.UserDataSecret != nil {
		providerSpec.UserDataSecret.Namespace = ""
	}

	if providerSpec.CloudsSecret != nil {
		providerSpec.CloudsSecret.Namespace = ""

		if providerSpec.CloudsSecret.Name == "" {
			providerSpec.CloudsSecret = nil
		}
	}

	// Clear fields that depend on other, unset fields or cannot coexist
	if providerSpec.CloudsSecret == nil {
		providerSpec.CloudName = ""
	}

	switch c.Int31n(2) {
	case 0:
		providerSpec.ServerGroupID = uuid.NewString()
		providerSpec.ServerGroupName = ""
	case 1:
		providerSpec.ServerGroupID = ""
		providerSpec.ServerGroupName = uuid.NewString()
	}

	// Clear pointers to empty structs.
	if providerSpec.UserDataSecret != nil && providerSpec.UserDataSecret.Name == "" {
		providerSpec.UserDataSecret = nil
	}

	// Copy instance-type, region and zone to the struct so they can be set at the machine labels too.
	f.MAPIMachineFuzzer.InstanceType = providerSpec.Flavor
	f.MAPIMachineFuzzer.Zone = providerSpec.AvailabilityZone
}

//nolint:funlen
func (f *openstackProviderFuzzer) FuzzerFuncsMachineSet(codecs runtimeserializer.CodecFactory) []any {
	return []any{
		func(bdm *mapiv1alpha1.BlockDeviceStorage, c randfill.Continue) {
			switch c.Int31n(2) {
			case 0:
				bdm.Type = mapiv1alpha1.LocalBlockDevice
				bdm.Volume = nil
			case 1:
				bdm.Type = mapiv1alpha1.VolumeBlockDevice
				// Fuzz required fields so that they are not empty.
				volume := &mapiv1alpha1.BlockDeviceVolume{}
				c.Fill(volume)
				bdm.Volume = volume
			}
		},
		func(network *mapiv1alpha1.NetworkParam, c randfill.Continue) {
			switch c.Int31n(2) {
			case 0:
				network.UUID = uuid.NewString()
				network.Filter = mapiv1alpha1.Filter{}
			case 1:
				network.UUID = ""
				c.Fill(&network.Filter)

				// Clear fields that are not supported by conversion.
				// These fields exist in the API but are not implemented in MAPO.
				network.FixedIp = ""
				network.Filter.DeprecatedAdminStateUp = nil
				network.Filter.DeprecatedLimit = 0
				network.Filter.DeprecatedMarker = ""
				network.Filter.DeprecatedShared = nil
				network.Filter.DeprecatedSortDir = ""
				network.Filter.DeprecatedSortKey = ""
				network.Filter.DeprecatedStatus = ""
				network.Filter.ID = ""
				network.Filter.TenantID = ""

				// Set fields that must be specific values to those values
				network.Filter.Tags = generateFakeTags()
				network.Filter.TagsAny = generateFakeTags()
				network.Filter.NotTags = generateFakeTags()
				network.Filter.NotTagsAny = generateFakeTags()
			}
		},
		func(port *mapiv1alpha1.PortOpts, c randfill.Continue) {
			// Clear fields that are not supported by conversion.
			// These fields exist in the API but are not implemented in MAPO.
			port.DeprecatedHostID = ""
		},
		func(rootVolume *mapiv1alpha1.RootVolume, c randfill.Continue) {
			c.FillNoCustom(rootVolume)

			// Clear fields that are not supported by conversion.
			// These fields exist in the API but are not implemented in MAPO.
			rootVolume.DeprecatedDeviceType = ""
			rootVolume.DeprecatedSourceType = ""
			rootVolume.SourceUUID = ""
		},
		func(securityGroup *mapiv1alpha1.SecurityGroupParam, c randfill.Continue) {
			switch c.Int31n(2) {
			case 0:
				securityGroup.UUID = uuid.NewString()
				securityGroup.Name = ""
				securityGroup.Filter = mapiv1alpha1.SecurityGroupFilter{}
			case 1:
				c.Fill(&securityGroup.Name)
				securityGroup.UUID = ""
				c.Fill(&securityGroup.Filter)

				// Clear fields that are not supported by conversion.
				// These fields exist in the API but are not implemented in MAPO.
				securityGroup.Filter.DeprecatedLimit = 0
				securityGroup.Filter.DeprecatedMarker = ""
				securityGroup.Filter.DeprecatedSortDir = ""
				securityGroup.Filter.DeprecatedSortKey = ""
				securityGroup.Filter.ID = ""
				securityGroup.Filter.Name = ""
				securityGroup.Filter.TenantID = ""

				// Set fields that must be specific values to those values
				securityGroup.Filter.Tags = generateFakeTags()
				securityGroup.Filter.TagsAny = generateFakeTags()
				securityGroup.Filter.NotTags = generateFakeTags()
				securityGroup.Filter.NotTagsAny = generateFakeTags()
			}
		},
		f.fuzzProviderSpec,
	}
}

func (f *openstackProviderFuzzer) FuzzerFuncsMachine(codecs runtimeserializer.CodecFactory) []interface{} {
	return append(
		f.FuzzerFuncsMachineSet(codecs),
		f.FuzzMachine,
	)
}

// generateFakeTags generate a fake alphanumeric CSV string for use in a tags field.
func generateFakeTags() string {
	var buffer bytes.Buffer

	tagCount := rand.Intn(10)
	for i := 0; i < tagCount; i++ {
		tagLen := rand.Intn(20) + 1
		for j := 0; j < tagLen; j++ {
			buffer.WriteString(string(latin[rand.Intn(len(latin))]))
		}

		if i+1 < tagCount {
			buffer.WriteString(",")
		}
	}

	return buffer.String()
}

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
package capi2mapi_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	randfill "sigs.k8s.io/randfill"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	conversiontest "github.com/openshift/cluster-capi-operator/pkg/conversion/test/fuzz"

	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	awsMachineKind  = "AWSMachine"
	awsTemplateKind = "AWSMachineTemplate"
)

var _ = Describe("AWS Fuzz (capi2mapi)", func() {
	infra := &configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	infraCluster := &awsv1.AWSCluster{
		Spec: awsv1.AWSClusterSpec{
			Region: "us-east-1",
		},
	}

	Context("AWSMachine Conversion", func() {
		fromMachineAndAWSMachineAndAWSCluster := func(machine *clusterv1.Machine, infraMachine client.Object, infraCluster client.Object) capi2mapi.MachineAndInfrastructureMachine {
			awsMachine, ok := infraMachine.(*awsv1.AWSMachine)
			Expect(ok).To(BeTrue(), "input infra machine should be of type %T, got %T", &awsv1.AWSMachine{}, infraMachine)

			awsCluster, ok := infraCluster.(*awsv1.AWSCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &awsv1.AWSCluster{}, infraCluster)

			return capi2mapi.FromMachineAndAWSMachineAndAWSCluster(machine, awsMachine, awsCluster)
		}

		conversiontest.CAPI2MAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&awsv1.AWSMachine{},
			mapi2capi.FromAWSMachineAndInfra,
			fromMachineAndAWSMachineAndAWSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(awsProviderIDFuzzer, awsMachineKind, awsv1.GroupVersion.String(), infra.Status.InfrastructureName),
			awsMachineFuzzerFuncs,
		)
	})

	Context("AWSMachineSet Conversion", func() {
		fromMachineSetAndAWSMachineTemplateAndAWSCluster := func(machineSet *clusterv1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) capi2mapi.MachineSetAndMachineTemplate {
			awsMachineTemplate, ok := infraMachineTemplate.(*awsv1.AWSMachineTemplate)
			Expect(ok).To(BeTrue(), "input infra machine template should be of type %T, got %T", &awsv1.AWSMachineTemplate{}, infraMachineTemplate)

			awsCluster, ok := infraCluster.(*awsv1.AWSCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &awsv1.AWSCluster{}, infraCluster)

			return capi2mapi.FromMachineSetAndAWSMachineTemplateAndAWSCluster(machineSet, awsMachineTemplate, awsCluster)
		}

		conversiontest.CAPI2MAPIMachineSetRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&awsv1.AWSMachineTemplate{},
			mapi2capi.FromAWSMachineSetAndInfra,
			fromMachineSetAndAWSMachineTemplateAndAWSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(awsProviderIDFuzzer, awsTemplateKind, awsv1.GroupVersion.String(), infra.Status.InfrastructureName),
			conversiontest.CAPIMachineSetFuzzerFuncs(awsTemplateKind, awsv1.GroupVersion.String(), infra.Status.InfrastructureName),
			awsMachineFuzzerFuncs,
			awsMachineTemplateFuzzerFuncs,
		)
	})
})

func awsProviderIDFuzzer(c randfill.Continue) string {
	return "aws:///us-west-2a/i-" + strings.ReplaceAll(c.String(0), "/", "")
}

//nolint:funlen
func awsMachineFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(nit *awsv1.NetworkInterfaceType, c randfill.Continue) {
			switch c.Int31n(3) {
			case 0:
				*nit = awsv1.NetworkInterfaceTypeEFAWithENAInterface
			case 1:
				*nit = awsv1.NetworkInterfaceTypeENI
			case 2:
				*nit = ""
			}
		},
		func(imdo *awsv1.InstanceMetadataOptions, c randfill.Continue) {
			c.FillNoCustom(imdo)

			// TODO(OCPCLOUD-2710): Fields not yet supported by MAPI.
			imdo.HTTPEndpoint = awsv1.InstanceMetadataEndpointStateEnabled
			imdo.HTTPPutResponseHopLimit = 0
			imdo.InstanceMetadataTags = awsv1.InstanceMetadataEndpointStateDisabled
		},
		func(tokenState *awsv1.HTTPTokensState, c randfill.Continue) {
			switch c.Int31n(2) {
			case 0:
				*tokenState = awsv1.HTTPTokensStateOptional
			case 1:
				*tokenState = awsv1.HTTPTokensStateRequired
			}
		},
		func(ami *awsv1.AMIReference, c randfill.Continue) {
			c.FillNoCustom(ami)

			// Ensure that the AMI ID is set.
			for ami.ID == nil || *ami.ID == "" {
				c.Fill(&ami.ID)
			}
			// Not required for our use case. Can be ignored.
			ami.EKSOptimizedLookupType = nil
		},
		func(ignition *awsv1.Ignition, c randfill.Continue) {
			// We force these fields, so they must be fuzzed in this way.
			*ignition = awsv1.Ignition{
				StorageType: awsv1.IgnitionStorageTypeOptionUnencryptedUserData,
			}
		},
		func(spec *awsv1.AWSMachineSpec, c randfill.Continue) {
			c.FillNoCustom(spec)

			fuzzAWSMachineSpecTenancy(&spec.Tenancy, c)
			fuzzAWSMachineSpecMarketType(&spec.MarketType, c)

			// Fields not required for our use case can be ignored.
			spec.ImageLookupFormat = ""
			spec.ImageLookupOrg = ""
			spec.ImageLookupBaseOS = ""
			spec.NetworkInterfaces = nil
			spec.CloudInit = awsv1.CloudInit{}
			spec.UncompressedUserData = nil
			spec.PrivateDNSName = nil
			// We don't support this field since the externally managed annotation is added, so it's best to keep this nil.
			spec.SecurityGroupOverrides = nil
		},
		func(m *awsv1.AWSMachine, c randfill.Continue) {
			c.FillNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = awsv1.GroupVersion.String()
			m.TypeMeta.Kind = awsMachineKind
		},
	}
}

func fuzzAWSMachineSpecTenancy(tenancy *string, c randfill.Continue) {
	switch c.Int31n(4) {
	case 0:
		*tenancy = "default"
	case 1:
		*tenancy = "dedicated"
	case 2:
		*tenancy = "host"
	case 3:
		*tenancy = ""
	}
}

func fuzzAWSMachineSpecMarketType(marketType *awsv1.MarketType, c randfill.Continue) {
	switch c.Int31n(4) {
	case 0:
		*marketType = awsv1.MarketTypeOnDemand
	case 1:
		*marketType = awsv1.MarketTypeSpot
	case 2:
		*marketType = awsv1.MarketTypeCapacityBlock
	case 3:
		*marketType = ""
	}
}

func awsMachineTemplateFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(m *awsv1.AWSMachineTemplate, c randfill.Continue) {
			c.FillNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = awsv1.GroupVersion.String()
			m.TypeMeta.Kind = awsTemplateKind
		},
	}
}

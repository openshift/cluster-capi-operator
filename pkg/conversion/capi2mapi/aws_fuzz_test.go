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

	fuzz "github.com/google/gofuzz"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	conversiontest "github.com/openshift/cluster-capi-operator/pkg/conversion/test/fuzz"

	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	awsMachineAPIVersion = "infrastructure.cluster.x-k8s.io/v1beta2"
	awsMachineKind       = "AWSMachine"
	awsTemplateKind      = "AWSMachineTemplate"
	capiNamespace        = "openshift-cluster-api"
)

var _ = Describe("AWS Fuzz (capi2mapi)", func() {
	infra := &configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	infraCluster := &capav1.AWSCluster{
		Spec: capav1.AWSClusterSpec{
			Region: "us-east-1",
		},
	}

	Context("AWSMachine Conversion", func() {
		fromMachineAndAWSMachineAndAWSCluster := func(machine *capiv1.Machine, infraMachine client.Object, infraCluster client.Object) capi2mapi.MachineAndInfrastructureMachine {
			awsMachine, ok := infraMachine.(*capav1.AWSMachine)
			Expect(ok).To(BeTrue(), "input infra machine should be of type %T, got %T", &capav1.AWSMachine{}, infraMachine)

			awsCluster, ok := infraCluster.(*capav1.AWSCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &capav1.AWSCluster{}, infraCluster)

			return capi2mapi.FromMachineAndAWSMachineAndAWSCluster(machine, awsMachine, awsCluster)
		}

		conversiontest.CAPI2MAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&capav1.AWSMachine{},
			mapi2capi.FromAWSMachineAndInfra,
			fromMachineAndAWSMachineAndAWSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(awsProviderIDFuzzer, awsMachineKind, awsMachineAPIVersion, infra.Status.InfrastructureName),
			awsMachineFuzzerFuncs,
		)
	})

	Context("AWSMachineSet Conversion", func() {
		fromMachineSetAndAWSMachineTemplateAndAWSCluster := func(machineSet *capiv1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) capi2mapi.MachineSetAndMachineTemplate {
			awsMachineTemplate, ok := infraMachineTemplate.(*capav1.AWSMachineTemplate)
			Expect(ok).To(BeTrue(), "input infra machine template should be of type %T, got %T", &capav1.AWSMachineTemplate{}, infraMachineTemplate)

			awsCluster, ok := infraCluster.(*capav1.AWSCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &capav1.AWSCluster{}, infraCluster)

			return capi2mapi.FromMachineSetAndAWSMachineTemplateAndAWSCluster(machineSet, awsMachineTemplate, awsCluster)
		}

		conversiontest.CAPI2MAPIMachineSetRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&capav1.AWSMachineTemplate{},
			mapi2capi.FromAWSMachineSetAndInfra,
			fromMachineSetAndAWSMachineTemplateAndAWSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(awsProviderIDFuzzer, awsTemplateKind, awsMachineAPIVersion, infra.Status.InfrastructureName),
			conversiontest.CAPIMachineSetFuzzerFuncs(awsTemplateKind, awsMachineAPIVersion, infra.Status.InfrastructureName),
			awsMachineFuzzerFuncs,
			awsMachineTemplateFuzzerFuncs,
		)
	})
})

func awsProviderIDFuzzer(c fuzz.Continue) string {
	return "aws:///us-west-2a/i-" + strings.ReplaceAll(c.RandString(), "/", "")
}

//nolint:funlen
func awsMachineFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(nit *capav1.NetworkInterfaceType, c fuzz.Continue) {
			switch c.Int31n(3) {
			case 0:
				*nit = capav1.NetworkInterfaceTypeEFAWithENAInterface
			case 1:
				*nit = capav1.NetworkInterfaceTypeENI
			case 2:
				*nit = ""
			}
		},
		func(imdo *capav1.InstanceMetadataOptions, c fuzz.Continue) {
			c.FuzzNoCustom(imdo)

			// TODO(OCPCLOUD-2710): Fields not yet supported by MAPI.
			imdo.HTTPEndpoint = capav1.InstanceMetadataEndpointStateEnabled
			imdo.HTTPPutResponseHopLimit = 0
			imdo.InstanceMetadataTags = capav1.InstanceMetadataEndpointStateDisabled
		},
		func(tokenState *capav1.HTTPTokensState, c fuzz.Continue) {
			switch c.Int31n(2) {
			case 0:
				*tokenState = capav1.HTTPTokensStateOptional
			case 1:
				*tokenState = capav1.HTTPTokensStateRequired
			}
		},
		func(ami *capav1.AMIReference, c fuzz.Continue) {
			c.FuzzNoCustom(ami)

			// Ensure that the AMI ID is set.
			for ami.ID == nil || *ami.ID == "" {
				c.Fuzz(&ami.ID)
			}
			// Not required for our use case. Can be ignored.
			ami.EKSOptimizedLookupType = nil
		},
		func(ignition *capav1.Ignition, c fuzz.Continue) {
			// We force these fields, so they must be fuzzed in this way.
			*ignition = capav1.Ignition{
				Version:     "3.4",
				StorageType: capav1.IgnitionStorageTypeOptionUnencryptedUserData,
			}
		},
		func(spec *capav1.AWSMachineSpec, c fuzz.Continue) {
			c.FuzzNoCustom(spec)

			fuzzAWSMachineSpecTenancy(&spec.Tenancy, c)
			fuzzAWSMachineSpecMarketType(&spec.MarketType, c)

			// Fields not required for our use case can be ignored.
			spec.ImageLookupFormat = ""
			spec.ImageLookupOrg = ""
			spec.ImageLookupBaseOS = ""
			spec.NetworkInterfaces = nil
			spec.CloudInit = capav1.CloudInit{}
			spec.UncompressedUserData = nil
			spec.PrivateDNSName = nil
			// We don't support this field since the externally managed annotation is added, so it's best to keep this nil.
			spec.SecurityGroupOverrides = nil
		},
		func(m *capav1.AWSMachine, c fuzz.Continue) {
			c.FuzzNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = capav1.GroupVersion.String()
			m.TypeMeta.Kind = "AWSMachine"
		},
	}
}

func fuzzAWSMachineSpecTenancy(tenancy *string, c fuzz.Continue) {
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

func fuzzAWSMachineSpecMarketType(marketType *capav1.MarketType, c fuzz.Continue) {
	switch c.Int31n(4) {
	case 0:
		*marketType = capav1.MarketTypeOnDemand
	case 1:
		*marketType = capav1.MarketTypeSpot
	case 2:
		*marketType = capav1.MarketTypeCapacityBlock
	case 3:
		*marketType = ""
	}
}

func awsMachineTemplateFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(m *capav1.AWSMachineTemplate, c fuzz.Continue) {
			c.FuzzNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = capav1.GroupVersion.String()
			m.TypeMeta.Kind = "AWSMachineTemplate"
		},
	}
}

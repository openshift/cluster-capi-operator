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
package mapi2capi_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	fuzz "github.com/google/gofuzz"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	conversiontest "github.com/openshift/cluster-capi-operator/pkg/conversion/test/fuzz"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	awsProviderSpecKind = "AWSMachineProviderConfig"
)

var _ = Describe("AWS Fuzz (mapi2capi)", func() {
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

		conversiontest.MAPI2CAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			mapi2capi.FromAWSMachineAndInfra,
			fromMachineAndAWSMachineAndAWSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
			conversiontest.MAPIMachineFuzzerFuncs(&mapiv1.AWSMachineProviderConfig{}, awsProviderIDFuzzer),
			awsProviderSpecFuzzerFuncs,
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

		conversiontest.MAPI2CAPIMachineSetRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			mapi2capi.FromAWSMachineSetAndInfra,
			fromMachineSetAndAWSMachineTemplateAndAWSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
			conversiontest.MAPIMachineFuzzerFuncs(&mapiv1.AWSMachineProviderConfig{}, awsProviderIDFuzzer),
			conversiontest.MAPIMachineSetFuzzerFuncs(),
			awsProviderSpecFuzzerFuncs,
		)
	})
})

func awsProviderIDFuzzer(c fuzz.Continue) string {
	return "aws:///us-west-2a/i-" + strings.ReplaceAll(c.RandString(), "/", "")
}

//nolint:funlen,cyclop
func awsProviderSpecFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(nit *mapiv1.AWSNetworkInterfaceType, c fuzz.Continue) {
			switch c.Int31n(3) {
			case 0:
				*nit = mapiv1.AWSEFANetworkInterfaceType
			case 1:
				*nit = mapiv1.AWSENANetworkInterfaceType
			case 2:
				*nit = ""
			}
		},
		func(amiRef *mapiv1.AWSResourceReference, c fuzz.Continue) {
			var amiID string
			c.Fuzz(&amiID)

			*amiRef = mapiv1.AWSResourceReference{
				ID: &amiID,
			}
		},
		func(bdm *mapiv1.BlockDeviceMappingSpec, c fuzz.Continue) {
			c.FuzzNoCustom(bdm)

			// Fuzz required fields so that they are not empty.
			if bdm.EBS == nil {
				ebs := &mapiv1.EBSBlockDeviceSpec{}
				c.Fuzz(ebs)
				bdm.EBS = ebs
			}

			// Clear fields that are not supported by conversion in the block device mapping.
			// These fields exist in the API but are not implemented in MAPA.
			bdm.NoDevice = nil
			bdm.VirtualName = nil
		},
		func(ebs *mapiv1.EBSBlockDeviceSpec, c fuzz.Continue) {
			c.FuzzNoCustom(ebs)

			// Fuzz required fields so that they are not empty.
			// Setting volumeSize to a random int64 value.
			if ebs.VolumeSize == nil {
				ebs.VolumeSize = ptr.To(c.Int63())
			}

			// Clear pointers to empty fields.
			if ebs.VolumeType != nil && *ebs.VolumeType == "" {
				ebs.VolumeType = nil
			}
			if ebs.Iops != nil && *ebs.Iops == 0 {
				ebs.Iops = nil
			}
		},
		func(tenancy *mapiv1.InstanceTenancy, c fuzz.Continue) {
			switch c.Int31n(4) {
			case 0:
				*tenancy = mapiv1.DefaultTenancy
			case 1:
				*tenancy = mapiv1.DedicatedTenancy
			case 2:
				*tenancy = mapiv1.HostTenancy
			case 3:
				*tenancy = ""
			}
		},
		func(marketType *mapiv1.MarketType, c fuzz.Continue) {
			switch c.Int31n(4) {
			case 0:
				*marketType = mapiv1.MarketTypeOnDemand
			case 1:
				*marketType = mapiv1.MarketTypeSpot
			case 2:
				*marketType = mapiv1.MarketTypeCapacityBlock
			case 3:
				*marketType = ""
			}
		},
		func(msa *mapiv1.MetadataServiceAuthentication, c fuzz.Continue) {
			switch c.Intn(2) {
			case 0:
				*msa = mapiv1.MetadataServiceAuthenticationOptional
			case 1:
				*msa = mapiv1.MetadataServiceAuthenticationRequired
				// case 3:
				// 	*msa = "" // Do not fuzz MAPI MetadataServiceAuthentication to the empty value.
				// It will otherwise get converted to CAPA HTTPTokensStateOptional which
				// if converted back to MAPI will become MetadataServiceAuthenticationOptional,
				// resulting in a documented lossy rountrip conversion, which would make the test to fail.
			}
		},
		func(ps *mapiv1.AWSMachineProviderConfig, c fuzz.Continue) {
			c.FuzzNoCustom(ps)

			// The type meta is always set to these values by the conversion.
			ps.APIVersion = mapiv1.GroupVersion.String()
			ps.Kind = awsProviderSpecKind

			// region must match the input AWSCluster so force it here.
			ps.Placement.Region = "us-east-1"

			// Only one value here is valid in terms of fuzzing, so it is hardcoded.
			ps.CredentialsSecret = &corev1.LocalObjectReference{
				Name: mapi2capi.DefaultCredentialsSecretName,
			}

			// Clear fields that are not supported in the provider spec.
			ps.DeviceIndex = 0
			ps.LoadBalancers = nil
			ps.ObjectMeta = metav1.ObjectMeta{}
			// TODO(OCPCLOUD-2713): remove this, temporarily hardcoded for AWS to make the migration to work.
			ps.CredentialsSecret = &corev1.LocalObjectReference{Name: "aws-cloud-credentials"}

			// At least one device mapping must have no device name.
			rootFound := false
			for i := range ps.BlockDevices {
				if ps.BlockDevices[i].DeviceName == nil {
					rootFound = true
					break
				}
			}

			if !rootFound && len(ps.BlockDevices) > 0 {
				ps.BlockDevices[0].DeviceName = nil
			}

			// Clear pointers to empty structs.
			if ps.UserDataSecret != nil && ps.UserDataSecret.Name == "" {
				ps.UserDataSecret = nil
			}
		},
	}
}

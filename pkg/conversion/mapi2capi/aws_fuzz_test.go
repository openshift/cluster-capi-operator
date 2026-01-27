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

	randfill "sigs.k8s.io/randfill"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	conversiontest "github.com/openshift/cluster-capi-operator/pkg/conversion/test/fuzz"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
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

		f := &awsProviderFuzzer{}

		conversiontest.MAPI2CAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			mapi2capi.FromAWSMachineAndInfra,
			fromMachineAndAWSMachineAndAWSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
			conversiontest.MAPIMachineFuzzerFuncs(&mapiv1beta1.AWSMachineProviderConfig{}, &mapiv1beta1.AWSMachineProviderStatus{}, awsProviderIDFuzzer),
			f.FuzzerFuncsMachine,
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

		f := &awsProviderFuzzer{}

		conversiontest.MAPI2CAPIMachineSetRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			mapi2capi.FromAWSMachineSetAndInfra,
			fromMachineSetAndAWSMachineTemplateAndAWSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
			conversiontest.MAPIMachineFuzzerFuncs(&mapiv1beta1.AWSMachineProviderConfig{}, &mapiv1beta1.AWSMachineProviderStatus{}, awsProviderIDFuzzer),
			conversiontest.MAPIMachineSetFuzzerFuncs(),
			f.FuzzerFuncsMachineSet,
		)
	})
})

func awsProviderIDFuzzer(c randfill.Continue) string {
	return "aws:///us-west-2a/i-" + strings.ReplaceAll(c.String(0), "/", "")
}

type awsProviderFuzzer struct {
	conversiontest.MAPIMachineFuzzer
}

func (f *awsProviderFuzzer) fuzzProviderConfig(ps *mapiv1beta1.AWSMachineProviderConfig, c randfill.Continue) {
	c.FillNoCustom(ps)

	// The type meta is always set to these values by the conversion.
	ps.APIVersion = mapiv1beta1.GroupVersion.String()
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

	if ps.CPUOptions != nil && *ps.CPUOptions == (mapiv1beta1.CPUOptions{}) {
		ps.CPUOptions = nil
	}

	// Copy instance-type, region and zone to the struct so they can be set at the machine labels too.
	f.MAPIMachineFuzzer.InstanceType = ps.InstanceType
	f.MAPIMachineFuzzer.Region = ps.Placement.Region
	f.MAPIMachineFuzzer.Zone = ps.Placement.AvailabilityZone
}

func (f *awsProviderFuzzer) fuzzAWSMachineSpecCPUOptions(cpuOpts *mapiv1beta1.CPUOptions, c randfill.Continue) {
	c.FillNoCustom(cpuOpts)

	if cpuOpts.ConfidentialCompute != nil {
		fuzzAWSMachineSpecConfidentialComputePolicy(cpuOpts.ConfidentialCompute, c)
	}
}

func fuzzAWSMachineSpecConfidentialComputePolicy(ccp *mapiv1beta1.AWSConfidentialComputePolicy, c randfill.Continue) {
	switch c.Int31n(2) {
	case 0:
		*ccp = mapiv1beta1.AWSConfidentialComputePolicyDisabled
	case 1:
		*ccp = mapiv1beta1.AWSConfidentialComputePolicySEVSNP
	}
}

func (f *awsProviderFuzzer) fuzzPlacement(placement *mapiv1beta1.Placement, c randfill.Continue) {
	c.FillNoCustom(placement)

	switch c.Int31n(8) {
	case 0:
		placement.Tenancy = mapiv1beta1.DefaultTenancy
		placement.Host = nil
	case 1:
		placement.Tenancy = mapiv1beta1.DedicatedTenancy
		placement.Host = nil
	case 2:
		placement.Tenancy = mapiv1beta1.HostTenancy
		placement.Host = &mapiv1beta1.HostPlacement{
			Affinity:      ptr.To(mapiv1beta1.HostAffinityAnyAvailable),
			DedicatedHost: nil,
		}
	case 3:
		// User-provided host with AllocationStrategy (AnyAvailable affinity)
		placement.Tenancy = mapiv1beta1.HostTenancy
		placement.Host = &mapiv1beta1.HostPlacement{
			Affinity: ptr.To(mapiv1beta1.HostAffinityAnyAvailable),
			DedicatedHost: &mapiv1beta1.DedicatedHost{
				AllocationStrategy: ptr.To(mapiv1beta1.AllocationStrategyUserProvided),
				ID:                 "h-0123456789abcdef0",
			},
		}
	case 4:
		// User-provided host with AllocationStrategy (DedicatedHost affinity)
		placement.Tenancy = mapiv1beta1.HostTenancy
		placement.Host = &mapiv1beta1.HostPlacement{
			Affinity: ptr.To(mapiv1beta1.HostAffinityDedicatedHost),
			DedicatedHost: &mapiv1beta1.DedicatedHost{
				AllocationStrategy: ptr.To(mapiv1beta1.AllocationStrategyUserProvided),
				ID:                 "h-0123456789abcdef0",
			},
		}
	case 5:
		placement.Tenancy = ""
		placement.Host = nil
	case 6:
		// Dynamic host allocation without tags (DynamicHostAllocation is nil)
		placement.Tenancy = mapiv1beta1.HostTenancy
		placement.Host = &mapiv1beta1.HostPlacement{
			Affinity: ptr.To(mapiv1beta1.HostAffinityDedicatedHost),
			DedicatedHost: &mapiv1beta1.DedicatedHost{
				AllocationStrategy:    ptr.To(mapiv1beta1.AllocationStrategyDynamic),
				DynamicHostAllocation: nil,
			},
		}
	case 7:
		// Dynamic host allocation with tags (tags must be sorted by name for consistent roundtrip)
		placement.Tenancy = mapiv1beta1.HostTenancy
		placement.Host = &mapiv1beta1.HostPlacement{
			Affinity: ptr.To(mapiv1beta1.HostAffinityDedicatedHost),
			DedicatedHost: &mapiv1beta1.DedicatedHost{
				AllocationStrategy: ptr.To(mapiv1beta1.AllocationStrategyDynamic),
				DynamicHostAllocation: &mapiv1beta1.DynamicHostAllocationSpec{
					Tags: &[]mapiv1beta1.TagSpecification{
						{Name: "test-key-1", Value: "test-value-1"},
						{Name: "test-key-2", Value: "test-value-2"},
					},
				},
			},
		}
	}
}

func (f *awsProviderFuzzer) FuzzerFuncsMachineSet(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(nit *mapiv1beta1.AWSNetworkInterfaceType, c randfill.Continue) {
			switch c.Int31n(3) {
			case 0:
				*nit = mapiv1beta1.AWSEFANetworkInterfaceType
			case 1:
				*nit = mapiv1beta1.AWSENANetworkInterfaceType
			case 2:
				*nit = ""
			}
		},
		func(amiRef *mapiv1beta1.AWSResourceReference, c randfill.Continue) {
			var amiID string
			c.Fill(&amiID)

			*amiRef = mapiv1beta1.AWSResourceReference{
				ID: &amiID,
			}
		},
		func(bdm *mapiv1beta1.BlockDeviceMappingSpec, c randfill.Continue) {
			c.FillNoCustom(bdm)

			// Fuzz required fields so that they are not empty.
			if bdm.EBS == nil {
				ebs := &mapiv1beta1.EBSBlockDeviceSpec{}
				c.Fill(ebs)
				bdm.EBS = ebs
			}

			// Clear fields that are not supported by conversion in the block device mapping.
			// These fields exist in the API but are not implemented in MAPA.
			bdm.NoDevice = nil
			bdm.VirtualName = nil
		},
		func(ebs *mapiv1beta1.EBSBlockDeviceSpec, c randfill.Continue) {
			c.FillNoCustom(ebs)

			// Fuzz required fields so that they are not empty.
			// Setting volumeSize to a random int64 value.
			if ebs.VolumeSize == nil {
				ebs.VolumeSize = ptr.To(c.Int63())
			}

			// Clear the deprecated deleteOnTermination field as it has no effect and
			// may cause roundtrip conversion failures when the conversion logic ignores it.
			ebs.DeprecatedDeleteOnTermination = nil

			// Clear pointers to empty fields.
			if ebs.VolumeType != nil && *ebs.VolumeType == "" {
				ebs.VolumeType = nil
			}
			if ebs.Iops != nil && *ebs.Iops == 0 {
				ebs.Iops = nil
			}
		},
		func(marketType *mapiv1beta1.MarketType, c randfill.Continue) {
			switch c.Int31n(4) {
			case 0:
				*marketType = mapiv1beta1.MarketTypeOnDemand
			case 1:
				*marketType = mapiv1beta1.MarketTypeSpot
			case 2:
				*marketType = mapiv1beta1.MarketTypeCapacityBlock
			case 3:
				*marketType = ""
			}
		},
		func(msa *mapiv1beta1.MetadataServiceAuthentication, c randfill.Continue) {
			switch c.Intn(2) {
			case 0:
				*msa = mapiv1beta1.MetadataServiceAuthenticationOptional
			case 1:
				*msa = mapiv1beta1.MetadataServiceAuthenticationRequired
				// case 3:
				// 	*msa = "" // Do not fuzz MAPI MetadataServiceAuthentication to the empty value.
				// It will otherwise get converted to CAPA HTTPTokensStateOptional which
				// if converted back to MAPI will become MetadataServiceAuthenticationOptional,
				// resulting in a documented lossy rountrip conversion, which would make the test to fail.
			}
		},
		f.fuzzPlacement,
		f.fuzzProviderConfig,
		f.fuzzAWSMachineSpecCPUOptions,
	}
}

func (f *awsProviderFuzzer) FuzzerFuncsMachine(codecs runtimeserializer.CodecFactory) []interface{} {
	return append(
		f.FuzzerFuncsMachineSet(codecs),
		f.FuzzMachine,
	)
}

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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/randfill"

	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	conversiontest "github.com/openshift/cluster-capi-operator/pkg/conversion/test/fuzz"
)

const (
	vSphereProviderSpecKind = "VSphereMachineProviderSpec"
)

var _ = Describe("vSphere Fuzz (mapi2capi)", func() {
	infra := &configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	infraCluster := &vspherev1.VSphereCluster{
		Spec: vspherev1.VSphereClusterSpec{},
	}

	Context("VSphereMachine Conversion", func() {
		fromMachineAndVSphereMachineAndVSphereCluster := func(machine *clusterv1.Machine, infraMachine client.Object, infraCluster client.Object) capi2mapi.MachineAndInfrastructureMachine {
			vsphereMachine, ok := infraMachine.(*vspherev1.VSphereMachine)
			Expect(ok).To(BeTrue(), "input infra machine should be of type %T, got %T", &vspherev1.VSphereMachine{}, infraMachine)

			vsphereCluster, ok := infraCluster.(*vspherev1.VSphereCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &vspherev1.VSphereCluster{}, infraCluster)

			return capi2mapi.FromMachineAndVSphereMachineAndVSphereCluster(machine, vsphereMachine, vsphereCluster)
		}

		f := &vsphereProviderFuzzer{}

		conversiontest.MAPI2CAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			mapi2capi.FromVSphereMachineAndInfra,
			fromMachineAndVSphereMachineAndVSphereCluster,
			conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
			conversiontest.MAPIMachineFuzzerFuncs(&mapiv1beta1.VSphereMachineProviderSpec{}, nil, vsphereProviderIDFuzzer),
			f.FuzzerFuncsMachine,
		)
	})

	Context("VSphereMachineSet Conversion", func() {
		fromMachineSetAndVSphereMachineTemplateAndVSphereCluster := func(machineSet *clusterv1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) capi2mapi.MachineSetAndMachineTemplate {
			vsphereMachineTemplate, ok := infraMachineTemplate.(*vspherev1.VSphereMachineTemplate)
			Expect(ok).To(BeTrue(), "input infra machine template should be of type %T, got %T", &vspherev1.VSphereMachineTemplate{}, infraMachineTemplate)

			vsphereCluster, ok := infraCluster.(*vspherev1.VSphereCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &vspherev1.VSphereCluster{}, infraCluster)

			return capi2mapi.FromMachineSetAndVSphereMachineTemplateAndVSphereCluster(machineSet, vsphereMachineTemplate, vsphereCluster)
		}

		f := &vsphereProviderFuzzer{}

		conversiontest.MAPI2CAPIMachineSetRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			mapi2capi.FromVSphereMachineSetAndInfra,
			fromMachineSetAndVSphereMachineTemplateAndVSphereCluster,
			conversiontest.ObjectMetaFuzzerFuncs(mapiNamespace),
			conversiontest.MAPIMachineFuzzerFuncs(&mapiv1beta1.VSphereMachineProviderSpec{}, nil, vsphereProviderIDFuzzer),
			conversiontest.MAPIMachineSetFuzzerFuncs(),
			f.FuzzerFuncsMachineSet,
		)
	})
})

func vsphereProviderIDFuzzer(c randfill.Continue) string {
	return "vsphere://" + strings.ReplaceAll(c.String(0), "/", "")
}

type vsphereProviderFuzzer struct {
	conversiontest.MAPIMachineFuzzer
}

func (f *vsphereProviderFuzzer) fuzzProviderSpec(providerSpec *mapiv1beta1.VSphereMachineProviderSpec, c randfill.Continue) {
	c.FillNoCustom(providerSpec)

	// The type meta is always set to these values by the conversion.
	providerSpec.APIVersion = mapiv1beta1.GroupVersion.String()
	providerSpec.Kind = vSphereProviderSpecKind

	// Clear fields that are not supported in the provider spec.
	providerSpec.ObjectMeta = metav1.ObjectMeta{}

	// Clear vmGroup field - it's a MAPI-specific field that doesn't exist in CAPV
	// and is not preserved during conversion (similar to OpenStack's FloatingIP, PrimarySubnet, etc.)
	if providerSpec.Workspace != nil {
		providerSpec.Workspace.VMGroup = ""
	}

	// Only one value here is valid in terms of fuzzing, so it is hardcoded.
	providerSpec.CredentialsSecret = &corev1.LocalObjectReference{
		Name: "vsphere-cloud-credentials",
	}

	// Clear pointers to empty structs.
	if providerSpec.UserDataSecret != nil && providerSpec.UserDataSecret.Name == "" {
		providerSpec.UserDataSecret = nil
	}

	// Normalize empty network devices to nil to match conversion behavior.
	// Empty slices are marshaled as [] but conversion returns nil.
	if providerSpec.Network.Devices != nil && len(providerSpec.Network.Devices) == 0 {
		providerSpec.Network.Devices = nil
	}

	// Copy template and datacenter to the struct so they can be set at the machine labels too.
	// For vSphere: template is used as instance-type, datacenter as region.
	// Zone comes from machine.Spec.FailureDomain (not from provider spec) so we leave it empty.
	f.MAPIMachineFuzzer.InstanceType = providerSpec.Template
	if providerSpec.Workspace != nil {
		f.MAPIMachineFuzzer.Region = providerSpec.Workspace.Datacenter
	}
}

func (f *vsphereProviderFuzzer) FuzzerFuncsMachineSet(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(cloneMode *mapiv1beta1.CloneMode, c randfill.Continue) {
			switch c.Int31n(2) {
			case 0:
				*cloneMode = mapiv1beta1.FullClone
			case 1:
				*cloneMode = mapiv1beta1.LinkedClone
				// case 2:
				//   *cloneMode = "" // Do not fuzz MAPI CloneMode to the empty value.
				// It will otherwise get converted to CAPV FullClone which
				// if converted back to MAPI will become FullClone,
				// resulting in a documented lossy roundtrip conversion, which would make the test to fail.
			}
		},
		func(provisioningMode *mapiv1beta1.ProvisioningMode, c randfill.Continue) {
			switch c.Int31n(4) {
			case 0:
				*provisioningMode = mapiv1beta1.ProvisioningModeThin
			case 1:
				*provisioningMode = mapiv1beta1.ProvisioningModeThick
			case 2:
				*provisioningMode = mapiv1beta1.ProvisioningModeEagerlyZeroed
			case 3:
				*provisioningMode = ""
			}
		},
		f.fuzzProviderSpec,
	}
}

func (f *vsphereProviderFuzzer) FuzzerFuncsMachine(codecs runtimeserializer.CodecFactory) []interface{} {
	return append(
		f.FuzzerFuncsMachineSet(codecs),
		f.FuzzMachine,
	)
}

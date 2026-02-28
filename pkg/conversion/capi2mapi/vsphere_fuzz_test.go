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
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	vsphereMachineKind  = "VSphereMachine"
	vsphereTemplateKind = "VSphereMachineTemplate"
)

var _ = Describe("vSphere Fuzz (capi2mapi)", func() {
	infra := &configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	infraCluster := &vspherev1.VSphereCluster{
		Spec: vspherev1.VSphereClusterSpec{
			Server: "vcenter.example.com",
		},
	}

	Context("VSphereMachine Conversion", func() {
		fromMachineAndVSphereMachineAndVSphereCluster := func(machine *clusterv1.Machine, infraMachine client.Object, infraCluster client.Object) capi2mapi.MachineAndInfrastructureMachine {
			vsphereMachine, ok := infraMachine.(*vspherev1.VSphereMachine)
			Expect(ok).To(BeTrue(), "input infra machine should be of type %T, got %T", &vspherev1.VSphereMachine{}, infraMachine)

			vsphereCluster, ok := infraCluster.(*vspherev1.VSphereCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &vspherev1.VSphereCluster{}, infraCluster)

			return capi2mapi.FromMachineAndVSphereMachineAndVSphereCluster(machine, vsphereMachine, vsphereCluster)
		}

		conversiontest.CAPI2MAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&vspherev1.VSphereMachine{},
			mapi2capi.FromVSphereMachineAndInfra,
			fromMachineAndVSphereMachineAndVSphereCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(vsphereProviderIDFuzzer, vsphereMachineKind, vspherev1.GroupVersion.Group, infra.Status.InfrastructureName),
			vsphereMachineFuzzerFuncs,
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

		conversiontest.CAPI2MAPIMachineSetRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&vspherev1.VSphereMachineTemplate{},
			mapi2capi.FromVSphereMachineSetAndInfra,
			fromMachineSetAndVSphereMachineTemplateAndVSphereCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(vsphereProviderIDFuzzer, vsphereTemplateKind, vspherev1.GroupVersion.Group, infra.Status.InfrastructureName),
			conversiontest.CAPIMachineSetFuzzerFuncs(vsphereTemplateKind, vspherev1.GroupVersion.Group, infra.Status.InfrastructureName),
			vsphereMachineFuzzerFuncs,
			vsphereMachineTemplateFuzzerFuncs,
		)
	})
})

func vsphereProviderIDFuzzer(c randfill.Continue) string {
	return "vsphere://" + strings.ReplaceAll(c.String(0), "/", "")
}

func vsphereMachineFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(m *vspherev1.VSphereMachine, c randfill.Continue) {
			c.FillNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = vspherev1.GroupVersion.String()
			m.TypeMeta.Kind = vsphereMachineKind
		},
		func(cloneMode *vspherev1.CloneMode, c randfill.Continue) {
			switch c.Int31n(3) {
			case 0:
				*cloneMode = vspherev1.FullClone
			case 1:
				*cloneMode = vspherev1.LinkedClone
			case 2:
				*cloneMode = ""
			}
		},
		func(provisioningMode *vspherev1.ProvisioningMode, c randfill.Continue) {
			switch c.Int31n(4) {
			case 0:
				*provisioningMode = vspherev1.ThinProvisioningMode
			case 1:
				*provisioningMode = vspherev1.ThickProvisioningMode
			case 2:
				*provisioningMode = vspherev1.EagerlyZeroedProvisioningMode
			case 3:
				*provisioningMode = ""
			}
		},
		func(spec *vspherev1.VSphereMachineSpec, c randfill.Continue) {
			c.FillNoCustom(spec)

			// Ensure required fields are set
			if spec.Template == "" {
				spec.Template = "test-template"
			}
			if spec.Server == "" {
				spec.Server = "vcenter.example.com"
			}
			if spec.Datacenter == "" {
				spec.Datacenter = "test-datacenter"
			}

			// Fields not supported in MAPI conversion - clear them
			spec.PowerOffMode = ""
			spec.GuestSoftPowerOffTimeout = nil
			spec.FailureDomain = nil
			spec.NamingStrategy = nil
			spec.OS = ""
			spec.HardwareVersion = ""
			spec.StoragePolicyName = ""
			spec.PciDevices = nil
			spec.CustomVMXKeys = nil
			spec.AdditionalDisksGiB = nil
			spec.ProviderID = nil

			// Simplify network spec for compatibility
			for i := range spec.Network.Devices {
				// Clear fields not directly supported in MAPI
				spec.Network.Devices[i].MACAddr = ""
				spec.Network.Devices[i].MTU = nil
				spec.Network.Devices[i].Gateway6 = ""
				spec.Network.Devices[i].Routes = nil
				spec.Network.Devices[i].SearchDomains = nil
				spec.Network.Devices[i].DeviceName = ""
				spec.Network.Devices[i].DHCP6 = false
				spec.Network.Devices[i].SkipIPAllocation = false

				// Clear AddressesFromPools API group as MAPI uses empty string
				for j := range spec.Network.Devices[i].AddressesFromPools {
					spec.Network.Devices[i].AddressesFromPools[j].APIGroup = nil
				}
			}

			// Clear network preferences field
			spec.Network.PreferredAPIServerCIDR = ""
		},
		func(status *vspherev1.VSphereMachineStatus, c randfill.Continue) {
			c.FillNoCustom(status)

			// Clear v1beta2 conditions and other fields not needed in conversion
			status.V1Beta2 = nil
			status.FailureReason = nil
			status.FailureMessage = nil
		},
	}
}

func vsphereMachineTemplateFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(m *vspherev1.VSphereMachineTemplate, c randfill.Continue) {
			c.FillNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = vspherev1.GroupVersion.String()
			m.TypeMeta.Kind = vsphereTemplateKind
		},
		func(spec *vspherev1.VSphereMachineTemplateSpec, c randfill.Continue) {
			c.FillNoCustom(spec)

			// Apply same constraints as VSphereMachineSpec
			if spec.Template.Spec.Template == "" {
				spec.Template.Spec.Template = "test-template"
			}
			if spec.Template.Spec.Server == "" {
				spec.Template.Spec.Server = "vcenter.example.com"
			}
			if spec.Template.Spec.Datacenter == "" {
				spec.Template.Spec.Datacenter = "test-datacenter"
			}

			// Fields not supported in MAPI conversion
			spec.Template.Spec.PowerOffMode = ""
			spec.Template.Spec.GuestSoftPowerOffTimeout = nil
			spec.Template.Spec.FailureDomain = nil
			spec.Template.Spec.NamingStrategy = nil
			spec.Template.Spec.OS = ""
			spec.Template.Spec.HardwareVersion = ""
			spec.Template.Spec.StoragePolicyName = ""
			spec.Template.Spec.PciDevices = nil
			spec.Template.Spec.CustomVMXKeys = nil
			spec.Template.Spec.AdditionalDisksGiB = nil
			spec.Template.Spec.ProviderID = nil

			// Simplify network spec
			for i := range spec.Template.Spec.Network.Devices {
				spec.Template.Spec.Network.Devices[i].MACAddr = ""
				spec.Template.Spec.Network.Devices[i].MTU = nil
				spec.Template.Spec.Network.Devices[i].Gateway6 = ""
				spec.Template.Spec.Network.Devices[i].Routes = nil
				spec.Template.Spec.Network.Devices[i].SearchDomains = nil
				spec.Template.Spec.Network.Devices[i].DeviceName = ""
				spec.Template.Spec.Network.Devices[i].DHCP6 = false
				spec.Template.Spec.Network.Devices[i].SkipIPAllocation = false

				for j := range spec.Template.Spec.Network.Devices[i].AddressesFromPools {
					spec.Template.Spec.Network.Devices[i].AddressesFromPools[j].APIGroup = nil
				}
			}

			spec.Template.Spec.Network.PreferredAPIServerCIDR = ""
		},
	}
}

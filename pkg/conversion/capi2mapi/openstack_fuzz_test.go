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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/uuid"
	configv1 "github.com/openshift/api/config/v1"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/randfill"

	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	conversiontest "github.com/openshift/cluster-capi-operator/pkg/conversion/test/fuzz"
)

const (
	openstackMachineKind  = "OpenStackMachine"
	openstackTemplateKind = "OpenStackMachineTemplate"
)

var _ = Describe("OpenStack Fuzz (capi2mapi)", func() {
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

		conversiontest.CAPI2MAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&openstackv1.OpenStackMachine{},
			mapi2capi.FromOpenStackMachineAndInfra,
			fromMachineAndOpenStackMachineAndOpenStackCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(openstackProviderIDFuzzer, openstackMachineKind, openstackv1.SchemeGroupVersion.Group, infra.Status.InfrastructureName),
			openstackMachineFuzzerFuncs,
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

		conversiontest.CAPI2MAPIMachineSetRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&openstackv1.OpenStackMachineTemplate{},
			mapi2capi.FromOpenStackMachineSetAndInfra,
			fromMachineSetAndOpenStackMachineTemplateAndOpenStackCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(openstackProviderIDFuzzer, openstackTemplateKind, openstackv1.SchemeGroupVersion.Group, infra.Status.InfrastructureName),
			conversiontest.CAPIMachineSetFuzzerFuncs(openstackTemplateKind, openstackv1.SchemeGroupVersion.Group, infra.Status.InfrastructureName),
			openstackMachineFuzzerFuncs,
			openstackMachineTemplateFuzzerFuncs,
		)
	})
})

func openstackProviderIDFuzzer(c randfill.Continue) string {
	return "openstack://" + uuid.NewString()
}

func openstackMachineFuzzerFuncs(codecs runtimeserializer.CodecFactory) []any {
	return []any{
		func(serverGroup *openstackv1.ServerGroupParam, c randfill.Continue) {
			// We require either an ID or a Name, not both.
			switch c.Int31n(2) {
			case 0:
				serverGroup.ID = ptr.To(uuid.NewString())
				serverGroup.Filter = nil
			case 1:
				serverGroup.ID = nil
				serverGroup.Filter = &openstackv1.ServerGroupFilter{Name: ptr.To(uuid.NewString())}
			}
		},
		func(image *openstackv1.ImageParam, c randfill.Continue) {
			// Only image names are supported
			image.ID = nil
			image.ImageRef = nil
			image.Filter = &openstackv1.ImageFilter{Name: ptr.To("custom-image")}
		},
		func(network *openstackv1.NetworkParam, c randfill.Continue) {
			// We require either an ID or a Name, not both.
			switch c.Int31n(2) {
			case 0:
				network.ID = ptr.To(uuid.NewString())
				network.Filter = nil
			case 1:
				network.ID = nil
				network.Filter = &openstackv1.NetworkFilter{Name: uuid.NewString()}
			}
		},
		func(port *openstackv1.PortOpts, c randfill.Continue) {
			c.FillNoCustom(port)

			// Fields not yet supported for conversion.
			port.HostID = nil
			port.PropagateUplinkStatus = nil
			port.ValueSpecs = nil
		},
		func(fixedIP *openstackv1.FixedIP, c randfill.Continue) {
			c.FillNoCustom(fixedIP)

			fixedIP.Subnet = &openstackv1.SubnetParam{
				ID:     ptr.To(uuid.NewString()),
				Filter: &openstackv1.SubnetFilter{},
			}
		},
		func(securityGroup *openstackv1.SecurityGroupParam, c randfill.Continue) {
			// We require either an ID or a Name, not both.
			securityGroup.ID = ptr.To(uuid.NewString())
			securityGroup.Filter = nil
		},
		func(spec *openstackv1.OpenStackMachineSpec, c randfill.Continue) {
			c.FillNoCustom(spec)

			// Fields not yet supported for conversion.
			spec.FlavorID = nil
		},
		func(m *openstackv1.OpenStackMachine, c randfill.Continue) {
			c.FillNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = openstackv1.SchemeGroupVersion.String()
			m.TypeMeta.Kind = openstackMachineKind
		},
	}
}

func openstackMachineTemplateFuzzerFuncs(codecs runtimeserializer.CodecFactory) []any {
	return []any{
		func(m *openstackv1.OpenStackMachineTemplate, c randfill.Continue) {
			c.FillNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = openstackv1.SchemeGroupVersion.String()
			m.TypeMeta.Kind = openstackTemplateKind
		},
	}
}

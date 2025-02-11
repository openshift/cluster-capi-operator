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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	fuzz "github.com/google/gofuzz"
	"github.com/google/uuid"
	configv1 "github.com/openshift/api/config/v1"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

	infraCluster := &capov1.OpenStackCluster{
		Spec: capov1.OpenStackClusterSpec{},
	}

	Context("OpenStackMachine Conversion", func() {
		fromMachineAndOpenStackMachineAndOpenStackCluster := func(machine *capiv1.Machine, infraMachine client.Object, infraCluster client.Object) capi2mapi.MachineAndInfrastructureMachine {
			openstackMachine, ok := infraMachine.(*capov1.OpenStackMachine)
			Expect(ok).To(BeTrue(), "input infra machine should be of type %T, got %T", &capov1.OpenStackMachine{}, infraMachine)

			openstackCluster, ok := infraCluster.(*capov1.OpenStackCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &capov1.OpenStackCluster{}, infraCluster)

			return capi2mapi.FromMachineAndOpenStackMachineAndOpenStackCluster(machine, openstackMachine, openstackCluster)
		}

		conversiontest.CAPI2MAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&capov1.OpenStackMachine{},
			mapi2capi.FromOpenStackMachineAndInfra,
			fromMachineAndOpenStackMachineAndOpenStackCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(openstackProviderIDFuzzer, openstackMachineKind, capov1.SchemeGroupVersion.String(), infra.Status.InfrastructureName),
			openstackMachineFuzzerFuncs,
		)
	})

	Context("OpenStackMachineSet Conversion", func() {
		fromMachineSetAndOpenStackMachineTemplateAndOpenStackCluster := func(machineSet *capiv1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) capi2mapi.MachineSetAndMachineTemplate {
			openstackMachineTemplate, ok := infraMachineTemplate.(*capov1.OpenStackMachineTemplate)
			Expect(ok).To(BeTrue(), "input infra machine template should be of type %T, got %T", &capov1.OpenStackMachineTemplate{}, infraMachineTemplate)

			openstackCluster, ok := infraCluster.(*capov1.OpenStackCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &capov1.OpenStackCluster{}, infraCluster)

			return capi2mapi.FromMachineSetAndOpenStackMachineTemplateAndOpenStackCluster(machineSet, openstackMachineTemplate, openstackCluster)
		}

		conversiontest.CAPI2MAPIMachineSetRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&capov1.OpenStackMachineTemplate{},
			mapi2capi.FromOpenStackMachineSetAndInfra,
			fromMachineSetAndOpenStackMachineTemplateAndOpenStackCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(openstackProviderIDFuzzer, openstackTemplateKind, capov1.SchemeGroupVersion.String(), infra.Status.InfrastructureName),
			conversiontest.CAPIMachineSetFuzzerFuncs(openstackTemplateKind, capov1.SchemeGroupVersion.String(), infra.Status.InfrastructureName),
			openstackMachineFuzzerFuncs,
			openstackMachineTemplateFuzzerFuncs,
		)
	})
})

func openstackProviderIDFuzzer(c fuzz.Continue) string {
	return "openstack://" + uuid.NewString()
}

//nolint:funlen
func openstackMachineFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(serverGroup *capov1.ServerGroupParam, c fuzz.Continue) {
			// We require either an ID or a Name, not both.
			switch c.Int31n(2) {
			case 0:
				serverGroup.ID = ptr.To(uuid.NewString())
				serverGroup.Filter = nil
			case 1:
				serverGroup.ID = nil
				serverGroup.Filter = &capov1.ServerGroupFilter{Name: ptr.To(uuid.NewString())}
			}
		},
		func(image *capov1.ImageParam, c fuzz.Continue) {
			// Only image names are supported
			image.ID = nil
			image.ImageRef = nil
			image.Filter = &capov1.ImageFilter{Name: ptr.To("custom-image")}
		},
		func(network *capov1.NetworkParam, c fuzz.Continue) {
			// We require either an ID or a Name, not both.
			switch c.Int31n(2) {
			case 0:
				network.ID = ptr.To(uuid.NewString())
				network.Filter = nil
			case 1:
				network.ID = nil
				network.Filter = &capov1.NetworkFilter{Name: uuid.NewString()}
			}
		},
		func(port *capov1.PortOpts, c fuzz.Continue) {
			c.FuzzNoCustom(port)

			// Fields not yet supported for conversion.
			port.HostID = nil
			port.PropagateUplinkStatus = nil
			port.ValueSpecs = nil
		},
		func(fixedIP *capov1.FixedIP, c fuzz.Continue) {
			c.FuzzNoCustom(fixedIP)

			fixedIP.Subnet = &capov1.SubnetParam{
				ID:     ptr.To(uuid.NewString()),
				Filter: &capov1.SubnetFilter{},
			}
		},
		func(securityGroup *capov1.SecurityGroupParam, c fuzz.Continue) {
			// We require either an ID or a Name, not both.
			// FIXME: We allow use of security group names in MAPO when requested via the
			// machine spec, but not when requested on a port. How do we express this?
			// switch c.Int31n(2) {
			// case 0:
			// 	securityGroup.ID = ptr.To(uuid.NewString())
			// 	securityGroup.Filter = nil
			// case 1:
			// 	securityGroup.ID = ptr.To("")
			// 	securityGroup.Filter = &capov1.SecurityGroupFilter{
			// 		Name: uuid.NewString(),
			// 	}
			// }
			securityGroup.ID = ptr.To(uuid.NewString())
			securityGroup.Filter = nil
		},
		func(spec *capov1.OpenStackMachineSpec, c fuzz.Continue) {
			c.FuzzNoCustom(spec)

			// Fields not yet supported for conversion.
			spec.FlavorID = nil
		},
		func(m *capov1.OpenStackMachine, c fuzz.Continue) {
			c.FuzzNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = capov1.SchemeGroupVersion.String()
			m.TypeMeta.Kind = openstackMachineKind
		},
	}
}

func openstackMachineTemplateFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(m *capov1.OpenStackMachineTemplate, c fuzz.Continue) {
			c.FuzzNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = capov1.SchemeGroupVersion.String()
			m.TypeMeta.Kind = openstackTemplateKind
		},
	}
}

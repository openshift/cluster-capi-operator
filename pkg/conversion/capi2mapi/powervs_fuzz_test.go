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
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	randfill "sigs.k8s.io/randfill"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/capi2mapi"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	conversiontest "github.com/openshift/cluster-capi-operator/pkg/conversion/test/fuzz"

	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	powerVSMachineKind  = "IBMPowerVSMachine"
	powerVSTemplateKind = "IBMPowerVSMachineTemplate"
)

var _ = Describe("PowerVS Fuzz (capi2mapi)", func() {
	infra := &configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	infraCluster := &ibmpowervsv1.IBMPowerVSCluster{
		Spec: ibmpowervsv1.IBMPowerVSClusterSpec{
			ServiceInstance: &ibmpowervsv1.IBMPowerVSResourceReference{Name: ptr.To("serviceInstance")},
			Zone:            ptr.To("test-zone"),
		},
	}

	Context("IBMPowerVSMachine Conversion", func() {
		fromMachineAndPowerVSMachineAndPowerVSCluster := func(machine *clusterv1.Machine, infraMachine client.Object, infraCluster client.Object) capi2mapi.MachineAndInfrastructureMachine {
			powerVSMachine, ok := infraMachine.(*ibmpowervsv1.IBMPowerVSMachine)
			Expect(ok).To(BeTrue(), "input infra machine should be of type %T, got %T", &ibmpowervsv1.IBMPowerVSMachine{}, infraMachine)

			powerVSCluster, ok := infraCluster.(*ibmpowervsv1.IBMPowerVSCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &ibmpowervsv1.IBMPowerVSCluster{}, infraCluster)

			return capi2mapi.FromMachineAndPowerVSMachineAndPowerVSCluster(machine, powerVSMachine, powerVSCluster)
		}

		conversiontest.CAPI2MAPIMachineRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&ibmpowervsv1.IBMPowerVSMachine{},
			mapi2capi.FromPowerVSMachineAndInfra,
			fromMachineAndPowerVSMachineAndPowerVSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(powerVSProviderIDFuzzer, powerVSMachineKind, ibmpowervsv1.GroupVersion.String(), infra.Status.InfrastructureName),
			powerVSMachineFuzzerFuncs,
		)
	})

	Context("PowerVSMachineSet Conversion", func() {

		fromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster := func(machineSet *clusterv1.MachineSet, infraMachineTemplate client.Object, infraCluster client.Object) capi2mapi.MachineSetAndMachineTemplate {
			powerVSMachineTemplate, ok := infraMachineTemplate.(*ibmpowervsv1.IBMPowerVSMachineTemplate)
			Expect(ok).To(BeTrue(), "input infra machine template should be of type %T, got %T", &ibmpowervsv1.IBMPowerVSMachineTemplate{}, infraMachineTemplate)

			powerVSCluster, ok := infraCluster.(*ibmpowervsv1.IBMPowerVSCluster)
			Expect(ok).To(BeTrue(), "input infra cluster should be of type %T, got %T", &ibmpowervsv1.IBMPowerVSCluster{}, infraCluster)

			return capi2mapi.FromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster(machineSet, powerVSMachineTemplate, powerVSCluster)
		}

		conversiontest.CAPI2MAPIMachineSetRoundTripFuzzTest(
			scheme,
			infra,
			infraCluster,
			&ibmpowervsv1.IBMPowerVSMachineTemplate{},
			mapi2capi.FromPowerVSMachineSetAndInfra,
			fromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster,
			conversiontest.ObjectMetaFuzzerFuncs(capiNamespace),
			conversiontest.CAPIMachineFuzzerFuncs(powerVSProviderIDFuzzer, powerVSMachineKind, ibmpowervsv1.GroupVersion.String(), infra.Status.InfrastructureName),
			conversiontest.CAPIMachineSetFuzzerFuncs(powerVSTemplateKind, ibmpowervsv1.GroupVersion.String(), infra.Status.InfrastructureName),
			powerVSMachineFuzzerFuncs,
			powerVSMachineTemplateFuzzerFuncs,
		)
	})
})

func powerVSProviderIDFuzzer(c randfill.Continue) string {
	// Power VS provider id format: ibmpowervs://<region>/<zone>/<service_instance_id>/<instance_id>
	return fmt.Sprintf("ibmpowervs://tok/tok04/%s/%s", strings.ReplaceAll(c.String(0), "/", ""), strings.ReplaceAll(c.String(0), "/", ""))
}

func powerVSMachineFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(serviceInstance *ibmpowervsv1.IBMPowerVSResourceReference, c randfill.Continue) {
			switch c.Int31n(3) {
			case 0:
				serviceInstance.ID = ptr.To(c.String(0))
			case 1:
				serviceInstance.Name = ptr.To(c.String(0))
			case 2:
				serviceInstance.RegEx = ptr.To(c.String(0))
			}
		},
		func(network *ibmpowervsv1.IBMPowerVSResourceReference, c randfill.Continue) {
			switch c.Int31n(3) {
			case 0:
				network.ID = ptr.To(c.String(0))
			case 1:
				network.Name = ptr.To(c.String(0))
			case 2:
				network.RegEx = ptr.To(c.String(0))
			}
		},
		func(image *ibmpowervsv1.IBMPowerVSResourceReference, c randfill.Continue) {
			switch c.Int31n(3) {
			case 0:
				image.ID = ptr.To(c.String(0))
			case 1:
				image.Name = ptr.To(c.String(0))
			case 2:
				image.RegEx = ptr.To(c.String(0))
			}
		},
		func(spec *ibmpowervsv1.IBMPowerVSMachineSpec, c randfill.Continue) {
			c.FillNoCustom(spec)

			// spec.ServiceInstanceID is deprecated and its advised to use spec.ServiceInstance
			spec.ServiceInstanceID = ""
		},
		func(m *ibmpowervsv1.IBMPowerVSMachine, c randfill.Continue) {
			c.FillNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = ibmpowervsv1.GroupVersion.String()
			m.TypeMeta.Kind = powerVSMachineKind
		},
	}
}

func powerVSMachineTemplateFuzzerFuncs(codecs runtimeserializer.CodecFactory) []interface{} {
	return []interface{}{
		func(m *ibmpowervsv1.IBMPowerVSMachineTemplate, c randfill.Continue) {
			c.FillNoCustom(m)

			// Ensure the type meta is set correctly.
			m.TypeMeta.APIVersion = ibmpowervsv1.GroupVersion.String()
			m.TypeMeta.Kind = powerVSTemplateKind
		},
	}
}

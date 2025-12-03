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
package capi2mapi

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	clusterv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	ibmpowervsv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"

	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("capi2mapi PowerVS conversion", func() {
	capiMachine := clusterv1resourcebuilder.Machine().WithName("test-name").Build()

	capiMachineSet := clusterv1resourcebuilder.MachineSet().WithReplicas(2).
		WithName("test-name").
		WithClusterName("test-name").
		Build()

	powerVSMachineTemplate := ibmpowervsv1resourcebuilder.PowerVSMachineTemplate().WithImageRef(&corev1.LocalObjectReference{Name: "rhcos-capi-powervs"}).
		WithProviderID(ptr.To("test-123")).
		WithServiceInstance(&ibmpowervsv1.IBMPowerVSResourceReference{Name: ptr.To("service-instance")}).
		WithNetwork(ibmpowervsv1.IBMPowerVSResourceReference{Name: ptr.To("network")}).
		Build()

	powerVSMachine := ibmpowervsv1resourcebuilder.PowerVSMachine().WithImageRef(&corev1.LocalObjectReference{Name: "rhcos-capi-powervs"}).
		WithProviderID(ptr.To("test-123")).
		WithServiceInstance(&ibmpowervsv1.IBMPowerVSResourceReference{Name: ptr.To("service-instance")}).
		WithNetwork(ibmpowervsv1.IBMPowerVSResourceReference{Name: ptr.To("network")}).
		Build()

	powerVSCluster := ibmpowervsv1resourcebuilder.PowerVSCluster().WithServiceInstance(&ibmpowervsv1.IBMPowerVSResourceReference{Name: ptr.To("serviceInstance")}).
		WithZone(ptr.To("test-zone")).
		WithReady(true).Build()

	type powerVSCAPI2MAPIMachineConversionInput struct {
		machine            *clusterv1beta1.Machine
		powerVSMachineFunc func() *ibmpowervsv1.IBMPowerVSMachine
		powerVSCluster     *ibmpowervsv1.IBMPowerVSCluster
		expectedErrors     []string
	}

	type powerVSCAPI2MAPIMachineSetConversionInput struct {
		machineSet             *clusterv1beta1.MachineSet
		powerVSMachineTemplate *ibmpowervsv1.IBMPowerVSMachineTemplate
		powerVSCluster         *ibmpowervsv1.IBMPowerVSCluster
		expectedErrors         []string
	}

	var _ = DescribeTable("capi2mapi PowerVS convert CAPI Machine/InfraMachine/InfraCluster to a MAPI Machine",
		func(in powerVSCAPI2MAPIMachineConversionInput) {
			_, _, err := FromMachineAndPowerVSMachineAndPowerVSCluster(
				in.machine,
				in.powerVSMachineFunc(),
				in.powerVSCluster,
			).ToMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors), "king", err,
				"should match expected errors while converting PowerVS CAPI resources to MAPI Machine")
		},

		Entry("With a Base configuration", powerVSCAPI2MAPIMachineConversionInput{
			machine: capiMachine,
			powerVSMachineFunc: func() *ibmpowervsv1.IBMPowerVSMachine {
				return powerVSMachine
			},
			powerVSCluster: powerVSCluster,
			expectedErrors: []string{},
		}),

		Entry("Without service instance", powerVSCAPI2MAPIMachineConversionInput{
			machine: capiMachine,
			powerVSMachineFunc: func() *ibmpowervsv1.IBMPowerVSMachine {
				pvsMachine := *powerVSMachine
				pvsMachine.Spec.ServiceInstance = nil

				return &pvsMachine
			},
			powerVSCluster: powerVSCluster,
			expectedErrors: []string{"spec.serviceInstance: Invalid value: \"null\": unable to convert service instance, service instance is nil"},
		}),

		Entry("With service instance id, without service instance", powerVSCAPI2MAPIMachineConversionInput{
			machine: capiMachine,
			powerVSMachineFunc: func() *ibmpowervsv1.IBMPowerVSMachine {
				pvsMachine := *powerVSMachine
				pvsMachine.Spec.ServiceInstance = nil
				pvsMachine.Spec.ServiceInstanceID = "test-id"

				return &pvsMachine
			},
			powerVSCluster: powerVSCluster,
			expectedErrors: []string{},
		}),

		Entry("Without image", powerVSCAPI2MAPIMachineConversionInput{
			machine: capiMachine,
			powerVSMachineFunc: func() *ibmpowervsv1.IBMPowerVSMachine {
				pvsMachine := *powerVSMachine
				pvsMachine.Spec.ImageRef = nil

				return &pvsMachine
			},
			powerVSCluster: powerVSCluster,
			expectedErrors: []string{"spec.image: Invalid value: \"null\": unable to convert image, image and imageref is nil"},
		}),

		Entry("Without imageref, with image", powerVSCAPI2MAPIMachineConversionInput{
			machine: capiMachine,
			powerVSMachineFunc: func() *ibmpowervsv1.IBMPowerVSMachine {
				pvsMachine := *powerVSMachine
				pvsMachine.Spec.ImageRef = nil
				pvsMachine.Spec.Image = &ibmpowervsv1.IBMPowerVSResourceReference{Name: ptr.To("test-image")}

				return &pvsMachine
			},
			powerVSCluster: powerVSCluster,
			expectedErrors: []string{},
		}),

		Entry("Without network", powerVSCAPI2MAPIMachineConversionInput{
			machine: capiMachine,
			powerVSMachineFunc: func() *ibmpowervsv1.IBMPowerVSMachine {
				pvsMachine := *powerVSMachine
				pvsMachine.Spec.Network = ibmpowervsv1.IBMPowerVSResourceReference{}

				return &pvsMachine
			},
			powerVSCluster: powerVSCluster,
			expectedErrors: []string{"spec.network: Invalid value: v1beta2.IBMPowerVSResourceReference{ID:(*string)(nil), Name:(*string)(nil), RegEx:(*string)(nil)}: unable to convert network to MAPI"},
		}),
	)

	var _ = DescribeTable("capi2mapi PowerVS convert CAPI MachineSet/InfraMachineTemplate/InfraCluster to MAPI MachineSet",
		func(in powerVSCAPI2MAPIMachineSetConversionInput) {
			_, _, err := FromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster(
				in.machineSet,
				in.powerVSMachineTemplate,
				in.powerVSCluster,
			).ToMachineSet()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting PowerVS CAPI resources to MAPI MachineSet")
		},

		Entry("With a Base configuration", powerVSCAPI2MAPIMachineSetConversionInput{
			machineSet:             capiMachineSet,
			powerVSMachineTemplate: powerVSMachineTemplate,
			powerVSCluster:         powerVSCluster,
			expectedErrors:         []string{},
		}),
	)
})

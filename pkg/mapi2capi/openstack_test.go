package mapi2capi

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1alpha1 "github.com/openshift/api/machine/v1alpha1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
)

var _ = Describe("mapi2capi OpenStack", Ordered, func() {

	mapiProviderConfig := &mapiv1alpha1.OpenstackProviderSpec{
		Flavor: "m1.tiny",
	}

	openstackProviderSpec := machinebuilder.OpenStackProviderSpec().Build()
	openstackMAPIMachine := machinebuilder.Machine().WithProviderSpecBuilder(machinebuilder.OpenStackProviderSpec()).Build()
	openstackMAPIMachineSet := machinebuilder.MachineSet().WithProviderSpecBuilder(machinebuilder.OpenStackProviderSpec()).Build()
	infra := configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	BeforeEach(func() {
	})

	AfterEach(func() {
	})

	It("should be able to convert an OpenStack MAPI providerSpec to a CAPI MachineTemplateSpec", func() {
		// Convert a MAPI ProviderSpec to a CAPI InfraMachineTemplateSpec.
		openstackTemplateSpec, warns, err := FromOpenStackProviderSpecAndInfra(openstackProviderSpec, &infra).ToMachineTemplateSpec()
		Expect(openstackTemplateSpec).To(Not(BeNil()), "should not have a nil CAPI MachineTemplateSpec")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert providerSpec to MachineTemplateSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting providerSpec to MachineTemplateSpec")
	})

	It("should be able to convert another OpenStack MAPI providerSpec to a CAPI MachineTemplateSpec", func() {
		// Convert a MAPI ProviderSpec to a CAPI InfraMachineTemplateSpec.
		openstackTemplateSpec, warns, err := FromOpenStackProviderSpecAndInfra(mapiProviderConfig, &infra).ToMachineTemplateSpec()
		Expect(openstackTemplateSpec).To(Not(BeNil()), "should not have a nil CAPI MachineTemplateSpec")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert providerSpec to MachineTemplateSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting providerSpec to MachineTemplateSpec")
	})

	It("should be able to convert a MAPI Machine to a CAPI Machine", func() {
		// Convert a MAPI Machine to a CAPI Core Machine + a CAPI InfraMachineTemplateSpec.
		capiMachine, capiInfraMachineTemplate, warns, err := FromOpenStackMachineAndInfra(openstackMAPIMachine, &infra).ToMachineAndMachineTemplate()
		Expect(capiMachine).To(Not(BeNil()), "should not have a nil CAPI Machine")
		Expect(capiInfraMachineTemplate).To(Not(BeNil()), "should not have a nil CAPI MachineTemplate")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert providerSpec to MachineTemplateSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting providerSpec to MachineTemplateSpec")
	})

	It("should be able to convert a MAPI MachineSet to a CAPI MachineSet", func() {
		// Convert a MAPI MachineSet to a CAPI Core MachineSet + a CAPI InfraMachineTemplateSpec.
		capiMachineSet, capiInfraMachineTemplate, warns, err := FromOpenStackMachineSetAndInfra(openstackMAPIMachineSet, &infra).ToMachineSetAndMachineTemplate()
		Expect(capiMachineSet).To(Not(BeNil()), "should not have a nil CAPI MachineSet")
		Expect(capiInfraMachineTemplate).To(Not(BeNil()), "should not have a nil CAPI MachineTemplate")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert MAPI MachineSet to CAPI MachineSet")
		Expect(warns).To(BeEmpty(), "should have not warned while converting MAPI MachineSet to CAPI MachineSet")
	})
})

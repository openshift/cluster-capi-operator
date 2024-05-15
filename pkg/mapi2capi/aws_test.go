package mapi2capi

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
)

var _ = Describe("mapi2capi AWS", Ordered, func() {

	awsProviderSpec := machinebuilder.AWSProviderSpec().Build()
	awsMAPIMachine := machinebuilder.Machine().WithProviderSpecBuilder(machinebuilder.AWSProviderSpec()).Build()
	awsMAPIMachineSet := machinebuilder.MachineSet().WithProviderSpecBuilder(machinebuilder.AWSProviderSpec()).Build()

	BeforeEach(func() {
	})

	AfterEach(func() {
	})

	It("should be able to convert an AWS MAPI providerSpec to a CAPI MachineTemplateSpec", func() {
		// Convert a MAPI ProviderSpec to a CAPI InfraMachineTemplateSpec.
		awsTemplateSpec, warns, err :=
			FromAWSProviderSpec(awsProviderSpec).ToMachineTemplateSpec()
		Expect(awsTemplateSpec).To(Not(BeNil()), "should not have a nil CAPI MachineTemplateSpec")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert providerSpec to MachineTemplateSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting providerSpec to MachineTemplateSpec")
	})

	It("should be able to convert a MAPI Machine to a CAPI Machine", func() {
		// Convert a MAPI Machine to a CAPI Core Machine + a CAPI InfraMachineTemplateSpec.
		capiMachine, capiInfraMachineTemplate, warns, err :=
			FromAWSMachine(awsMAPIMachine).ToMachineAndMachineTemplate()
		Expect(capiMachine).To(Not(BeNil()), "should not have a nil CAPI Machine")
		Expect(capiInfraMachineTemplate).To(Not(BeNil()), "should not have a nil CAPI MachineTemplate")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert providerSpec to MachineTemplateSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting providerSpec to MachineTemplateSpec")
	})

	It("should be able to convert a MAPI MachineSet to a CAPI MachineSet", func() {
		// Convert a MAPI MachineSet to a CAPI Core MachineSet + a CAPI InfraMachineTemplateSpec.
		capiMachineSet, capiInfraMachineTemplate, warns, err :=
			FromAWSMachineSet(awsMAPIMachineSet).ToMachineSetAndMachineTemplate()
		Expect(capiMachineSet).To(Not(BeNil()), "should not have a nil CAPI MachineSet")
		Expect(capiInfraMachineTemplate).To(Not(BeNil()), "should not have a nil CAPI MachineTemplate")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert MAPI MachineSet to CAPI MachineSet")
		Expect(warns).To(BeEmpty(), "should have not warned while converting MAPI MachineSet to CAPI MachineSet")
	})
})

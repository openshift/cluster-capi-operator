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
package mapi2capi

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"

	"k8s.io/utils/ptr"
)

var _ = Describe("mapi2capi AWS", func() {
	awsMAPIMachine := machinebuilder.Machine().WithProviderSpecBuilder(machinebuilder.AWSProviderSpec()).WithProviderID("aws:///us-west-2a/i-05442bc41c3df969d").Build()
	awsMAPIMachineSet := machinebuilder.MachineSet().WithProviderSpecBuilder(machinebuilder.AWSProviderSpec()).Build()
	awsMAPIMachineSet.Spec.Template.Spec.ProviderID = ptr.To("aws:///us-west-2a/i-05442bc41c3df969d") // TODO: do this in machinebuilder.
	infra := configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	// We need to add error handling for the providerSpec issues before we can enable the following tests.
	PIt("should be able to convert a MAPI Machine to a CAPI Machine", func() {
		// Convert a MAPI Machine to a CAPI Core Machine + a CAPI InfraMachine.
		capiMachine, capiInfraMachine, warns, err :=
			FromAWSMachineAndInfra(awsMAPIMachine, &infra).ToMachineAndInfrastructureMachine()
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert providerSpec to MachineTemplateSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting providerSpec to MachineTemplateSpec")
		Expect(capiMachine).To(Not(BeNil()), "should not have a nil CAPI Machine")
		Expect(capiInfraMachine).To(Not(BeNil()), "should not have a nil CAPI InfrastructureMachine")
	})

	PIt("should be able to convert a MAPI MachineSet to a CAPI MachineSet", func() {
		// Convert a MAPI MachineSet to a CAPI Core MachineSet + a CAPI InfraMachineTemplateSpec.
		capiMachineSet, capiInfraMachineTemplate, warns, err :=
			FromAWSMachineSetAndInfra(awsMAPIMachineSet, &infra).ToMachineSetAndMachineTemplate()
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert MAPI MachineSet to CAPI MachineSet")
		Expect(warns).To(BeEmpty(), "should have not warned while converting MAPI MachineSet to CAPI MachineSet")
		Expect(capiMachineSet).To(Not(BeNil()), "should not have a nil CAPI MachineSet")
		Expect(capiInfraMachineTemplate).To(Not(BeNil()), "should not have a nil CAPI MachineTemplate")
	})

})

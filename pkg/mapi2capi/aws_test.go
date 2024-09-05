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
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"

	"k8s.io/utils/ptr"
)

var _ = Describe("mapi2capi AWS", func() {
	mapiProviderConfig := &mapiv1.AWSMachineProviderConfig{
		AMI: mapiv1.AWSResourceReference{
			ID: ptr.To("testID"),
		},
		InstanceType: "testInstanceType",
		Tags: []mapiv1.TagSpecification{
			{
				Name:  "testName",
				Value: "testValue",
			},
		},
		IAMInstanceProfile: &mapiv1.AWSResourceReference{
			ID: ptr.To("testID"),
		},
		KeyName: ptr.To("testKey"),
		Placement: mapiv1.Placement{
			AvailabilityZone: "zone",
			Tenancy:          mapiv1.DefaultTenancy,
		},
		SecurityGroups: []mapiv1.AWSResourceReference{
			{
				ID: ptr.To("testID"),
			},
		},
		Subnet: mapiv1.AWSResourceReference{
			ID: ptr.To("testID"),
		},
		PublicIP: ptr.To(true),
		SpotMarketOptions: &mapiv1.SpotMarketOptions{
			MaxPrice: ptr.To("1"),
		},
		BlockDevices: []mapiv1.BlockDeviceMappingSpec{
			{
				EBS: &mapiv1.EBSBlockDeviceSpec{
					VolumeSize: ptr.To(int64(1)),
					VolumeType: ptr.To("type1"),
					Iops:       ptr.To(int64(1)),
					Encrypted:  ptr.To(false),
					KMSKey: mapiv1.AWSResourceReference{
						ID: ptr.To("test1"),
					},
				},
			},
			{
				DeviceName: ptr.To("nonrootdevice"),
				EBS: &mapiv1.EBSBlockDeviceSpec{
					VolumeSize: ptr.To(int64(2)),
					VolumeType: ptr.To("type2"),
					Iops:       ptr.To(int64(2)),
					Encrypted:  ptr.To(false),
					KMSKey: mapiv1.AWSResourceReference{
						ID: ptr.To("test2"),
					},
				},
			},
		},
	}

	awsProviderSpec := machinebuilder.AWSProviderSpec().Build()
	awsMAPIMachine := machinebuilder.Machine().WithProviderSpecBuilder(machinebuilder.AWSProviderSpec()).WithProviderID("aws:///us-west-2a/i-05442bc41c3df969d").Build()
	awsMAPIMachineSet := machinebuilder.MachineSet().WithProviderSpecBuilder(machinebuilder.AWSProviderSpec()).Build()
	awsMAPIMachineSet.Spec.Template.Spec.ProviderID = ptr.To("aws:///us-west-2a/i-05442bc41c3df969d") // TODO: do this in machinebuilder.
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

	It("should be able to convert an AWS MAPI providerSpec to a CAPI MachineTemplateSpec", func() {
		// Convert a MAPI ProviderSpec to a CAPI InfraMachineTemplateSpec.
		awsTemplateSpec, warns, err :=
			FromAWSProviderSpecAndInfra(awsProviderSpec, &infra).ToMachineTemplateSpec()
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert providerSpec to MachineTemplateSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting providerSpec to MachineTemplateSpec")
		Expect(awsTemplateSpec).To(Not(BeNil()), "should not have a nil CAPI MachineTemplateSpec")
	})

	It("should be able to convert another AWS MAPI providerSpec to a CAPI MachineTemplateSpec", func() {
		// Convert a MAPI ProviderSpec to a CAPI InfraMachineTemplateSpec.
		awsTemplateSpec, warns, err :=
			FromAWSProviderSpecAndInfra(mapiProviderConfig, &infra).ToMachineTemplateSpec()
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert providerSpec to MachineTemplateSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting providerSpec to MachineTemplateSpec")
		Expect(awsTemplateSpec).To(Not(BeNil()), "should not have a nil CAPI MachineTemplateSpec")
	})

	It("should be able to convert a MAPI Machine to a CAPI Machine", func() {
		// Convert a MAPI Machine to a CAPI Core Machine + a CAPI InfraMachineTemplateSpec.
		capiMachine, capiInfraMachineTemplate, warns, err :=
			FromAWSMachineAndInfra(awsMAPIMachine, &infra).ToMachineAndMachineTemplate()
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert providerSpec to MachineTemplateSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting providerSpec to MachineTemplateSpec")
		Expect(capiMachine).To(Not(BeNil()), "should not have a nil CAPI Machine")
		Expect(capiInfraMachineTemplate).To(Not(BeNil()), "should not have a nil CAPI MachineTemplate")
	})

	It("should be able to convert a MAPI MachineSet to a CAPI MachineSet", func() {
		// Convert a MAPI MachineSet to a CAPI Core MachineSet + a CAPI InfraMachineTemplateSpec.
		capiMachineSet, capiInfraMachineTemplate, warns, err :=
			FromAWSMachineSetAndInfra(awsMAPIMachineSet, &infra).ToMachineSetAndMachineTemplate()
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert MAPI MachineSet to CAPI MachineSet")
		Expect(warns).To(BeEmpty(), "should have not warned while converting MAPI MachineSet to CAPI MachineSet")
		Expect(capiMachineSet).To(Not(BeNil()), "should not have a nil CAPI MachineSet")
		Expect(capiInfraMachineTemplate).To(Not(BeNil()), "should not have a nil CAPI MachineTemplate")
	})

})

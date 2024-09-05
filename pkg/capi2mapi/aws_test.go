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

	"k8s.io/utils/ptr"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("capi2mapi AWS", func() {

	awsMachineTemplate := &capav1.AWSMachineTemplate{
		Spec: capav1.AWSMachineTemplateSpec{
			Template: capav1.AWSMachineTemplateResource{
				Spec: capav1.AWSMachineSpec{
					ProviderID: ptr.To("test-123"),
					InstanceID: ptr.To("test-123"),
					PublicIP:   ptr.To(false),
				},
			},
		},
		Status: capav1.AWSMachineTemplateStatus{},
	}

	awsMachine := &capiv1.Machine{
		Spec: capiv1.MachineSpec{
			ClusterName: "test-123",
		},
	}

	awsMachineSet := &capiv1.MachineSet{
		Spec: capiv1.MachineSetSpec{
			Replicas:    ptr.To(int32(2)),
			ClusterName: "test-123",
			Template: capiv1.MachineTemplateSpec{
				Spec: capiv1.MachineSpec{
					ClusterName: "test-123",
				},
			},
		},
	}

	capiAWSCluster := &capav1.AWSCluster{
		Spec: capav1.AWSClusterSpec{
			Region: "us-east-2",
		},
		Status: capav1.AWSClusterStatus{
			Ready: true,
		},
	}

	It("should be able to convert a CAPI Machine and AWSMachineTemplate to a MAPI AWSProviderSpec", func() {
		awsProviderSpecConfig, warns, err :=
			FromMachineAndAWSMachineTemplateAndAWSCluster(awsMachine, awsMachineTemplate, capiAWSCluster).ToProviderSpec()
		Expect(awsProviderSpecConfig).To(Not(BeNil()), "should not have a nil MAPI ProviderSpecConfig")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert AWSMachineTemplateSpec to AWSProviderSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting AWSMachineTemplateSpec to AWSProviderSpec")
	})

	It("should be able to convert a CAPI Machine to a MAPI Machine", func() {
		mapiMachine, warns, err :=
			FromMachineAndAWSMachineTemplateAndAWSCluster(awsMachine, awsMachineTemplate, capiAWSCluster).ToMachine()
		Expect(mapiMachine).To(Not(BeNil()), "should not have a nil MAPI Machine")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert CAPI Machine/AWSMachineTemplate to MAPI Machine")
		Expect(warns).To(BeEmpty(), "should have not warned while converting CAPI Machine/AWSMachineTemplate to MAPI Machine")
	})

	It("should be able to convert a MAPI MachineSet to a CAPI MachineSet", func() {
		mapiMachineSet, warns, err :=
			FromMachineSetAndAWSMachineTemplateAndAWSCluster(awsMachineSet, awsMachineTemplate, capiAWSCluster).ToMachineSet()
		Expect(mapiMachineSet).To(Not(BeNil()), "should not have a nil MAPI MachineSet")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert CAPI MachineSet/AWSMachineTemplate to MAPI MachineSet")
		Expect(warns).To(BeEmpty(), "should have not warned while converting CAPI MachineSet/AWSMachineTemplate to MAPI MachineSet")
	})
})

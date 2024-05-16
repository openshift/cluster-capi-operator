package capi2mapi

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

var _ = Describe("capi2mapi AWS", Ordered, func() {

	awsMachineTemplate := capav1.AWSMachineTemplate{
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

	awsMachine := capiv1.Machine{
		Spec: capiv1.MachineSpec{
			ClusterName: "test-123",
		},
	}

	awsMachineSet := capiv1.MachineSet{
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

	It("should be able to convert a CAPI Machine and AWSMachineTemplate to a MAPI AWSProviderSpec", func() {
		awsProviderSpecConfig, warns, err :=
			FromMachineAndAWSMachineTemplate(&awsMachine, &awsMachineTemplate).ToProviderSpec()
		Expect(awsProviderSpecConfig).To(Not(BeNil()), "should not have a nil MAPI ProviderSpecConfig")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert AWSMachineTemplateSpec to AWSProviderSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting AWSMachineTemplateSpec to AWSProviderSpec")
	})

	It("should be able to convert a MAPI Machine to a CAPI Machine", func() {
		mapiMachine, warns, err :=
			FromMachineAndAWSMachineTemplate(&awsMachine, &awsMachineTemplate).ToMachine()
		Expect(mapiMachine).To(Not(BeNil()), "should not have a nil MAPI Machine")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert CAPI Machine/AWSMachineTemplate to MAPI Machine")
		Expect(warns).To(BeEmpty(), "should have not warned while converting CAPI Machine/AWSMachineTemplate to MAPI Machine")
	})

	It("should be able to convert a MAPI MachineSet to a CAPI MachineSet", func() {
		mapiMachineSet, warns, err :=
			FromMachineSetAndAWSMachineTemplate(&awsMachineSet, &awsMachineTemplate).ToMachineSet()
		Expect(mapiMachineSet).To(Not(BeNil()), "should not have a nil MAPI MachineSet")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert CAPI MachineSet/AWSMachineTemplate to MAPI MachineSet")
		Expect(warns).To(BeEmpty(), "should have not warned while converting CAPI MachineSet/AWSMachineTemplate to MAPI MachineSet")
	})
})

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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	capibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("capi2mapi PowerVS", func() {

	powerVSMachineTemplate := &capibmv1.IBMPowerVSMachineTemplate{
		Spec: capibmv1.IBMPowerVSMachineTemplateSpec{
			Template: capibmv1.IBMPowerVSMachineTemplateResource{
				Spec: capibmv1.IBMPowerVSMachineSpec{
					ImageRef:        &corev1.LocalObjectReference{Name: "rhcos-capi-powervs"},
					ProviderID:      ptr.To("test-123"),
					ServiceInstance: &capibmv1.IBMPowerVSResourceReference{Name: ptr.To("service-instance")},
					Network:         capibmv1.IBMPowerVSResourceReference{Name: ptr.To("network")},
				},
			},
		},
		Status: capibmv1.IBMPowerVSMachineTemplateStatus{},
	}

	powerVSMachine := &capibmv1.IBMPowerVSMachine{
		Spec: capibmv1.IBMPowerVSMachineSpec{
			ImageRef:        &corev1.LocalObjectReference{Name: "rhcos-capi-powervs"},
			ProviderID:      ptr.To("test-123"),
			ServiceInstance: &capibmv1.IBMPowerVSResourceReference{Name: ptr.To("service-instance")},
			Network:         capibmv1.IBMPowerVSResourceReference{Name: ptr.To("network")},
		},
	}

	capiMachine := &capiv1.Machine{
		Spec: capiv1.MachineSpec{
			ClusterName: "test-123",
		},
	}

	powerVSMachineSet := &capiv1.MachineSet{
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

	capiPowerVSCluster := &capibmv1.IBMPowerVSCluster{
		Spec: capibmv1.IBMPowerVSClusterSpec{
			ServiceInstance: &capibmv1.IBMPowerVSResourceReference{Name: ptr.To("serviceInstance")},
			Zone:            ptr.To("test-zone"),
		},
		Status: capibmv1.IBMPowerVSClusterStatus{
			Ready: true,
		},
	}

	It("should be able to convert a Power VS CAPI Machine to a Power VS MAPI Machine", func() {
		mapiMachine, warns, err :=
			FromMachineAndPowerVSMachineAndPowerVSCluster(capiMachine, powerVSMachine, capiPowerVSCluster).ToMachine()
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert CAPI Machine/PowerVSMachineTemplate to MAPI Machine")
		Expect(mapiMachine).To(Not(BeNil()), "should not have a nil MAPI Machine")
		Expect(warns).To(BeEmpty(), "should have not warned while converting CAPI Machine/PowerVSMachineTemplate to MAPI Machine")
	})

	It("should be able to convert a Power VS MAPI MachineSet to a Power VS CAPI MachineSet", func() {
		mapiMachineSet, warns, err :=
			FromMachineSetAndPowerVSMachineTemplateAndPowerVSCluster(powerVSMachineSet, powerVSMachineTemplate, capiPowerVSCluster).ToMachineSet()
		Expect(mapiMachineSet).To(Not(BeNil()), "should not have a nil MAPI MachineSet")
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert CAPI MachineSet/PowerVSMachineTemplate to MAPI MachineSet")
		Expect(warns).To(BeEmpty(), "should have not warned while converting CAPI MachineSet/PowerVSMachineTemplate to MAPI MachineSet")
	})
})

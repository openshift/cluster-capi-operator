/*
Copyright 2026 Red Hat, Inc.

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
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/api/govmomi/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("Unsupported vSphere fields", func() {
	DescribeTable("should reject unsupported fields in Machines and MachineSets",
		func(rawSpec, fieldName string) {
			spec := vspherev1.VSphereMachineSpec{}
			Expect(json.Unmarshal([]byte(rawSpec), &spec)).To(Succeed())
			spec.Template = "test-template"
			spec.CloneMode = vspherev1.FullClone
			cluster := &vspherev1.VSphereCluster{}

			_, _, err := FromMachineAndVSphereMachineAndVSphereCluster(
				&clusterv1.Machine{}, &vspherev1.VSphereMachine{Spec: spec}, cluster).ToMachine()
			if fieldName == "" {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(MatchError(ContainSubstring("spec." + fieldName)))
			}

			template := &vspherev1.VSphereMachineTemplate{Spec: vspherev1.VSphereMachineTemplateSpec{
				Template: vspherev1.VSphereMachineTemplateResource{Spec: spec},
			}}
			machineSet := &clusterv1.MachineSet{ObjectMeta: metav1.ObjectMeta{Name: "test-machineset"}}

			_, _, err = FromMachineSetAndVSphereMachineTemplateAndVSphereCluster(machineSet, template, cluster).ToMachineSet()
			if fieldName == "" {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(MatchError(ContainSubstring("spec." + fieldName)))
			}
		},
		Entry("when fields are omitted", `{}`, ""),
		Entry("when strings and lists are empty", `{"ftEncryptionMode":"","migrateEncryption":"","cryptoKeyID":"","cryptoProfile":"","storagePolicyName":"","hardwareVersion":"","pciDevices":[]}`, ""),
		Entry("when nestedHV is true", `{"nestedHV":true}`, "nestedHV"),
		Entry("when nestedHV is false", `{"nestedHV":false}`, "nestedHV"),
		Entry("when ftEncryptionMode is configured", `{"ftEncryptionMode":"ftEncryptionRequired"}`, "ftEncryptionMode"),
		Entry("when migrateEncryption is configured", `{"migrateEncryption":"required"}`, "migrateEncryption"),
		Entry("when cryptoKeyID is configured", `{"cryptoKeyID":"key"}`, "cryptoKeyID"),
		Entry("when cryptoProfile is configured", `{"cryptoProfile":"profile"}`, "cryptoProfile"),
		Entry("when storagePolicyName is configured", `{"storagePolicyName":"gold"}`, "storagePolicyName"),
		Entry("when hardwareVersion is configured", `{"hardwareVersion":"vmx-19"}`, "hardwareVersion"),
		Entry("when pciDevices is configured", `{"pciDevices":[{"deviceId":1,"vendorId":1}]}`, "pciDevices"),
	)
})

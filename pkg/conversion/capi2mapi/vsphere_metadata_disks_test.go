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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/consts"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/api/govmomi/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("vSphere disk names and inventory metadata", func() {
	DescribeTable("should generate unique legacy disk names without changing input disks",
		func(existingName string, expectedNames []string) {
			infraMachine := &vspherev1.VSphereMachine{Spec: vspherev1.VSphereMachineSpec{
				VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{
					Template:           "template",
					DataDisks:          []vspherev1.VSphereDisk{{Name: existingName, SizeGiB: 10}},
					AdditionalDisksGiB: []int32{20, 30},
				},
			}}
			original := infraMachine.DeepCopy()
			converter := machineAndVSphereMachineAndVSphereCluster{machine: &clusterv1.Machine{}, vSphereMachine: infraMachine, vSphereCluster: &vspherev1.VSphereCluster{}}
			spec, warnings, errs := converter.toProviderSpec()
			Expect(errs).To(BeEmpty())
			Expect(warnings).To(BeEmpty())
			Expect(spec.DataDisks).To(HaveExactElements(
				HaveField("Name", expectedNames[0]),
				HaveField("Name", expectedNames[1]),
				HaveField("Name", expectedNames[2]),
			))
			Expect(spec.DataDisks).To(HaveExactElements(
				HaveField("SizeGiB", int32(10)),
				HaveField("SizeGiB", int32(20)),
				HaveField("SizeGiB", int32(30)),
			))
			Expect(infraMachine).To(Equal(original))
		},
		Entry("without collisions", "existing", []string{"existing", "additional-disk-0", "additional-disk-1"}),
		Entry("with first generated name reserved", "additional-disk-0", []string{"additional-disk-0", "additional-disk-1", "additional-disk-2"}),
		Entry("with later generated name reserved", "additional-disk-1", []string{"additional-disk-1", "additional-disk-0", "additional-disk-2"}),
	)

	DescribeTable("should omit invalid derived labels while preserving inventory values",
		func(template, datacenter string, expectedLabels map[string]string) {
			converter := machineAndVSphereMachineAndVSphereCluster{
				machine: &clusterv1.Machine{},
				vSphereMachine: &vspherev1.VSphereMachine{Spec: vspherev1.VSphereMachineSpec{
					VirtualMachineCloneSpec: vspherev1.VirtualMachineCloneSpec{Template: template, Datacenter: datacenter},
				}},
				vSphereCluster: &vspherev1.VSphereCluster{},
			}
			labels, _ := converter.buildAdditionalMetadata()
			Expect(labels).To(Equal(expectedLabels))

			spec, _, errs := converter.toProviderSpec()
			Expect(errs).To(BeEmpty())
			Expect(spec.Template).To(Equal(template))
			Expect(spec.Workspace).To(HaveField("Datacenter", datacenter))
		},
		Entry("valid names", "worker-template", "DC", map[string]string{consts.MAPIMachineMetadataLabelInstanceType: "worker-template", consts.MAPIMachineMetadataLabelRegion: "DC"}),
		Entry("template path", "/DC/vm/worker-template", "DC", map[string]string{consts.MAPIMachineMetadataLabelRegion: "DC"}),
		Entry("datacenter path", "worker-template", "/DC", map[string]string{consts.MAPIMachineMetadataLabelInstanceType: "worker-template"}),
		Entry("overlong names", strings.Repeat("t", 64), strings.Repeat("d", 64), map[string]string{}),
	)
})

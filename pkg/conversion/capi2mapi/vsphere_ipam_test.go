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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	vspherev1 "sigs.k8s.io/cluster-api-provider-vsphere/api/govmomi/v1beta1"
)

var _ = Describe("vSphere IPAM mappings", func() {
	It("should reject a nil API group", func() {
		network := vspherev1.NetworkSpec{Devices: []vspherev1.NetworkDeviceSpec{{AddressesFromPools: []corev1.TypedLocalObjectReference{{Kind: "InClusterIPPool", Name: "pool"}}}}}
		_, _, errs := convertCAPVNetworkSpecToMAPI(field.NewPath("spec", "network"), network)
		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Field).To(Equal("spec.network.devices[0].addressesFromPools[0]"))
	})

	DescribeTable("should validate pool references", func(group, kind, resource string) {
		network := vspherev1.NetworkSpec{Devices: []vspherev1.NetworkDeviceSpec{{AddressesFromPools: []corev1.TypedLocalObjectReference{{APIGroup: ptr.To(group), Kind: kind, Name: "pool"}}}}}

		converted, _, errs := convertCAPVNetworkSpecToMAPI(field.NewPath("spec", "network"), network)
		if resource == "" {
			Expect(errs).To(HaveLen(1))
			Expect(errs[0].Field).To(Equal("spec.network.devices[0].addressesFromPools[0]"))

			return
		}

		Expect(errs).To(BeEmpty())
		Expect(converted.Devices[0].AddressesFromPools[0].Resource).To(Equal(resource))
	},
		Entry("namespaced pool", "ipam.cluster.x-k8s.io", "InClusterIPPool", "inclusterippools"),
		Entry("global pool", "ipam.cluster.x-k8s.io", "GlobalInClusterIPPool", "globalinclusterippools"),
		Entry("unknown group", "custom.io", "InClusterIPPool", ""),
		Entry("unknown kind", "ipam.cluster.x-k8s.io", "CustomPool", ""),
		Entry("missing group", "", "InClusterIPPool", ""),
	)
})

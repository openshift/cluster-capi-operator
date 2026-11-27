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

package mapi2capi

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var _ = Describe("vSphere IPAM mappings", func() {
	DescribeTable("should validate pool references", func(group, resource, kind string) {
		network := mapiv1beta1.NetworkSpec{Devices: []mapiv1beta1.NetworkDeviceSpec{{AddressesFromPools: []mapiv1beta1.AddressesFromPool{{Group: group, Resource: resource, Name: "pool"}}}}}

		converted, _, errs := convertVSphereNetworkSpecMAPIToCAPI(field.NewPath("spec", "network"), network)
		if kind == "" {
			Expect(errs).To(HaveLen(1))
			Expect(errs[0].Field).To(Equal("spec.network.devices[0].addressesFromPools[0]"))

			return
		}

		Expect(errs).To(BeEmpty())
		Expect(converted.Devices[0].AddressesFromPools[0].Kind).To(Equal(kind))
	},
		Entry("namespaced pool", "ipam.cluster.x-k8s.io", "inclusterippools", "InClusterIPPool"),
		Entry("global pool", "ipam.cluster.x-k8s.io", "globalinclusterippools", "GlobalInClusterIPPool"),
		Entry("unknown group", "custom.io", "inclusterippools", ""),
		Entry("unknown resource", "ipam.cluster.x-k8s.io", "custompools", ""),
		Entry("missing group", "", "inclusterippools", ""),
	)
})

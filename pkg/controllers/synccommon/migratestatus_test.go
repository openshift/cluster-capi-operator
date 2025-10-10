/*
Copyright 2025 Red Hat, Inc.

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

package synccommon

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
)

var _ = Describe("IsMigrationCancellationRequested", func() {
	DescribeTable("should correctly identify migration cancellation scenarios",
		func(specAuthority, statusAuthority mapiv1beta1.MachineAuthority, synchronizedAPI mapiv1beta1.SynchronizedAPI, expectedMigrationCancellation bool) {
			result := IsMigrationCancellationRequested(specAuthority, statusAuthority, synchronizedAPI)
			Expect(result).To(Equal(expectedMigrationCancellation))
		},
		Entry("Migration cancellation from stuck ClusterAPI migration",
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAuthorityMigrating,
			mapiv1beta1.MachineAPISynchronized,
			true,
		),
		Entry("Migration cancellation from stuck MachineAPI migration",
			mapiv1beta1.MachineAuthorityClusterAPI,
			mapiv1beta1.MachineAuthorityMigrating,
			mapiv1beta1.ClusterAPISynchronized,
			true,
		),
		Entry("Not a migration cancellation - forward migration",
			mapiv1beta1.MachineAuthorityClusterAPI,
			mapiv1beta1.MachineAuthorityMigrating,
			mapiv1beta1.MachineAPISynchronized,
			false,
		),
		Entry("Not a migration cancellation - not migrating",
			mapiv1beta1.MachineAuthorityClusterAPI,
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAPISynchronized,
			false,
		),
		Entry("Not a migration cancellation - no synchronizedAPI",
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAuthorityMigrating,
			mapiv1beta1.SynchronizedAPI(""),
			false,
		),
	)
})

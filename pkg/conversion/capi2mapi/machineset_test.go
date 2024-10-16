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
	capibuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	capabuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("capi2mapi MachineSet conversion", func() {
	var (
		capiMachineSetBase = capibuilder.MachineSet()
	)

	type capi2MAPIMachinesetConversionInput struct {
		machineSetBuilder capibuilder.MachineSetBuilder
		expectedErrors    []string
		expectedWarnings  []string
	}

	var _ = DescribeTable("capi2mapi convert CAPI MachineSet/InfraMachineTemplate/InfraCluster to MAPI MachineSet",
		func(in capi2MAPIMachinesetConversionInput) {
			_, warns, err := FromMachineSetAndAWSMachineTemplateAndAWSCluster(
				in.machineSetBuilder.Build(),
				capabuilder.AWSMachineTemplate().Build(),
				capabuilder.AWSCluster().Build(),
			).ToMachineSet()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting CAPI resources to MAPI MachineSet")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting CAPI resources to MAPI MachineSet")
		},

		// Base Case.
		Entry("With a Base configuration", capi2MAPIMachinesetConversionInput{
			machineSetBuilder: capiMachineSetBase,
			expectedErrors:    []string{},
			expectedWarnings:  []string{},
		}),
		Entry("With unsupported OwnerReferences", capi2MAPIMachinesetConversionInput{
			machineSetBuilder: capiMachineSetBase.WithOwnerReferences([]metav1.OwnerReference{{Name: "a"}}),
			expectedErrors:    []string{"metadata.ownerReferences: Invalid value: []v1.OwnerReference{v1.OwnerReference{APIVersion:\"\", Kind:\"\", Name:\"a\", UID:\"\", Controller:(*bool)(nil), BlockOwnerDeletion:(*bool)(nil)}}: ownerReferences are not supported"},
			expectedWarnings:  []string{},
		}),
	)
})

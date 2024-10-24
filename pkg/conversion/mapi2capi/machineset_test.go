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
	mapiv1 "github.com/openshift/api/machine/v1beta1"
	configbuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"

	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("mapi2capi MachineSet conversion", func() {
	var (
		awsBaseProviderSpec = machinebuilder.AWSProviderSpec().WithLoadBalancers(nil).WithRegion("eu-west-2")
		mapiMachineSetBase  = machinebuilder.MachineSet().WithProviderSpecBuilder(awsBaseProviderSpec)
		infraBase           = configbuilder.Infrastructure().AsAWS("test", "eu-west-2")
	)

	type mapi2CAPIMachinesetConversionInput struct {
		machineSetBuilder machinebuilder.MachineSetBuilder
		infraBuilder      configbuilder.InfrastructureBuilder
		expectedErrors    []string
		expectedWarnings  []string
	}

	var _ = DescribeTable("mapi2capi convert MAPI MachineSet to CAPI MachineSet",
		func(in mapi2CAPIMachinesetConversionInput) {
			_, _, warns, err := FromAWSMachineSetAndInfra(
				in.machineSetBuilder.Build(),
				in.infraBuilder.Build(),
			).ToMachineSetAndMachineTemplate()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting MAPI MachineSet to CAPI MachineSet")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting MAPI MachineSet to CAPI MachineSet")
		},

		// Base Case
		Entry("With a Base configuration", mapi2CAPIMachinesetConversionInput{
			machineSetBuilder: mapiMachineSetBase,
			infraBuilder:      infraBase,
			expectedErrors:    []string{},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported metadata.ownerReferences set", mapi2CAPIMachinesetConversionInput{
			infraBuilder:      infraBase,
			machineSetBuilder: mapiMachineSetBase.WithOwnerReferences([]metav1.OwnerReference{{Name: "a"}}),
			expectedErrors:    []string{"metadata.ownerReferences: Invalid value: []v1.OwnerReference{v1.OwnerReference{APIVersion:\"\", Kind:\"\", Name:\"a\", UID:\"\", Controller:(*bool)(nil), BlockOwnerDeletion:(*bool)(nil)}}: ownerReferences are not supported"},
			expectedWarnings:  []string{},
		}),

		Entry("With unsupported spec.metadata.generateName set", mapi2CAPIMachinesetConversionInput{
			machineSetBuilder: mapiMachineSetBase.WithMachineSpecObjectMeta(mapiv1.ObjectMeta{
				GenerateName: "test-generate-",
			}),
			infraBuilder:     infraBase,
			expectedErrors:   []string{"spec.metadata.generateName: Invalid value: \"test-generate-\": generateName is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.metadata.name set", mapi2CAPIMachinesetConversionInput{
			machineSetBuilder: mapiMachineSetBase.WithMachineSpecObjectMeta(mapiv1.ObjectMeta{
				Name: "test-name",
			}),
			infraBuilder:     infraBase,
			expectedErrors:   []string{"spec.metadata.name: Invalid value: \"test-name\": name is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.metadata.namespace set", mapi2CAPIMachinesetConversionInput{
			infraBuilder: infraBase,
			machineSetBuilder: mapiMachineSetBase.WithMachineSpecObjectMeta(mapiv1.ObjectMeta{
				Namespace: "test-namespace",
			}),
			expectedErrors:   []string{"spec.metadata.namespace: Invalid value: \"test-namespace\": namespace is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.metadata.ownerReferences set", mapi2CAPIMachinesetConversionInput{
			infraBuilder: infraBase,
			machineSetBuilder: mapiMachineSetBase.WithMachineSpecObjectMeta(mapiv1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "v1",
					Kind:       "Pod",
					Name:       "test-pod",
					UID:        "test-uid",
				}},
			}),
			expectedErrors:   []string{"spec.metadata.ownerReferences: Invalid value: []v1.OwnerReference{v1.OwnerReference{APIVersion:\"v1\", Kind:\"Pod\", Name:\"test-pod\", UID:\"test-uid\", Controller:(*bool)(nil), BlockOwnerDeletion:(*bool)(nil)}}: ownerReferences are not supported"},
			expectedWarnings: []string{},
		}),
	)
})

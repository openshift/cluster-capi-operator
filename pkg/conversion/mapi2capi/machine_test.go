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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

var _ = Describe("mapi2capi Machine conversion", func() {
	var (
		awsBaseProviderSpec = machinebuilder.AWSProviderSpec().WithLoadBalancers(nil).WithRegion("eu-west-2")
		mapiMachineBase     = machinebuilder.Machine().WithProviderSpecBuilder(awsBaseProviderSpec)
		infraBase           = configbuilder.Infrastructure().AsAWS("test", "eu-west-2")
	)

	type mapi2CAPIMachineConversionInput struct {
		machineBuilder   machinebuilder.MachineBuilder
		infraBuilder     configbuilder.InfrastructureBuilder
		expectedErrors   []string
		expectedWarnings []string
	}
	var _ = DescribeTable("mapi2capi convert MAPI Machine to a CAPI Machine",
		func(in mapi2CAPIMachineConversionInput) {
			_, _, warns, err := FromAWSMachineAndInfra(
				in.machineBuilder.Build(),
				in.infraBuilder.Build(),
			).ToMachineAndInfrastructureMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors),
				"should match expected errors while converting MAPI Machine to CAPI Machine")
			Expect(warns).To(matchers.ConsistOfSubstrings(in.expectedWarnings),
				"should match expected warnings while converting MAPI Machine to CAPI Machine")
		},

		// Base Case.
		Entry("With a Base configuration", mapi2CAPIMachineConversionInput{
			machineBuilder:   mapiMachineBase,
			infraBuilder:     infraBase,
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported CAPI managed labels", mapi2CAPIMachineConversionInput{
			infraBuilder: infraBase,
			machineBuilder: mapiMachineBase.WithMachineSpecObjectMeta(mapiv1.ObjectMeta{
				Labels: map[string]string{
					"node-role.kubernetes.io/worker": "true",
				},
			}),
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With machineSet owner reference", mapi2CAPIMachineConversionInput{
			infraBuilder: infraBase,
			machineBuilder: mapiMachineBase.WithOwnerReferences([]metav1.OwnerReference{{
				APIVersion:         "machine.openshift.io/v1beta1",
				Kind:               "MachineSet",
				Name:               "test-machineset",
				UID:                "test-uid",
				Controller:         ptr.To(true),
				BlockOwnerDeletion: ptr.To(true),
			}}),
			expectedErrors:   []string{},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.metadata.generateName set", mapi2CAPIMachineConversionInput{
			machineBuilder: mapiMachineBase.WithMachineSpecObjectMeta(mapiv1.ObjectMeta{
				GenerateName: "test-generate-",
			}),
			infraBuilder:     infraBase,
			expectedErrors:   []string{"spec.metadata.generateName: Invalid value: \"test-generate-\": generateName is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.metadata.name set", mapi2CAPIMachineConversionInput{
			machineBuilder: mapiMachineBase.WithMachineSpecObjectMeta(mapiv1.ObjectMeta{
				Name: "test-name",
			}),
			infraBuilder:     infraBase,
			expectedErrors:   []string{"spec.metadata.name: Invalid value: \"test-name\": name is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.metadata.namespace set", mapi2CAPIMachineConversionInput{
			infraBuilder: infraBase,
			machineBuilder: mapiMachineBase.WithMachineSpecObjectMeta(mapiv1.ObjectMeta{
				Namespace: "test-namespace",
			}),
			expectedErrors:   []string{"spec.metadata.namespace: Invalid value: \"test-namespace\": namespace is not supported"},
			expectedWarnings: []string{},
		}),

		Entry("With unsupported spec.taints set", mapi2CAPIMachineConversionInput{
			infraBuilder: infraBase,
			machineBuilder: mapiMachineBase.WithTaints([]corev1.Taint{{
				Key:    "key1",
				Value:  "value1",
				Effect: corev1.TaintEffectNoSchedule,
			}}),
			expectedErrors:   []string{"spec.taints: Invalid value: []v1.Taint{v1.Taint{Key:\"key1\", Value:\"value1\", Effect:\"NoSchedule\", TimeAdded:<nil>}}: taints are not currently supported"},
			expectedWarnings: []string{},
		}),
	)
})

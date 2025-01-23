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
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	powervsbuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1"
	machinebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"
)

var _ = Describe("mapi2capi PowerVS conversion", func() {

	var (
		powerVSBaseProviderSpec   = powervsbuilder.PowerVSProviderSpec().WithLoadBalancers(nil)
		powerVSMAPIMachineBase    = machinebuilder.Machine().WithProviderSpecBuilder(powerVSBaseProviderSpec)
		powerVSMAPIMachineSetBase = machinebuilder.MachineSet().WithProviderSpecBuilder(powerVSBaseProviderSpec)

		infra = &configv1.Infrastructure{
			Spec:   configv1.InfrastructureSpec{},
			Status: configv1.InfrastructureStatus{InfrastructureName: "sample-cluster-name"},
		}

		mustConvertPowerVSProviderSpecToRawExtension = func(spec *mapiv1.PowerVSMachineProviderConfig) *runtime.RawExtension {
			if spec == nil {
				return &runtime.RawExtension{}
			}

			rawBytes, err := json.Marshal(spec)
			if err != nil {
				panic(fmt.Sprintf("unable to convert (marshal) test PowerVSProviderSpec to runtime.RawExtension: %v", err))
			}

			return &runtime.RawExtension{
				Raw: rawBytes,
			}
		}
	)

	type powerVSMAPI2CAPIConversionInput struct {
		machineBuilder machinebuilder.MachineBuilder
		infra          *configv1.Infrastructure
		expectedErrors []string
	}

	type powerVSMAPI2CAPIMachineSetConversionInput struct {
		machineSetBuilder machinebuilder.MachineSetBuilder
		infra             *configv1.Infrastructure
		expectedErrors    []string
	}

	var _ = DescribeTable("mapi2capi PowerVS convert MAPI Machine",
		func(in powerVSMAPI2CAPIConversionInput) {
			_, _, _, err := FromPowerVSMachineAndInfra(in.machineBuilder.Build(), in.infra).ToMachineAndInfrastructureMachine()
			fmt.Println(err)
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors), "should match expected errors while converting an PowerVS MAPI Machine to CAPI")

		},

		// Base Case.
		Entry("With a Base configuration", powerVSMAPI2CAPIConversionInput{
			machineBuilder: powerVSMAPIMachineBase,
			infra:          infra,
			expectedErrors: []string{},
		}),

		// Only Error.
		Entry("Without ServiceInstance", powerVSMAPI2CAPIConversionInput{
			machineBuilder: powerVSMAPIMachineBase.WithProviderSpecBuilder(powerVSBaseProviderSpec.WithServiceInstance(mapiv1.PowerVSResource{})),
			infra:          infra,
			expectedErrors: []string{
				"spec.providerSpec.value.serviceInstance.type: Invalid value: \"\": unknown type",
			},
		}),

		Entry("Without Image", powerVSMAPI2CAPIConversionInput{
			machineBuilder: powerVSMAPIMachineBase.WithProviderSpecBuilder(powerVSBaseProviderSpec.WithImage(mapiv1.PowerVSResource{})),
			infra:          infra,
			expectedErrors: []string{
				"spec.providerSpec.value.image.type: Invalid value: \"\": unknown type",
			},
		}),

		Entry("Without Network", powerVSMAPI2CAPIConversionInput{
			machineBuilder: powerVSMAPIMachineBase.WithProviderSpecBuilder(powerVSBaseProviderSpec.WithNetwork(mapiv1.PowerVSResource{})),
			infra:          infra,
			expectedErrors: []string{
				"spec.providerSpec.value.network.type: Invalid value: \"\": unknown type",
			},
		}),

		Entry("With LoadBalancer", powerVSMAPI2CAPIConversionInput{
			machineBuilder: powerVSMAPIMachineBase.WithProviderSpecBuilder(powerVSBaseProviderSpec.WithLoadBalancers([]mapiv1.LoadBalancerReference{
				{Name: "LB-One",
					Type: mapiv1.ApplicationLoadBalancerType,
				},
			})),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.loadBalancers: Invalid value: []v1.LoadBalancerReference{v1.LoadBalancerReference{Name:\"LB-One\", Type:\"Application\"}}: loadBalancers are not supported",
			},
		}),

		Entry("With metadata in provider spec", powerVSMAPI2CAPIConversionInput{
			machineBuilder: powerVSMAPIMachineBase.WithProviderSpec(machinev1beta1.ProviderSpec{
				Value: mustConvertPowerVSProviderSpecToRawExtension(&mapiv1.PowerVSMachineProviderConfig{
					ObjectMeta:      metav1.ObjectMeta{Name: "test"},
					ServiceInstance: mapiv1.PowerVSResource{Type: mapiv1.PowerVSResourceTypeID, ID: ptr.To("default-serviceInstanceID")},
					Image:           mapiv1.PowerVSResource{Type: mapiv1.PowerVSResourceTypeID, ID: ptr.To("default-imageID")},
					Network:         mapiv1.PowerVSResource{Type: mapiv1.PowerVSResourceTypeID, ID: ptr.To("default-networkID")},
				}),
			}),
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.metadata: Invalid value: v1.ObjectMeta{Name:\"test\", GenerateName:\"\", Namespace:\"\", SelfLink:\"\", UID:\"\", ResourceVersion:\"\", Generation:0, CreationTimestamp:time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC), DeletionTimestamp:<nil>, DeletionGracePeriodSeconds:(*int64)(nil), Labels:map[string]string(nil), Annotations:map[string]string(nil), OwnerReferences:[]v1.OwnerReference(nil), Finalizers:[]string(nil), ManagedFields:[]v1.ManagedFieldsEntry(nil)}: metadata is not supported",
			},
		}),
	)

	var _ = DescribeTable("mapi2capi PowerVS convert MAPI MachineSet",
		func(in powerVSMAPI2CAPIMachineSetConversionInput) {
			_, _, _, err := FromPowerVSMachineSetAndInfra(in.machineSetBuilder.Build(), in.infra).ToMachineSetAndMachineTemplate()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors), "should match expected errors while converting an PowerVS MAPI MachineSet to CAPI")
		},

		Entry("With a Base configuration", powerVSMAPI2CAPIMachineSetConversionInput{
			machineSetBuilder: powerVSMAPIMachineSetBase,
			infra:             infra,
			expectedErrors:    []string{},
		}),
	)
})

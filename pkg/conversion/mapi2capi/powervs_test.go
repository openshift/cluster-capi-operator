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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/test/matchers"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

var _ = Describe("mapi2capi PowerVS conversion", func() {

	var (
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
		machineFunc    func() *machinev1beta1.Machine
		infra          *configv1.Infrastructure
		expectedErrors []string
	}

	type powerVSMAPI2CAPIMachineSetConversionInput struct {
		machineSetFunc func() *machinev1beta1.MachineSet
		infra          *configv1.Infrastructure
		expectedErrors []string
	}

	var _ = DescribeTable("mapi2capi PowerVS convert MAPI Machine",
		func(in powerVSMAPI2CAPIConversionInput) {
			_, _, _, err := FromPowerVSMachineAndInfra(in.machineFunc(), in.infra).ToMachineAndInfrastructureMachine()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors), "should match expected errors while converting an PowerVS MAPI Machine to CAPI")
		},

		// Base Case.
		Entry("With a Base configuration", powerVSMAPI2CAPIConversionInput{
			machineFunc: func() *machinev1beta1.Machine {
				return &machinev1beta1.Machine{
					Spec: machinev1beta1.MachineSpec{
						ProviderSpec: machinev1beta1.ProviderSpec{
							Value: mustConvertPowerVSProviderSpecToRawExtension(getPowerVSProviderSpec()),
						},
						ProviderID: ptr.To("test-123"),
					},
				}
			},
			infra:          infra,
			expectedErrors: []string{},
		}),

		// Only Error.
		Entry("Without ServiceInstance", powerVSMAPI2CAPIConversionInput{
			machineFunc: func() *machinev1beta1.Machine {
				providerSpec := getPowerVSProviderSpec()
				providerSpec.ServiceInstance = mapiv1.PowerVSResource{}
				powerVSMAPIMachine := &machinev1beta1.Machine{
					Spec: machinev1beta1.MachineSpec{
						ProviderSpec: machinev1beta1.ProviderSpec{
							Value: mustConvertPowerVSProviderSpecToRawExtension(providerSpec),
						},
						ProviderID: ptr.To("test-123"),
					},
				}

				return powerVSMAPIMachine
			},
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.serviceInstance.type: Invalid value: \"\": unknown type",
			},
		}),

		Entry("Without Image", powerVSMAPI2CAPIConversionInput{
			machineFunc: func() *machinev1beta1.Machine {
				providerSpec := getPowerVSProviderSpec()
				providerSpec.Image = mapiv1.PowerVSResource{}
				powerVSMAPIMachine := &machinev1beta1.Machine{
					Spec: machinev1beta1.MachineSpec{
						ProviderSpec: machinev1beta1.ProviderSpec{
							Value: mustConvertPowerVSProviderSpecToRawExtension(providerSpec),
						},
						ProviderID: ptr.To("test-123"),
					},
				}

				return powerVSMAPIMachine
			},
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.image.type: Invalid value: \"\": unknown type",
			},
		}),

		Entry("Without Network", powerVSMAPI2CAPIConversionInput{
			machineFunc: func() *machinev1beta1.Machine {
				providerSpec := getPowerVSProviderSpec()
				providerSpec.Network = mapiv1.PowerVSResource{}
				powerVSMAPIMachine := &machinev1beta1.Machine{
					Spec: machinev1beta1.MachineSpec{
						ProviderSpec: machinev1beta1.ProviderSpec{
							Value: mustConvertPowerVSProviderSpecToRawExtension(providerSpec),
						},
						ProviderID: ptr.To("test-123"),
					},
				}

				return powerVSMAPIMachine
			},
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.network.type: Invalid value: \"\": unknown type",
			},
		}),

		Entry("With LoadBalancer", powerVSMAPI2CAPIConversionInput{
			machineFunc: func() *machinev1beta1.Machine {
				providerSpec := getPowerVSProviderSpec()
				providerSpec.LoadBalancers = append(providerSpec.LoadBalancers, mapiv1.LoadBalancerReference{
					Name: "LB-One",
					Type: mapiv1.ApplicationLoadBalancerType,
				})
				powerVSMAPIMachine := &machinev1beta1.Machine{
					Spec: machinev1beta1.MachineSpec{
						ProviderSpec: machinev1beta1.ProviderSpec{
							Value: mustConvertPowerVSProviderSpecToRawExtension(providerSpec),
						},
						ProviderID: ptr.To("test-123"),
					},
				}

				return powerVSMAPIMachine
			},
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.loadBalancers: Invalid value: []v1.LoadBalancerReference{v1.LoadBalancerReference{Name:\"LB-One\", Type:\"Application\"}}: loadBalancers are not supported",
			},
		}),

		Entry("With metadata in provider spec", powerVSMAPI2CAPIConversionInput{

			machineFunc: func() *machinev1beta1.Machine {
				providerSpec := getPowerVSProviderSpec()
				providerSpec.ObjectMeta = metav1.ObjectMeta{Name: "test"}
				powerVSMAPIMachine := &machinev1beta1.Machine{
					Spec: machinev1beta1.MachineSpec{
						ProviderSpec: machinev1beta1.ProviderSpec{
							Value: mustConvertPowerVSProviderSpecToRawExtension(providerSpec),
						},
						ProviderID: ptr.To("test-123"),
					},
				}

				return powerVSMAPIMachine
			},
			infra: infra,
			expectedErrors: []string{
				"spec.providerSpec.value.metadata: Invalid value: v1.ObjectMeta{Name:\"test\", GenerateName:\"\", Namespace:\"\", SelfLink:\"\", UID:\"\", ResourceVersion:\"\", Generation:0, CreationTimestamp:time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC), DeletionTimestamp:<nil>, DeletionGracePeriodSeconds:(*int64)(nil), Labels:map[string]string(nil), Annotations:map[string]string(nil), OwnerReferences:[]v1.OwnerReference(nil), Finalizers:[]string(nil), ManagedFields:[]v1.ManagedFieldsEntry(nil)}: metadata is not supported",
			},
		}),
	)

	var _ = DescribeTable("mapi2capi PowerVS convert MAPI MachineSet",
		func(in powerVSMAPI2CAPIMachineSetConversionInput) {
			_, _, _, err := FromPowerVSMachineSetAndInfra(in.machineSetFunc(), in.infra).ToMachineSetAndMachineTemplate()
			Expect(err).To(matchers.ConsistOfMatchErrorSubstrings(in.expectedErrors), "should match expected errors while converting an PowerVS MAPI MachineSet to CAPI")
		},

		Entry("With a Base configuration", powerVSMAPI2CAPIMachineSetConversionInput{
			machineSetFunc: func() *machinev1beta1.MachineSet {
				return &machinev1beta1.MachineSet{
					Spec: machinev1beta1.MachineSetSpec{
						Template: machinev1beta1.MachineTemplateSpec{
							Spec: machinev1beta1.MachineSpec{
								ProviderSpec: machinev1beta1.ProviderSpec{
									Value: mustConvertPowerVSProviderSpecToRawExtension(getPowerVSProviderSpec()),
								},
								ProviderID: ptr.To("test-123"),
							},
						},
					},
				}
			},
			infra:          infra,
			expectedErrors: []string{},
		}),
	)
})

// TODO: We should add this to machine builder
// getPowerVSProviderSpec builds and returns PowerVSProviderConfig.
func getPowerVSProviderSpec() *mapiv1.PowerVSMachineProviderConfig {
	return &mapiv1.PowerVSMachineProviderConfig{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{},
		UserDataSecret: &mapiv1.PowerVSSecretReference{
			Name: "worker-user-data",
		},
		CredentialsSecret: &mapiv1.PowerVSSecretReference{
			Name: "powervs-credentials",
		},
		ServiceInstance: mapiv1.PowerVSResource{
			Type: mapiv1.PowerVSResourceTypeID,
			ID:   ptr.To("1234"),
		},
		Image: mapiv1.PowerVSResource{
			Type: mapiv1.PowerVSResourceTypeName,
			Name: ptr.To("rhcos-ipi-sa04-418nig-jqw97"),
		},
		Network: mapiv1.PowerVSResource{
			Type:  mapiv1.PowerVSResourceTypeRegEx,
			RegEx: ptr.To("^DHCPSERVER.*ipi-sa04-418nig-jqw97.*_Private$"),
		},
		KeyPairName:   "ipi-sa04-418nig-jqw97-key",
		SystemType:    "s922",
		ProcessorType: mapiv1.PowerVSProcessorTypeShared,
		Processors:    intstr.FromString("2"),
		MemoryGiB:     32,
	}
}

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

	mapiv1 "github.com/openshift/api/machine/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
)

var _ = Describe("mapi2capi PowerVS", func() {

	powerVSProviderSpec := getPowerVSProviderSpec()

	powerVSRawExt, err := rawExtensionFromPowerVSProviderSpec(powerVSProviderSpec)
	Expect(err).ToNot(HaveOccurred())

	powerVSMAPIMachine := &machinev1beta1.Machine{
		Spec: machinev1beta1.MachineSpec{
			ProviderSpec: machinev1beta1.ProviderSpec{
				Value: powerVSRawExt,
			},
			ProviderID: ptr.To("test-123"),
		},
	}

	powerVSMAPIMachineSet := &machinev1beta1.MachineSet{
		Spec: machinev1beta1.MachineSetSpec{
			Selector: metav1.LabelSelector{},
			Template: machinev1beta1.MachineTemplateSpec{
				Spec: machinev1beta1.MachineSpec{
					ProviderSpec: machinev1beta1.ProviderSpec{
						Value: powerVSRawExt,
					},
					ProviderID: ptr.To("test-123"),
				},
			},
		},
	}

	infra := configv1.Infrastructure{
		Spec: configv1.InfrastructureSpec{},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "sample-cluster-name",
		},
	}

	// We need to add error handling for the providerSpec issues before we can enable the following tests.
	It("should be able to convert a MAPI Power VS Machine to a CAPI Machine", func() {
		// Convert a MAPI Machine to a CAPI Core Machine + a CAPI InfraMachine.
		capiMachine, capiInfraMachine, warns, err :=
			FromPowerVSMachineAndInfra(powerVSMAPIMachine, &infra).ToMachineAndInfrastructureMachine()
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert providerSpec to MachineTemplateSpec")
		Expect(warns).To(BeEmpty(), "should have not warned while converting providerSpec to MachineTemplateSpec")
		Expect(capiMachine).To(Not(BeNil()), "should not have a nil CAPI Machine")
		Expect(capiInfraMachine).To(Not(BeNil()), "should not have a nil CAPI InfrastructureMachine")
	})

	It("should be able to convert a MAPI Power VS MachineSet to a CAPI MachineSet", func() {
		// Convert a MAPI MachineSet to a CAPI Core MachineSet + a CAPI InfraMachineTemplateSpec.
		capiMachineSet, capiInfraMachineTemplate, warns, err :=
			FromPowerVSMachineSetAndInfra(powerVSMAPIMachineSet, &infra).ToMachineSetAndMachineTemplate()
		Expect(err).ToNot(HaveOccurred(), "should have been able to convert MAPI MachineSet to CAPI MachineSet")
		Expect(warns).To(BeEmpty(), "should have not warned while converting MAPI MachineSet to CAPI MachineSet")
		Expect(capiMachineSet).To(Not(BeNil()), "should not have a nil CAPI MachineSet")
		Expect(capiInfraMachineTemplate).To(Not(BeNil()), "should not have a nil CAPI MachineTemplate")
	})

})

// getPowerVSProviderSpec builds and returns PowerVSProviderConfig.
func getPowerVSProviderSpec() *mapiv1.PowerVSMachineProviderConfig {
	// TODO: May be we can add this in machine builder?
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

// rawExtensionFromPowerVSProviderSpec marshals the machine provider spec.
func rawExtensionFromPowerVSProviderSpec(spec *mapiv1.PowerVSMachineProviderConfig) (*runtime.RawExtension, error) {
	if spec == nil {
		return &runtime.RawExtension{}, nil
	}

	rawBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("error marshalling providerSpec: %w", err)
	}

	return &runtime.RawExtension{
		Raw: rawBytes,
	}, nil
}

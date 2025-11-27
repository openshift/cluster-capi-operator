// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	powerVSMachineTemplateName = "powervs-machine-template"
)

var _ = Describe("[sig-cluster-lifecycle][Feature:ClusterAPI][platform:powervs][Disruptive] Cluster API IBMPowerVS MachineSet", Ordered, Label("Conformance"), Label("Serial"), func() {
	var powerVSMachineTemplate *ibmpowervsv1.IBMPowerVSMachineTemplate
	var machineSet *clusterv1.MachineSet
	var mapiMachineSpec *mapiv1.PowerVSMachineProviderConfig

	BeforeAll(func() {
		InitCommonVariables()
		if platform != configv1.PowerVSPlatformType {
			Skip("Skipping PowerVS E2E tests")
		}
		mapiMachineSpec = framework.GetMAPIProviderSpec[mapiv1.PowerVSMachineProviderConfig](ctx, cl)
	})

	AfterEach(func() {
		if platform != configv1.PowerVSPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping PowerVS E2E tests")
		}
		framework.DeleteMachineSets(ctx, cl, machineSet)
		framework.WaitForMachineSetsDeleted(machineSet)
		framework.DeleteObjects(ctx, cl, powerVSMachineTemplate)
	})

	It("should be able to run a machine", func() {
		powerVSMachineTemplate = createIBMPowerVSMachineTemplate(ctx, cl, mapiMachineSpec)

		machineSet = framework.CreateMachineSet(ctx, cl, framework.NewMachineSetParams(
			"ibmpowervs-machineset",
			clusterName,
			"",
			1,
			clusterv1.ContractVersionedObjectReference{
				Kind:     "IBMPowerVSMachineTemplate",
				APIGroup: infraAPIGroup,
				Name:     powerVSMachineTemplateName,
			},
			"worker-user-data",
		))
		framework.WaitForMachineSet(ctx, cl, machineSet.Name, machineSet.Namespace, framework.WaitLong)
	})

})

func createIBMPowerVSMachineTemplate(ctx context.Context, cl client.Client, mapiProviderSpec *mapiv1.PowerVSMachineProviderConfig) *ibmpowervsv1.IBMPowerVSMachineTemplate {
	GinkgoHelper()
	By("Creating IBMPowerVS machine template")

	Expect(mapiProviderSpec).ToNot(BeNil(), "expected MAPI ProviderSpec to not be nil")
	Expect(mapiProviderSpec.ServiceInstance).ToNot(BeNil(), "expected ServiceInstance to not be nil")
	Expect(mapiProviderSpec.KeyPairName).ToNot(BeEmpty(), "expected KeyPairName to not be empty")
	Expect(mapiProviderSpec.Image).ToNot(BeNil(), "expected Image to not be nil")
	Expect(mapiProviderSpec.SystemType).ToNot(BeEmpty(), "expected SystemType to not be empty")
	Expect(mapiProviderSpec.ProcessorType).ToNot(BeEmpty(), "expected ProcessorType to not be empty")

	ibmPowerVSMachineSpec := ibmpowervsv1.IBMPowerVSMachineSpec{
		ServiceInstance: getServiceInstance(mapiProviderSpec.ServiceInstance),
		SSHKey:          mapiProviderSpec.KeyPairName,
		Image: &ibmpowervsv1.IBMPowerVSResourceReference{
			Name: mapiProviderSpec.Image.Name,
		},
		SystemType:    mapiProviderSpec.SystemType,
		ProcessorType: ibmpowervsv1.PowerVSProcessorType(mapiProviderSpec.ProcessorType),
		Processors:    mapiProviderSpec.Processors,
		MemoryGiB:     mapiProviderSpec.MemoryGiB,
		Network:       getNetworkResourceReference(mapiProviderSpec.Network),
	}

	ibmPowerVSMachineTemplate := &ibmpowervsv1.IBMPowerVSMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      powerVSMachineTemplateName,
			Namespace: framework.CAPINamespace,
		},
		Spec: ibmpowervsv1.IBMPowerVSMachineTemplateSpec{
			Template: ibmpowervsv1.IBMPowerVSMachineTemplateResource{
				Spec: ibmPowerVSMachineSpec,
			},
		},
	}

	if err := cl.Create(ctx, ibmPowerVSMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred(), "should not fail creating IBMPowerVS machine template")
	}

	return ibmPowerVSMachineTemplate
}

func getNetworkResourceReference(networkResource mapiv1.PowerVSResource) ibmpowervsv1.IBMPowerVSResourceReference {
	GinkgoHelper()

	switch networkResource.Type {
	case mapiv1.PowerVSResourceTypeID:
		if networkResource.ID == nil {
			Fail("networkResource reference is specified as ID but it is nil")
		}

		return ibmpowervsv1.IBMPowerVSResourceReference{
			ID: networkResource.ID,
		}
	case mapiv1.PowerVSResourceTypeName:
		if networkResource.Name == nil {
			Fail("networkResource reference is specified as Name but it is nil")
		}

		return ibmpowervsv1.IBMPowerVSResourceReference{
			Name: networkResource.Name,
		}
	case mapiv1.PowerVSResourceTypeRegEx:
		if networkResource.RegEx == nil {
			Fail("networkResource reference is specified as RegEx but it is nil")
		}

		return ibmpowervsv1.IBMPowerVSResourceReference{
			RegEx: networkResource.RegEx,
		}
	default:
		Fail("networkResource reference is not specified")
	}

	return ibmpowervsv1.IBMPowerVSResourceReference{}
}

func getServiceInstance(serviceInstance mapiv1.PowerVSResource) *ibmpowervsv1.IBMPowerVSResourceReference {
	GinkgoHelper()

	switch serviceInstance.Type {
	case mapiv1.PowerVSResourceTypeID:
		return &ibmpowervsv1.IBMPowerVSResourceReference{ID: serviceInstance.ID}
	case mapiv1.PowerVSResourceTypeName:
		return &ibmpowervsv1.IBMPowerVSResourceReference{Name: serviceInstance.Name}
	case mapiv1.PowerVSResourceTypeRegEx:
		return &ibmpowervsv1.IBMPowerVSResourceReference{RegEx: serviceInstance.RegEx}
	default:
		Fail("unknown type for service instance")
	}

	return nil
}

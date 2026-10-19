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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1 "github.com/openshift/api/machine/v1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
)

const (
	nutanixMachineTemplateName = "nutanix-machine-template"
)

var _ = Describe("Cluster API Nutanix MachineSet", Ordered, func() {
	var nutanixMachineTemplate *nutanixv1.NutanixMachineTemplate
	var machineSet *clusterv1.MachineSet
	var mapiMachineSpec *mapiv1.NutanixMachineProviderConfig

	BeforeAll(func() {
		if platform != configv1.NutanixPlatformType {
			Skip("Skipping Nutanix E2E tests")
		}
		mapiMachineSpec = framework.GetMAPIProviderSpec[mapiv1.NutanixMachineProviderConfig](ctx, cl)
		createNutanixSecret(cl, mapiMachineSpec)
	})

	AfterEach(func() {
		if platform != configv1.NutanixPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping Nutanix E2E tests")
		}
		framework.DeleteMachineSets(ctx, cl, machineSet)
		framework.WaitForMachineSetsDeleted(machineSet)
		framework.DeleteObjects(ctx, cl, nutanixMachineTemplate)
	})

	It("should be able to run a machine", func() {
		nutanixMachineTemplate = createNutanixMachineTemplate(cl, mapiMachineSpec)

		machineSet = framework.CreateMachineSet(ctx, cl, framework.NewMachineSetParams(
			"nutanix-machineset",
			clusterName,
			"",
			1,
			clusterv1.ContractVersionedObjectReference{
				Kind:     "NutanixMachineTemplate",
				APIGroup: infraAPIGroup,
				Name:     nutanixMachineTemplateName,
			},
			"worker-user-data",
		))

		framework.WaitForMachineSet(ctx, cl, machineSet.Name, machineSet.Namespace, framework.WaitLong)
	})
})

func createNutanixSecret(cl client.Client, mapiProviderSpec *mapiv1.NutanixMachineProviderConfig) {
	GinkgoHelper()
	By("Creating a Nutanix credentials secret")

	Expect(mapiProviderSpec).ToNot(BeNil(), "expected MAPI ProviderSpec to not be nil")
	Expect(mapiProviderSpec.CredentialsSecret).ToNot(BeNil(), "expected MAPI ProviderSpec's CredentialsSecret to not be nil")

	mapiSecret := &corev1.Secret{}
	err := cl.Get(ctx, types.NamespacedName{
		Namespace: framework.MAPINamespace,
		Name:      mapiProviderSpec.CredentialsSecret.Name,
	}, mapiSecret)
	Expect(err).ToNot(HaveOccurred(), "should not fail getting the Nutanix credentials secret")

	nutanixSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: framework.CAPINamespace,
		},
		Data: mapiSecret.Data,
	}

	if err := cl.Create(ctx, nutanixSecret); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred(), "should not fail creating a Nutanix credentials secret")
	}
}

func createNutanixMachineTemplate(cl client.Client, mapiProviderSpec *mapiv1.NutanixMachineProviderConfig) *nutanixv1.NutanixMachineTemplate {
	GinkgoHelper()
	By("Creating Nutanix machine template")

	Expect(mapiProviderSpec).ToNot(BeNil(), "expected MAPI ProviderSpec to not be nil")
	Expect(mapiProviderSpec.Subnets).ToNot(BeEmpty(), "expected MAPI ProviderSpec's Subnets to not be empty")
	Expect(mapiProviderSpec.VCPUsPerSocket).To(BeNumerically(">", 0), "expected MAPI ProviderSpec's VCPUsPerSocket to be greater than 0")
	Expect(mapiProviderSpec.VCPUSockets).To(BeNumerically(">", 0), "expected MAPI ProviderSpec's VCPUSockets to be greater than 0")

	image := convertNutanixIdentifier(mapiProviderSpec.Image)

	nutanixMachineSpec := nutanixv1.NutanixMachineSpec{
		VCPUsPerSocket: mapiProviderSpec.VCPUsPerSocket,
		VCPUSockets:    mapiProviderSpec.VCPUSockets,
		MemorySize:     mapiProviderSpec.MemorySize,
		SystemDiskSize: mapiProviderSpec.SystemDiskSize,
		Image:          &image,
		Cluster:        convertNutanixIdentifier(mapiProviderSpec.Cluster),
		Subnets:        convertNutanixIdentifiers(mapiProviderSpec.Subnets),
		BootType:       convertNutanixBootType(mapiProviderSpec.BootType),
	}

	nutanixMachineTemplate := &nutanixv1.NutanixMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nutanixMachineTemplateName,
			Namespace: framework.CAPINamespace,
		},
		Spec: nutanixv1.NutanixMachineTemplateSpec{
			Template: nutanixv1.NutanixMachineTemplateResource{
				Spec: nutanixMachineSpec,
			},
		},
	}

	if err := cl.Create(ctx, nutanixMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred(), "should not error creating the Nutanix machine template")
	}

	return nutanixMachineTemplate
}

func convertNutanixIdentifiers(in []mapiv1.NutanixResourceIdentifier) []nutanixv1.NutanixResourceIdentifier {
	out := make([]nutanixv1.NutanixResourceIdentifier, 0, len(in))
	for _, identifier := range in {
		out = append(out, convertNutanixIdentifier(identifier))
	}

	return out
}

func convertNutanixIdentifier(in mapiv1.NutanixResourceIdentifier) nutanixv1.NutanixResourceIdentifier {
	out := nutanixv1.NutanixResourceIdentifier{
		UUID: in.UUID,
		Name: in.Name,
	}

	switch in.Type {
	case mapiv1.NutanixIdentifierUUID:
		out.Type = nutanixv1.NutanixIdentifierUUID
	case mapiv1.NutanixIdentifierName:
		out.Type = nutanixv1.NutanixIdentifierName
	}

	return out
}

func convertNutanixBootType(in mapiv1.NutanixBootType) nutanixv1.NutanixBootType {
	switch in {
	case mapiv1.NutanixLegacyBoot:
		return nutanixv1.NutanixBootTypeLegacy
	case mapiv1.NutanixUEFIBoot, mapiv1.NutanixSecureBoot:
		return nutanixv1.NutanixBootTypeUEFI
	default:
		return ""
	}
}

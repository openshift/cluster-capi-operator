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
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ptr "k8s.io/utils/ptr"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	azureMachineTemplateName        = "azure-machine-template"
	capzManagerBootstrapCredentials = "capz-manager-bootstrap-credentials"
)

var _ = Describe("[sig-cluster-lifecycle][Feature:ClusterAPI][platform:azure][Disruptive] Cluster API Azure MachineSet", Ordered, Label("Conformance"), Label("Serial"), func() {
	var azureMachineTemplate *azurev1.AzureMachineTemplate
	var machineSet *clusterv1.MachineSet
	var mapiMachineSpec *mapiv1beta1.AzureMachineProviderSpec

	BeforeAll(func() {
		InitCommonVariables()
		if platform != configv1.AzurePlatformType {
			Skip("Skipping Azure E2E tests")
		}
		mapiMachineSpec = framework.GetMAPIProviderSpec[mapiv1beta1.AzureMachineProviderSpec](ctx, cl)
	})

	AfterEach(func() {
		if platform != configv1.AzurePlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping Azure E2E tests")
		}
		framework.DeleteMachineSets(ctx, cl, machineSet)
		framework.WaitForMachineSetsDeleted(machineSet)
		framework.DeleteObjects(ctx, cl, azureMachineTemplate)
	})

	It("should be able to run a machine", func() {
		azureMachineTemplate = createAzureMachineTemplate(ctx, cl, mapiMachineSpec)

		machineSet = framework.CreateMachineSet(ctx, cl, framework.NewMachineSetParams(
			"azure-machineset",
			clusterName,
			"",
			1,
			clusterv1.ContractVersionedObjectReference{
				Kind:     "AzureMachineTemplate",
				APIGroup: infraAPIGroup,
				Name:     azureMachineTemplateName,
			},
			"worker-user-data",
		))

		framework.WaitForMachineSet(ctx, cl, machineSet.Name, machineSet.Namespace, framework.WaitLong)
	})

})

func createAzureMachineTemplate(ctx context.Context, cl client.Client, mapiProviderSpec *mapiv1beta1.AzureMachineProviderSpec) *azurev1.AzureMachineTemplate {
	GinkgoHelper()
	By("Creating Azure machine template")

	Expect(mapiProviderSpec).ToNot(BeNil(), "expected MAPI ProviderSpec to not be nil")
	Expect(mapiProviderSpec.Subnet).ToNot(BeEmpty(), "expected Subnet to not be empty")
	Expect(mapiProviderSpec.AcceleratedNetworking).ToNot(BeNil(), "expected AcceleratedNetworking to not be nil")
	Expect(mapiProviderSpec.OSDisk.ManagedDisk.StorageAccountType).ToNot(BeEmpty(), "expected StorageAccountType to not be empty")
	Expect(mapiProviderSpec.OSDisk.DiskSizeGB).To(BeNumerically(">", 0), "expected DiskSizeGB to be greater than 0")
	Expect(mapiProviderSpec.OSDisk.OSType).ToNot(BeEmpty(), "expected OSType to not be empty")
	Expect(mapiProviderSpec.VMSize).ToNot(BeEmpty(), "expected VMSize to not be empty")

	// Get Azure credentials secret
	credentialsSecret := corev1.Secret{}
	credentialsSecretKey := types.NamespacedName{Name: capzManagerBootstrapCredentials, Namespace: framework.CAPINamespace}
	err := cl.Get(ctx, credentialsSecretKey, &credentialsSecret)
	Expect(err).To(BeNil(), "capz-manager-bootstrap-credentials secret should exist")

	subscriptionID := credentialsSecret.Data["azure_subscription_id"]

	// Convert MAPI Image to CAPI Image
	azureImage := convertMAPIImageToCAPIImage(&mapiProviderSpec.Image, subscriptionID)

	var (
		identity               = azurev1.VMIdentityNone
		userAssignedIdentities []azurev1.UserAssignedIdentity
	)

	if mi := mapiProviderSpec.ManagedIdentity; mi != "" {
		providerID := mi
		if !strings.HasPrefix(mi, "/subscriptions/") {
			providerID = fmt.Sprintf("azure:///subscriptions/%s/resourcegroups/%s/providers/Microsoft.ManagedIdentity/userAssignedIdentities/%s", string(subscriptionID), mapiProviderSpec.ResourceGroup, mi)
		}

		userAssignedIdentities = []azurev1.UserAssignedIdentity{{ProviderID: providerID}}
		identity = azurev1.VMIdentityUserAssigned
	}

	azureMachineSpec := azurev1.AzureMachineSpec{
		Identity:               identity,
		UserAssignedIdentities: userAssignedIdentities,
		NetworkInterfaces: []azurev1.NetworkInterface{
			{
				PrivateIPConfigs:      1,
				SubnetName:            mapiProviderSpec.Subnet,
				AcceleratedNetworking: &mapiProviderSpec.AcceleratedNetworking,
			},
		},
		Image: azureImage,
		OSDisk: azurev1.OSDisk{
			DiskSizeGB: &mapiProviderSpec.OSDisk.DiskSizeGB,
			ManagedDisk: &azurev1.ManagedDiskParameters{
				StorageAccountType: mapiProviderSpec.OSDisk.ManagedDisk.StorageAccountType,
			},
			CachingType: mapiProviderSpec.OSDisk.CachingType,
			OSType:      mapiProviderSpec.OSDisk.OSType,
		},
		DisableExtensionOperations: ptr.To(true),
		SSHPublicKey:               mapiProviderSpec.SSHPublicKey,
		VMSize:                     mapiProviderSpec.VMSize,
	}

	azureMachineTemplate := &azurev1.AzureMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      azureMachineTemplateName,
			Namespace: framework.CAPINamespace,
		},
		Spec: azurev1.AzureMachineTemplateSpec{
			Template: azurev1.AzureMachineTemplateResource{
				Spec: azureMachineSpec,
			},
		},
	}

	if err := cl.Create(ctx, azureMachineTemplate); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred(), "should not fail creating Azure machine template")
	}

	return azureMachineTemplate
}

// convertMAPIImageToCAPIImage converts a MAPI Azure Image to a CAPI Azure Image.
func convertMAPIImageToCAPIImage(mapiImage *mapiv1beta1.Image, subscriptionID []byte) *azurev1.Image {
	if mapiImage.ResourceID != "" {
		// Use ResourceID with provided subscription ID
		azureImageID := fmt.Sprintf("/subscriptions/%s%s", subscriptionID, mapiImage.ResourceID)
		return &azurev1.Image{ID: &azureImageID}
	}

	if mapiImage.Publisher != "" && mapiImage.Offer != "" && mapiImage.SKU != "" && mapiImage.Version != "" {
		// Use marketplace image
		thirdPartyImage := false
		if mapiImage.Type == mapiv1beta1.AzureImageTypeMarketplaceWithPlan {
			thirdPartyImage = true
		}

		return &azurev1.Image{
			Marketplace: &azurev1.AzureMarketplaceImage{
				ImagePlan: azurev1.ImagePlan{
					Publisher: mapiImage.Publisher,
					Offer:     mapiImage.Offer,
					SKU:       mapiImage.SKU,
				},
				Version:         mapiImage.Version,
				ThirdPartyImage: thirdPartyImage,
			},
		}
	}

	// No image specified - let CAPZ use defaults
	return nil
}

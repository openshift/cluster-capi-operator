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
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	yaml "sigs.k8s.io/yaml"
)

const (
	azureMachineTemplateName        = "azure-machine-template"
	clusterSecretName               = "capz-manager-cluster-credential"
	capzManagerBootstrapCredentials = "capz-manager-bootstrap-credentials"
)

var _ = Describe("Cluster API Azure MachineSet", Ordered, func() {
	var azureMachineTemplate *azurev1.AzureMachineTemplate
	var machineSet *clusterv1beta1.MachineSet
	var mapiMachineSpec *mapiv1beta1.AzureMachineProviderSpec

	BeforeAll(func() {
		if platform != configv1.AzurePlatformType {
			Skip("Skipping Azure E2E tests")
		}
		mapiMachineSpec = getAzureMAPIProviderSpec(ctx, cl)
	})

	AfterEach(func() {
		if platform != configv1.AzurePlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping Azure E2E tests")
		}
		framework.DeleteMachineSets(ctx, cl, machineSet)
		framework.WaitForMachineSetsDeleted(cl, machineSet)
		framework.DeleteObjects(ctx, cl, azureMachineTemplate)
	})

	It("should be able to run a machine", func() {
		azureMachineTemplate = createAzureMachineTemplate(ctx, cl, mapiMachineSpec)

		machineSet = framework.CreateMachineSet(ctx, cl, framework.NewMachineSetParams(
			"azure-machineset",
			clusterName,
			"",
			1,
			corev1.ObjectReference{
				Kind:       "AzureMachineTemplate",
				APIVersion: infraAPIVersion,
				Name:       azureMachineTemplateName,
			},
			"worker-user-data",
		))

		framework.WaitForMachineSet(cl, machineSet.Name, machineSet.Namespace)
	})

})

func getAzureMAPIProviderSpec(ctx context.Context, cl client.Client) *mapiv1beta1.AzureMachineProviderSpec {
	machineSetList := &mapiv1beta1.MachineSetList{}
	Expect(cl.List(ctx, machineSetList, client.InNamespace(framework.MAPINamespace))).To(Succeed())

	Expect(machineSetList.Items).ToNot(HaveLen(0))
	machineSet := machineSetList.Items[0]
	Expect(machineSet.Spec.Template.Spec.ProviderSpec.Value).ToNot(BeNil())

	providerSpec := &mapiv1beta1.AzureMachineProviderSpec{}
	Expect(yaml.Unmarshal(machineSet.Spec.Template.Spec.ProviderSpec.Value.Raw, providerSpec)).To(Succeed())

	return providerSpec
}

func createAzureMachineTemplate(ctx context.Context, cl client.Client, mapiProviderSpec *mapiv1beta1.AzureMachineProviderSpec) *azurev1.AzureMachineTemplate {
	By("Creating Azure machine template")

	Expect(mapiProviderSpec).ToNot(BeNil())
	Expect(mapiProviderSpec.Subnet).ToNot(BeEmpty())
	Expect(mapiProviderSpec.AcceleratedNetworking).ToNot(BeNil())
	Expect(mapiProviderSpec.Image.ResourceID).ToNot(BeEmpty())
	Expect(mapiProviderSpec.OSDisk.ManagedDisk.StorageAccountType).ToNot(BeEmpty())
	Expect(mapiProviderSpec.OSDisk.DiskSizeGB).To(BeNumerically(">", 0))
	Expect(mapiProviderSpec.OSDisk.OSType).ToNot(BeEmpty())
	Expect(mapiProviderSpec.VMSize).ToNot(BeEmpty())

	azure_credentials_secret := corev1.Secret{}
	azure_credentials_secret_key := types.NamespacedName{Name: "capz-manager-bootstrap-credentials", Namespace: "openshift-cluster-api"}
	err := cl.Get(context.Background(), azure_credentials_secret_key, &azure_credentials_secret)
	Expect(err).To(BeNil(), "capz-manager-bootstrap-credentials secret should exist")
	subscriptionID := azure_credentials_secret.Data["azure_subscription_id"]
	azureImageID := fmt.Sprintf("/subscriptions/%s%s", subscriptionID, mapiProviderSpec.Image.ResourceID)

	var (
		identity               azurev1.VMIdentity = azurev1.VMIdentityNone
		userAssignedIdentities []azurev1.UserAssignedIdentity
	)

	if mi := mapiProviderSpec.ManagedIdentity; mi != "" {
		providerID := mi
		if !strings.HasPrefix(mi, "/subscriptions/") {
			providerID = fmt.Sprintf("azure:///subscriptions/%s/resourcegroups/%s/providers/Microsoft.ManagedIdentity/userAssignedIdentities/%s", subscriptionID, mapiProviderSpec.ResourceGroup, mi)
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
		Image: &azurev1.Image{
			ID: &azureImageID,
		},
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
		Expect(err).ToNot(HaveOccurred())
	}

	return azureMachineTemplate
}

package cluster

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
)

func (r *ClusterReconciler) reconcileAzureCluster(ctx context.Context, azurePlatformStatus *configv1.AzurePlatformStatus) error {
	if azurePlatformStatus == nil {
		return errors.New("AzurePlatformStatus can't be nil")
	}

	credentialsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "capz-manager-bootstrap-credentials",
			Namespace: controllers.DefaultManagedNamespace,
		},
	}

	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(credentialsSecret), credentialsSecret); err != nil {
		return fmt.Errorf("failed to get credentials secret: %w", err)
	}

	region, found := credentialsSecret.Data["azure_region"]
	if !found {
		return fmt.Errorf("azure_region not found in credentials secret")
	}

	azureCluster := &azurev1.AzureCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.clusterName,
			Namespace: controllers.DefaultManagedNamespace,
			Annotations: map[string]string{
				clusterv1.ManagedByAnnotation: "",
			},
		},
		Spec: azurev1.AzureClusterSpec{
			ResourceGroup: azurePlatformStatus.ResourceGroupName,
			NetworkSpec: azurev1.NetworkSpec{
				Vnet: azurev1.VnetSpec{
					ResourceGroup: azurePlatformStatus.NetworkResourceGroupName,
				},
			},
			AzureClusterClassSpec: azurev1.AzureClusterClassSpec{
				Location:         string(region),
				AzureEnvironment: string(azurePlatformStatus.CloudName),
			},
		},
	}

	azureClusterCopy := azureCluster.DeepCopy()
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, azureCluster, func() error {
		azureCluster.Annotations = azureClusterCopy.Annotations
		azureCluster.Spec.ResourceGroup = azureClusterCopy.Spec.ResourceGroup
		azureCluster.Spec.NetworkSpec.Vnet.ResourceGroup = azureClusterCopy.Spec.NetworkSpec.Vnet.ResourceGroup
		azureCluster.Spec.AzureClusterClassSpec.Location = azureClusterCopy.Spec.AzureClusterClassSpec.Location
		azureCluster.Spec.AzureClusterClassSpec.AzureEnvironment = azureClusterCopy.Spec.AzureClusterClassSpec.AzureEnvironment
		return nil
	}); err != nil {
		return fmt.Errorf("unable to create or patch azure cluster: %v", err)
	}

	azureCluster.Status.Ready = true
	if err := r.Status().Patch(ctx, azureCluster, client.MergeFrom(azureClusterCopy)); err != nil {
		return fmt.Errorf("unable to update azure cluster status: %v", err)
	}

	return nil
}

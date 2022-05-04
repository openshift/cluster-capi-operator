package cluster

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	azurev1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("Reconcile Azure cluster", func() {
	var r *ClusterReconciler
	var azureCluster *azurev1.AzureCluster
	var azurePlatformStatus *configv1.AzurePlatformStatus
	var credentialsSecret *corev1.Secret

	rg := "test-rg"
	networkRG := "test-network-rg"
	cloudName := configv1.AzurePublicCloud
	region := "test-region"

	BeforeEach(func() {
		credentialsSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "capz-manager-bootstrap-credentials",
				Namespace: controllers.DefaultManagedNamespace,
			},
			Data: map[string][]byte{"azure_region": []byte(region)},
		}

		Expect(cl.Create(ctx, credentialsSecret)).To(Succeed())

		r = &ClusterReconciler{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client: cl,
			},
			clusterName: "test-cluster",
		}

		azurePlatformStatus = &configv1.AzurePlatformStatus{
			ResourceGroupName:        rg,
			NetworkResourceGroupName: networkRG,
			CloudName:                cloudName,
		}

		azureCluster = &azurev1.AzureCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      r.clusterName,
				Namespace: controllers.DefaultManagedNamespace,
			},
		}
	})

	AfterEach(func() {
		Expect(cl.Get(ctx, client.ObjectKey{
			Name:      azureCluster.Name,
			Namespace: azureCluster.Namespace,
		}, azureCluster)).To(Succeed())

		Expect(azureCluster.Annotations).To(HaveKey(clusterv1.ManagedByAnnotation))
		Expect(azureCluster.Spec.ResourceGroup).To(Equal(rg))
		Expect(azureCluster.Spec.NetworkSpec.Vnet.ResourceGroup).To(Equal(networkRG))
		Expect(azureCluster.Spec.AzureClusterClassSpec.Location).To(Equal(region))
		Expect(azureCluster.Spec.AzureClusterClassSpec.AzureEnvironment).To(Equal(string(cloudName)))
		Expect(azureCluster.Status.Ready).To(BeTrue())

		Expect(test.CleanupAndWait(ctx, cl, azureCluster, credentialsSecret)).To(Succeed())
	})

	It("should create a cluster with expected spec and status", func() {
		Expect(r.reconcileAzureCluster(ctx, azurePlatformStatus)).To(Succeed())
	})

	It("should reconcile created cluster with expected spec and status", func() {
		Expect(r.reconcileAzureCluster(ctx, azurePlatformStatus)).To(Succeed())
		Expect(cl.Get(ctx, client.ObjectKey{
			Name:      azureCluster.Name,
			Namespace: azureCluster.Namespace,
		}, azureCluster)).To(Succeed())
		azureCluster.Spec.ResourceGroup = "foo"
		azureCluster.Spec.NetworkSpec.Vnet.ResourceGroup = "foo"
		azureCluster.Spec.AzureClusterClassSpec.Location = "foo"
		azureCluster.Spec.AzureClusterClassSpec.AzureEnvironment = "foo"
		Expect(cl.Update(ctx, azureCluster)).To(Succeed())
		Expect(r.reconcileAzureCluster(ctx, azurePlatformStatus)).To(Succeed())
	})
})

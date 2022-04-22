package cluster

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
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

	BeforeEach(func() {
		r = &ClusterReconciler{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client: cl,
			},
			clusterName: "test-cluster",
		}

		azurePlatformStatus = &configv1.AzurePlatformStatus{}

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
		Expect(azureCluster.Status.Ready).To(BeTrue())

		Expect(test.CleanupAndWait(ctx, cl, azureCluster)).To(Succeed())
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
		Expect(r.reconcileAzureCluster(ctx, azurePlatformStatus)).To(Succeed())
	})
})

package cluster

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("Reconcile CAPI cluster", func() {
	var r *ClusterReconciler
	var cluster *clusterv1.Cluster

	infraClusterKind := "AWSCluster"

	BeforeEach(func() {
		r = &ClusterReconciler{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client: cl,
			},
			clusterName: "test-cluster",
		}

		cluster = &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      r.clusterName,
				Namespace: controllers.DefaultManagedNamespace,
			},
		}
	})

	AfterEach(func() {
		Expect(cl.Get(ctx, client.ObjectKey{
			Name:      cluster.Name,
			Namespace: cluster.Namespace,
		}, cluster)).To(Succeed())

		Expect(cluster.Spec.InfrastructureRef.APIVersion).To(Equal(infraGVK))
		Expect(cluster.Spec.InfrastructureRef.Kind).To(Equal(infraClusterKind))
		Expect(cluster.Spec.InfrastructureRef.Name).To(Equal(r.clusterName))
		Expect(cluster.Spec.InfrastructureRef.Namespace).To(Equal(controllers.DefaultManagedNamespace))

		Expect(test.CleanupAndWait(ctx, cl, cluster)).To(Succeed())
	})

	It("should create a cluster with expected spec and status", func() {
		Expect(r.reconcileCluster(ctx, infraClusterKind)).To(Succeed())
	})

	It("should reconcile created cluster with expected spec and status", func() {
		Expect(r.reconcileCluster(ctx, infraClusterKind)).To(Succeed())
		Expect(cl.Get(ctx, client.ObjectKey{
			Name:      cluster.Name,
			Namespace: cluster.Namespace,
		}, cluster)).To(Succeed())
		Expect(r.reconcileCluster(ctx, infraClusterKind)).To(Succeed())
	})
})

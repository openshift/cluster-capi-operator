package cluster

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gcpv1 "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("Reconcile GCP cluster", func() {
	var r *ClusterReconciler
	var gcpCluster *gcpv1.GCPCluster
	var gcpPlatformStatus *configv1.GCPPlatformStatus

	region := "test-region"
	project := "test-project"

	BeforeEach(func() {
		r = &ClusterReconciler{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client: cl,
			},
			clusterName: "test-cluster",
		}

		gcpPlatformStatus = &configv1.GCPPlatformStatus{
			Region:    region,
			ProjectID: project,
		}

		gcpCluster = &gcpv1.GCPCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      r.clusterName,
				Namespace: controllers.DefaultManagedNamespace,
			},
		}
	})

	AfterEach(func() {
		Expect(cl.Get(ctx, client.ObjectKey{
			Name:      gcpCluster.Name,
			Namespace: gcpCluster.Namespace,
		}, gcpCluster)).To(Succeed())

		Expect(gcpCluster.Annotations).To(HaveKey(clusterv1.ManagedByAnnotation))
		Expect(gcpCluster.Spec.Project).To(Equal(gcpPlatformStatus.ProjectID))
		Expect(gcpCluster.Spec.Region).To(Equal(gcpPlatformStatus.Region))
		Expect(gcpCluster.Status.Ready).To(BeTrue())

		Expect(test.CleanupAndWait(ctx, cl, gcpCluster)).To(Succeed())
	})

	It("should create a cluster with expected spec and status", func() {
		Expect(r.reconcileGCPCluster(ctx, gcpPlatformStatus)).To(Succeed())
	})

	It("should reconcile created cluster with expected spec and status", func() {
		Expect(r.reconcileGCPCluster(ctx, gcpPlatformStatus)).To(Succeed())
		Expect(cl.Get(ctx, client.ObjectKey{
			Name:      gcpCluster.Name,
			Namespace: gcpCluster.Namespace,
		}, gcpCluster)).To(Succeed())
		gcpCluster.Spec.Project = "foo"
		gcpCluster.Spec.Region = "foo"
		Expect(cl.Update(ctx, gcpCluster)).To(Succeed())
		Expect(r.reconcileGCPCluster(ctx, gcpPlatformStatus)).To(Succeed())
	})
})

package cluster

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

var _ = Describe("Reconcile AWS cluster", func() {
	var r *ClusterReconciler
	var awsCluster *awsv1.AWSCluster
	var awsPlatformStatus *configv1.AWSPlatformStatus

	region := "us-east-1"

	BeforeEach(func() {
		r = &ClusterReconciler{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client: cl,
			},
			clusterName: "test-cluster",
		}

		awsPlatformStatus = &configv1.AWSPlatformStatus{
			Region: region,
		}

		awsCluster = &awsv1.AWSCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      r.clusterName,
				Namespace: controllers.DefaultManagedNamespace,
			},
		}
	})

	AfterEach(func() {
		Expect(cl.Get(ctx, client.ObjectKey{
			Name:      awsCluster.Name,
			Namespace: awsCluster.Namespace,
		}, awsCluster)).To(Succeed())

		Expect(awsCluster.Annotations).To(HaveKey(clusterv1.ManagedByAnnotation))
		Expect(awsCluster.Spec.Region).To(Equal(awsPlatformStatus.Region))
		Expect(awsCluster.Status.Ready).To(BeTrue())

		Expect(cl.Delete(ctx, awsCluster)).To(Succeed())
		Eventually(
			apierrors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(awsCluster.DeepCopy()), &awsv1.AWSCluster{})),
			timeout,
		).Should(BeTrue())
	})

	It("should create a cluster with expected spec and status", func() {
		Expect(r.reconcileAWSCluster(ctx, awsPlatformStatus)).To(Succeed())
	})

	It("should reconcile created cluster with expected spec and status", func() {
		Expect(r.reconcileAWSCluster(ctx, awsPlatformStatus)).To(Succeed())
		Expect(cl.Get(ctx, client.ObjectKey{
			Name:      awsCluster.Name,
			Namespace: awsCluster.Namespace,
		}, awsCluster)).To(Succeed())
		Expect(r.reconcileAWSCluster(ctx, awsPlatformStatus)).To(Succeed())
	})
})

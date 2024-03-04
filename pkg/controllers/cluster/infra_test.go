package cluster

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("Reconcile Infrastructure cluster", func() {
	var awsCluster *awsv1.AWSCluster

	BeforeEach(func() {
		awsCluster = &awsv1.AWSCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-name",
				Namespace: controllers.DefaultManagedNamespace,
			},
		}

		Expect(cl.Create(ctx, awsCluster)).To(Succeed())
	})

	AfterEach(func() {
		Expect(test.CleanupAndWait(ctx, cl, awsCluster)).To(Succeed())
	})

	It("set annotation and update aws cluster status", func() {
		r := &GenericInfraClusterReconciler{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client: cl,
			},
			InfraCluster: &awsv1.AWSCluster{},
		}

		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: awsCluster.Namespace,
				Name:      awsCluster.Name,
			},
		})
		Expect(err).ToNot(HaveOccurred())

		Expect(cl.Get(ctx, client.ObjectKey{
			Name:      awsCluster.Name,
			Namespace: awsCluster.Namespace,
		}, awsCluster)).To(Succeed())

		Expect(awsCluster.Annotations).To(HaveKey(clusterv1.ManagedByAnnotation))
		Expect(awsCluster.Status.Ready).To(BeTrue())
	})
})

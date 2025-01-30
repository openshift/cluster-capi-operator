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

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

var _ = Describe("Reconcile Infrastructure cluster", func() {
	var awsCluster *awsv1.AWSCluster
	var testNamespaceName string

	BeforeEach(func() {
		By("Creating the cluster-api ClusterOperator")
		capiClusterOperator := &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{
				Name: controllers.ClusterOperatorName,
			},
		}
		Expect(cl.Create(ctx, capiClusterOperator)).To(Succeed(), "should be able to create the 'cluster-api' ClusterOperator object")

		By("Creating the testing namespace")
		namespace := corev1resourcebuilder.Namespace().WithGenerateName("test-capi-infra-").Build()
		Expect(cl.Create(ctx, namespace)).To(Succeed())
		testNamespaceName = namespace.Name

		awsCluster = &awsv1.AWSCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-name",
				Namespace: testNamespaceName,
			},
		}

		Expect(cl.Create(ctx, awsCluster)).To(Succeed())
	})

	AfterEach(func() {
		testutils.CleanupResources(Default, ctx, testEnv.Config, cl, testNamespaceName,
			&configv1.ClusterOperator{}, &awsv1.AWSCluster{})
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

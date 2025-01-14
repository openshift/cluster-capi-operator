package cluster

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

var _ = Describe("Reconcile Core cluster", func() {
	var coreCluster *clusterv1.Cluster
	var capiClusterOperator *configv1.ClusterOperator
	var testNamespaceName string
	desiredOperatorReleaseVersion := "this-is-the-desired-release-version"

	BeforeEach(func() {
		By("Creating the cluster-api ClusterOperator")
		capiClusterOperator = &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{
				Name: controllers.ClusterOperatorName,
			},
		}
		Expect(cl.Create(ctx, capiClusterOperator)).To(Succeed(), "should be able to create the 'cluster-api' ClusterOperator object")

		By("Creating the testing namespace")
		namespace := corev1resourcebuilder.Namespace().WithGenerateName("test-capi-corecluster-").Build()
		Expect(cl.Create(ctx, namespace)).To(Succeed())
		testNamespaceName = namespace.Name

		By("Creating the core cluster object")
		coreCluster = &clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-name",
				Namespace: testNamespaceName,
			},
		}
		Expect(cl.Create(ctx, coreCluster)).To(Succeed())

		By("Starting the controller")
		r := &CoreClusterReconciler{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client:         cl,
				ReleaseVersion: desiredOperatorReleaseVersion,
			},
			Cluster: &clusterv1.Cluster{},
		}
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: coreCluster.Namespace,
				Name:      coreCluster.Name,
			},
		})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		testutils.CleanupResources(Default, ctx, testEnv.Config, cl, testNamespaceName, &configv1.ClusterOperator{}, &clusterv1.Cluster{})
	})

	It("should update core cluster status", func() {
		Expect(cl.Get(ctx, client.ObjectKey{
			Name:      coreCluster.Name,
			Namespace: coreCluster.Namespace,
		}, coreCluster)).To(Succeed())

		Expect(coreCluster.Status.Conditions).ToNot(BeEmpty())
		Expect(coreCluster.Status.Conditions[0].Type).To(Equal(clusterv1.ControlPlaneInitializedCondition))
		Expect(coreCluster.Status.Conditions[0].Status).To(Equal(corev1.ConditionTrue))
	})

	It("should update the ClusterOperator status to be available, upgradeable, non-progressing, non-degraded", func() {
		co := komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())
		Eventually(co).Should(
			HaveField("Status.Conditions", SatisfyAll(
				ContainElement(And(HaveField("Type", Equal(configv1.OperatorAvailable)), HaveField("Status", Equal(configv1.ConditionTrue)))),
				ContainElement(And(HaveField("Type", Equal(configv1.OperatorProgressing)), HaveField("Status", Equal(configv1.ConditionFalse)))),
				ContainElement(And(HaveField("Type", Equal(configv1.OperatorDegraded)), HaveField("Status", Equal(configv1.ConditionFalse)))),
				ContainElement(And(HaveField("Type", Equal(configv1.OperatorUpgradeable)), HaveField("Status", Equal(configv1.ConditionTrue)))),
			)),
		)
	})

	It("should update the ClusterOperator status version to the desired one", func() {
		co := komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build())
		Eventually(co).Should(
			HaveField("Status.Versions", ContainElement(SatisfyAll(
				HaveField("Name", Equal("operator")),
				HaveField("Version", Equal(desiredOperatorReleaseVersion)),
			))),
		)
	})
})

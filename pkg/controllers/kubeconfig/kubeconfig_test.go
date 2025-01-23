/*
Copyright 2024 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package kubeconfig

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"k8s.io/utils/ptr"
)

var _ = Describe("Reconcile kubeconfig secret", func() {
	var testNamespaceName string
	var r *KubeconfigController
	var mgrCancel context.CancelFunc
	var mgrDone chan struct{}

	startManager := func() (context.CancelFunc, chan struct{}) {
		mgrCtx, mgrCancel := context.WithCancel(context.Background())
		mgrDone := make(chan struct{})

		By("Setting up a manager and controller")
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: testScheme,
			Controller: config.Controller{
				SkipNameValidation: ptr.To(true),
			},
		})
		Expect(err).ToNot(HaveOccurred(), "Manager should be able to be created")

		r = &KubeconfigController{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client:           cl,
				ManagedNamespace: testNamespaceName,
			},
			clusterName: "test-cluster",
			RestCfg:     cfg,
		}

		Expect(r.SetupWithManager(mgr)).To(Succeed(), "Reconciler should be able to setup with manager")

		By("Starting the manager")
		go func() {
			defer GinkgoRecover()
			defer close(mgrDone)

			Expect((mgr).Start(mgrCtx)).To(Succeed())
		}()

		return mgrCancel, mgrDone
	}

	stopManager := func() {
		By("Stopping the manager")
		mgrCancel()
		// Wait for the mgrDone to be closed, which will happen once the mgr has stopped.
		<-mgrDone

		Eventually(mgrDone).Should(BeClosed())
	}

	BeforeEach(func() {
		By("Creating the testing namespace")
		namespace := corev1resourcebuilder.Namespace().WithGenerateName("test-capi-corecluster-").Build()
		Expect(cl.Create(ctx, namespace)).To(Succeed())
		testNamespaceName = namespace.Name

		By("Creating the testing ClusterOperator object")
		cO := configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build()
		Expect(cl.Create(ctx, cO)).To(Succeed())

		By("Creating the testing infrastructure object")
		baseInfra := configv1resourcebuilder.Infrastructure().WithName(controllers.InfrastructureResourceName).
			AsAWS("test-cluster", "us-east-1").Build()
		Expect(cl.Create(ctx, baseInfra)).To(Succeed())

		infra := baseInfra.DeepCopy()
		infra.Status = configv1.InfrastructureStatus{
			APIServerInternalURL: "https://test:8081",
			InfrastructureName:   "test-cluster",
			Platform:             configv1.AWSPlatformType,
			PlatformStatus: &configv1.PlatformStatus{
				Type: configv1.AWSPlatformType,
			},
		}
		Expect(cl.Status().Patch(ctx, infra, client.MergeFrom(baseInfra))).To(Succeed())
	})

	AfterEach(func() {
		By("Cleaning up the testing resources")
		testutils.CleanupResources(Default, ctx, testEnv.Config, cl, testNamespaceName,
			&corev1.Secret{}, &configv1.Infrastructure{}, &configv1.ClusterOperator{})
	})

	JustBeforeEach(func() {
		mgrCancel, mgrDone = startManager()
	})

	JustAfterEach(func() {
		stopManager()
	})

	Context("create or update kubeconfig secret", func() {
		It("should create a kubeconfig secret when it doesn't exist", func() {
			By("Creating the secret token object")
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tokenSecretName,
					Namespace: testNamespaceName,
				},
				Data: map[string][]byte{
					"token":  []byte("dGVzdA=="),
					"ca.crt": []byte("dGVzdA=="),
				},
			}
			Expect(cl.Create(ctx, tokenSecret)).To(Succeed(), "should succeed creating the token secret")

			By("Checking the kubeconfig secret has been created")
			kubeconfigSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-kubeconfig", r.clusterName),
					Namespace: testNamespaceName,
				},
			}
			Eventually(komega.Get(kubeconfigSecret), timeout).Should(Succeed(), "should succeed getting the token secret")
			Expect(kubeconfigSecret.Data).To(HaveKey("value")) // kubeconfig content is tested separately

			By("Checking it updated the ClusterOperator status conditions with controller specific ones to reflect a normal state")
			Eventually(komega.Object(configv1resourcebuilder.ClusterOperator().WithName(controllers.ClusterOperatorName).Build()), timeout).
				Should(
					HaveField("Status.Conditions", SatisfyAll(
						ContainElement(And(
							HaveField("Type", BeEquivalentTo(operatorstatus.KubeconfigControllerAvailableCondition)),
							HaveField("Status", BeEquivalentTo(configv1.ConditionTrue)),
						)),
						ContainElement(And(
							HaveField("Type", BeEquivalentTo(operatorstatus.KubeconfigControllerDegradedCondition)),
							HaveField("Status", BeEquivalentTo(configv1.ConditionFalse)),
						)),
					)),
				)
		})

		It("requeue when token secret doesn't exist", func() {
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tokenSecretName,
					Namespace: testNamespaceName,
				},
			}
			Consistently(komega.Get(tokenSecret)).Should(MatchError("secrets \"cluster-capi-operator-secret\" not found"),
				"should error as it is not able to find token secret")
		})

		It("should delete token secret if its old and requeue", func() {
			By("Updating the token secret to be an old one")
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:              tokenSecretName,
					Namespace:         testNamespaceName,
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
				},
			}

			// Using a fake client and manually reconciling,
			// because it's not possible to update creation timestamp in envtest,
			// see: https://github.com/kubernetes-sigs/controller-runtime/issues/2019.
			fakeClient := fake.NewClientBuilder().WithScheme(testEnv.Scheme).WithRuntimeObjects(tokenSecret).Build()
			r.Client = fakeClient
			Expect(r.Update(ctx, tokenSecret)).To(Succeed(), "should succeed updating the token secret")

			res, err := r.reconcileKubeconfig(ctx, ctrl.LoggerFrom(ctx).WithName("KubeconfigController"))
			Expect(err).To(Not(HaveOccurred()))
			Expect(res.RequeueAfter).To(Equal(1*time.Minute), "reconciler should set a requeue after")

			Eventually(func() error {
				return fakeClient.Get(ctx, client.ObjectKeyFromObject(tokenSecret), tokenSecret)
			}, timeout).Should(Not(Succeed()), "token secret should not immediately get recreated")
		})
	})
})

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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("Reconcile kubeconfig secret", func() {
	Context("create or update kubeconfig secret", func() {
		var r *KubeconfigReconciler
		var tokenSecret *corev1.Secret
		kubeconfigSecret := &corev1.Secret{}
		log := ctrl.LoggerFrom(ctx).WithName("KubeconfigController")

		BeforeEach(func() {
			r = &KubeconfigReconciler{
				ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
					Client: cl,
				},
				clusterName: "test-cluster",
				RestCfg:     cfg,
			}

			tokenSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tokenSecretName,
					Namespace: controllers.DefaultCAPINamespace,
				},
				Data: map[string][]byte{
					"token":  []byte("dGVzdA=="),
					"ca.crt": []byte("dGVzdA=="),
				},
			}

			Expect(cl.Create(ctx, tokenSecret)).To(Succeed())
		})

		AfterEach(func() {
			Expect(test.CleanupAndWait(ctx, cl, tokenSecret, kubeconfigSecret)).To(Succeed())
		})

		It("should create a kubeconfig secret when it doesn't exist", func() {
			_, err := r.reconcileKubeconfig(ctx, log)
			Expect(err).To(Succeed())

			Expect(cl.Get(ctx, client.ObjectKey{
				Name:      fmt.Sprintf("%s-kubeconfig", r.clusterName),
				Namespace: controllers.DefaultCAPINamespace,
			}, kubeconfigSecret)).To(Succeed())
			Expect(kubeconfigSecret.Data).To(HaveKey("value")) // kubeconfig content is tested separately
		})

		It("should reconcile existing kubeconfig secret when it doesn't exist", func() {
			_, err := r.reconcileKubeconfig(ctx, log)
			Expect(err).To(Succeed())
			_, err = r.reconcileKubeconfig(ctx, log)
			Expect(err).To(Succeed())

			Expect(cl.Get(ctx, client.ObjectKey{
				Name:      fmt.Sprintf("%s-kubeconfig", r.clusterName),
				Namespace: controllers.DefaultCAPINamespace,
			}, kubeconfigSecret)).To(Succeed())
			Expect(kubeconfigSecret.Data).To(HaveKey("value")) // kubeconfig content is tested separately
		})

		It("requeue when token secret doesn't exist", func() {
			Expect(cl.Delete(ctx, tokenSecret)).To(Succeed())
			Eventually(func() error {
				return cl.Get(ctx, client.ObjectKeyFromObject(tokenSecret), tokenSecret)
			}, timeout).Should(Not(Succeed()))

			res, err := r.reconcileKubeconfig(ctx, log)
			Expect(err).To(Succeed())
			Expect(res.RequeueAfter).To(Equal(1 * time.Minute))
		})

		It("should clear token secret data if its old and requeue", func() {
			// Use fake client because it's not possible to update creation timestamp in envtest
			fakeClient := fake.NewClientBuilder().WithScheme(testEnv.Scheme).WithRuntimeObjects(tokenSecret).Build()
			r.Client = fakeClient
			tokenSecret.SetCreationTimestamp(metav1.Time{Time: time.Now().Add(-1 * time.Hour)})
			Expect(fakeClient.Update(ctx, tokenSecret)).To(Succeed())
			res, err := r.reconcileKubeconfig(ctx, log)
			Expect(err).To(Succeed())
			Expect(res).To(Equal(ctrl.Result{}))

			// Verify the secret still exists but data was cleared
			updatedSecret := &corev1.Secret{}
			Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(tokenSecret), updatedSecret)).To(Succeed())
			Expect(updatedSecret.Data).To(BeNil())

			// Verify the refresh annotation was set
			Expect(updatedSecret.Annotations).To(HaveKey(tokenRefreshAnnotationKey))
		})
	})
})

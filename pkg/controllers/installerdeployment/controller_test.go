/*
Copyright 2026 Red Hat, Inc.

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

package installerdeployment

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	testTimeout  = 10 * time.Second
	testInterval = 100 * time.Millisecond
)

var _ = Describe("InstallerDeployment Controller", func() {
	var (
		ctx        context.Context
		reconciler *InstallerDeploymentReconciler
		configMap  *corev1.ConfigMap
		clusterAPI *operatorv1alpha1.ClusterAPI
		k          komega.Komega
		namespace  string
	)

	BeforeEach(func() {
		ctx = context.Background()
		k = komega.New(cl).WithContext(ctx)

		// Create a unique test namespace.
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-installer-",
			},
		}
		Expect(cl.Create(ctx, ns)).To(Succeed())

		namespace = ns.Name

		// Create the InstallerDeploymentReconciler.
		reconciler = &InstallerDeploymentReconciler{
			Client:         cl,
			Namespace:      namespace,
			ContainerImage: "quay.io/openshift/cluster-capi-operator:test",
		}

		// Create ConfigMap with provider image refs.
		configMap = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      providerimages.ConfigMapName,
				Namespace: namespace,
			},
			Data: map[string]string{
				"aws-cluster-api-controllers":  "registry/aws@sha256:abc",
				"core-cluster-api-controllers": "registry/core@sha256:def",
			},
		}
		Expect(cl.Create(ctx, configMap)).To(Succeed())

		// Create ClusterAPI singleton.
		clusterAPI = &operatorv1alpha1.ClusterAPI{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterAPIName,
			},
			Spec: &operatorv1alpha1.ClusterAPISpec{},
		}
		Expect(cl.Create(ctx, clusterAPI)).To(Succeed())

		DeferCleanup(func() {
			testutils.CleanupResources(Default, ctx, cfg, cl, namespace,
				&corev1.ConfigMap{},
				&appsv1.Deployment{},
			)
			Expect(cl.Delete(ctx, clusterAPI)).To(Succeed())
		})
	})

	It("should create a Deployment with image volumes for ConfigMap refs", func() {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{})
		Expect(err).NotTo(HaveOccurred())

		// Verify deployment was created with correct number of image volumes
		// (2 from ConfigMap + 1 metrics-cert from the embedded base).
		Eventually(k.Object(&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploymentName,
				Namespace: namespace,
			},
		})).WithTimeout(testTimeout).WithPolling(testInterval).Should(HaveField("Spec.Template.Spec.Volumes", HaveLen(3)))

		deployment := &appsv1.Deployment{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: deploymentName, Namespace: namespace}, deployment)).To(Succeed())

		var imageRefs []string

		for _, vol := range deployment.Spec.Template.Spec.Volumes {
			if vol.Image != nil {
				imageRefs = append(imageRefs, vol.Image.Reference)
			}
		}

		Expect(imageRefs).To(ConsistOf("registry/aws@sha256:abc", "registry/core@sha256:def"))
	})

	It("should update Deployment when ConfigMap is updated", func() {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{})
		Expect(err).NotTo(HaveOccurred())

		deployment := &appsv1.Deployment{}

		Eventually(func() error {
			return cl.Get(ctx, client.ObjectKey{Name: deploymentName, Namespace: namespace}, deployment)
		}).WithTimeout(testTimeout).WithPolling(testInterval).Should(Succeed())

		Eventually(k.Update(configMap, func() {
			configMap.Data["gcp-cluster-api-controllers"] = "registry/gcp@sha256:123"
		})).Should(Succeed())

		_, err = reconciler.Reconcile(ctx, reconcile.Request{})
		Expect(err).NotTo(HaveOccurred())

		// Verify deployment has 4 volumes now (3 image + 1 metrics-cert).
		Eventually(k.Object(&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploymentName,
				Namespace: namespace,
			},
		})).WithTimeout(testTimeout).WithPolling(testInterval).Should(HaveField("Spec.Template.Spec.Volumes", HaveLen(4)))

		Expect(cl.Get(ctx, client.ObjectKey{Name: deploymentName, Namespace: namespace}, deployment)).To(Succeed())

		var imageRefs []string

		for _, vol := range deployment.Spec.Template.Spec.Volumes {
			if vol.Image != nil {
				imageRefs = append(imageRefs, vol.Image.Reference)
			}
		}

		Expect(imageRefs).To(ContainElement("registry/gcp@sha256:123"))
	})

	It("should include old revision images not in ConfigMap", func() {
		Eventually(k.UpdateStatus(clusterAPI, func() {
			clusterAPI.Status.Revisions = []operatorv1alpha1.ClusterAPIInstallerRevision{
				{
					Name:      "rev-1",
					Revision:  1,
					ContentID: "old-content",
					Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
						{
							Name: "old-provider",
							ClusterAPIInstallerComponentSource: operatorv1alpha1.ClusterAPIInstallerComponentSource{
								Type: operatorv1alpha1.InstallerComponentTypeImage,
								Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
									Ref:     "registry.example.com/old@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
									Profile: "default",
								},
							},
						},
					},
				},
			}
			clusterAPI.Status.DesiredRevision = "rev-1"
		})).Should(Succeed())

		_, err := reconciler.Reconcile(ctx, reconcile.Request{})
		Expect(err).NotTo(HaveOccurred())

		// Verify deployment has volumes for both ConfigMap and old revision images
		// (2 ConfigMap + 1 revision + 1 metrics-cert = 4).
		deployment := &appsv1.Deployment{}

		Eventually(func() int {
			if err := cl.Get(ctx, client.ObjectKey{Name: deploymentName, Namespace: namespace}, deployment); err != nil {
				return 0
			}

			return len(deployment.Spec.Template.Spec.Volumes)
		}).WithTimeout(testTimeout).WithPolling(testInterval).Should(BeNumerically(">=", 4))

		var imageRefs []string

		for _, vol := range deployment.Spec.Template.Spec.Volumes {
			if vol.Image != nil {
				imageRefs = append(imageRefs, vol.Image.Reference)
			}
		}

		Expect(imageRefs).To(ContainElement("registry.example.com/old@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	})

	Context("when platform is supported", func() {
		It("should not error when reconciling with unchanged inputs", func() {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{})
			Expect(err).NotTo(HaveOccurred())

			deployment := &appsv1.Deployment{}

			Eventually(func() error {
				return cl.Get(ctx, client.ObjectKey{Name: deploymentName, Namespace: namespace}, deployment)
			}).WithTimeout(testTimeout).WithPolling(testInterval).Should(Succeed())

			_, err = reconciler.Reconcile(ctx, reconcile.Request{})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

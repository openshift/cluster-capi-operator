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

package installer

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// markDeploymentAvailable patches the Deployment's status to Available=True so the
// installer controller considers its probe satisfied and marks the revision complete.
// In envtest no pods actually run, so the probe would never succeed without this.
// Uses Eventually to retry on resource-version conflicts from concurrent boxcutter applies.
func markDeploymentAvailable(ctx context.Context) {
	GinkgoHelper()

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: proxyTestDeploymentName, Namespace: "default"},
	}

	By("Waiting for the proxy Deployment to exist")
	Eventually(kWithCtx(ctx).Get(deploy)).WithContext(ctx).WithTimeout(defaultEventuallyTimeout).Should(Succeed())

	By("Marking the proxy Deployment as Available")
	Eventually(kWithCtx(ctx).UpdateStatus(deploy, func() {
		deploy.Status.Conditions = []appsv1.DeploymentCondition{{
			Type:   appsv1.DeploymentAvailable,
			Status: corev1.ConditionTrue,
		}}
	})).WithContext(ctx).WithTimeout(defaultEventuallyTimeout).Should(Succeed())
}

// addProxyRevisionAndWaitForSuccess adds a revision for the given proxy provider
// profile, marks the Deployment available so the probe passes, and waits for the
// installer controller to report the revision as applied.
func addProxyRevisionAndWaitForSuccess(ctx context.Context, providerName string) {
	GinkgoHelper()

	By("Adding a revision with proxy deployment provider: "+providerName, func() {
		revision := addRevision(ctx, providerName)
		markDeploymentAvailable(ctx)
		waitForRevision(ctx, revision.Name)
	})
}

// createTestProxy creates the cluster-wide Proxy singleton with the given HTTP proxy
// URL and registers a DeferCleanup to delete it at the end of the test.
func createTestProxy(ctx context.Context, httpProxy string) *configv1.Proxy {
	GinkgoHelper()

	proxy := &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
	}
	Expect(cl.Create(ctx, proxy)).To(Succeed())

	proxy.Status = configv1.ProxyStatus{
		HTTPProxy:  httpProxy,
		HTTPSProxy: httpProxy,
		NoProxy:    "localhost,127.0.0.1,::1",
	}
	Expect(cl.Status().Update(ctx, proxy)).To(Succeed())

	DeferCleanup(func(ctx context.Context) {
		Expect(client.IgnoreNotFound(cl.Delete(ctx, proxy))).To(Succeed())
	})

	return proxy
}

// getDeployment returns the proxy test Deployment from the cluster.
func getDeployment(ctx context.Context) (*appsv1.Deployment, error) {
	deploy := &appsv1.Deployment{}
	err := cl.Get(ctx, client.ObjectKey{Name: proxyTestDeploymentName, Namespace: "default"}, deploy)

	return deploy, err
}

// haveProxyEnvVars is a matcher that checks a container has the three proxy env vars.
func haveProxyEnvVars() OmegaMatcher {
	return HaveField("Env", ContainElements(
		HaveField("Name", "HTTP_PROXY"),
		HaveField("Name", "HTTPS_PROXY"),
		HaveField("Name", "NO_PROXY"),
	))
}

// notHaveProxyEnvVars is a matcher that checks a container has no proxy env vars.
func notHaveProxyEnvVars() OmegaMatcher {
	return HaveField("Env", And(
		Not(ContainElement(HaveField("Name", "HTTP_PROXY"))),
		Not(ContainElement(HaveField("Name", "HTTPS_PROXY"))),
		Not(ContainElement(HaveField("Name", "NO_PROXY"))),
	))
}

// haveContainer returns a matcher for a named container within
// Spec.Template.Spec.Containers.
func haveContainer(name string, containerMatcher OmegaMatcher) OmegaMatcher {
	return HaveField("Spec.Template.Spec.Containers", ContainElement(
		SatisfyAll(HaveField("Name", name), containerMatcher),
	))
}

// proxyEventuallyTimeout is longer than defaultEventuallyTimeout because
// the proxy controller's SSA apply adds a reconciliation round-trip after
// the installer controller applies the managed object.
const proxyEventuallyTimeout = 10 * time.Second

// waitForContainer waits until the Deployment satisfies the given container matcher.
func waitForContainer(ctx context.Context, name string, matcher OmegaMatcher) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		deploy, err := getDeployment(ctx)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(deploy).To(haveContainer(name, matcher))
	}).WithContext(ctx).WithTimeout(proxyEventuallyTimeout).Should(Succeed())
}

var _ = Describe("ProxyController", Serial, func() {
	BeforeEach(func(ctx context.Context) {
		createFixtures(ctx)
	}, defaultNodeTimeout)

	AfterEach(func(ctx context.Context) {
		// Reconcile an empty revision to tear down all managed objects.
		emptyRevision := addEmptyRevision(ctx)
		waitForRevision(ctx, emptyRevision.Name)
	}, defaultNodeTimeout)

	Context("with a Deployment carrying the inject-proxy annotation", func() {
		BeforeEach(func(ctx context.Context) {
			addProxyRevisionAndWaitForSuccess(ctx, providerProxyAnnotated)
		}, defaultNodeTimeout)

		It("injects HTTP_PROXY/HTTPS_PROXY/NO_PROXY into the annotated container when proxy is configured", func(ctx context.Context) {
			By("Creating the cluster-wide Proxy")
			createTestProxy(ctx, "http://proxy.example.com:3128")

			By("Waiting for the proxy env vars to appear in the manager container")
			waitForContainer(ctx, "manager", haveProxyEnvVars())

			By("Verifying the unannotated container is unaffected")

			deploy, err := getDeployment(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(deploy).To(haveContainer("other", notHaveProxyEnvVars()))
		}, defaultNodeTimeout)

		It("clears proxy env vars when the Proxy CR is deleted", func(ctx context.Context) {
			By("Creating the cluster-wide Proxy")

			proxy := createTestProxy(ctx, "http://proxy.example.com:3128")

			By("Waiting for env vars to be injected")
			waitForContainer(ctx, "manager", haveProxyEnvVars())

			By("Deleting the Proxy CR")
			Expect(cl.Delete(ctx, proxy)).To(Succeed())

			By("Waiting for env vars to be cleared from the manager container")
			waitForContainer(ctx, "manager", notHaveProxyEnvVars())
		}, defaultNodeTimeout)

		It("clears proxy env vars when proxy is reconfigured to empty values", func(ctx context.Context) {
			By("Creating the cluster-wide Proxy")

			proxy := createTestProxy(ctx, "http://proxy2.example.com:3128")

			By("Waiting for env vars to be injected")
			waitForContainer(ctx, "manager", haveProxyEnvVars())

			By("Clearing the proxy status values")
			Eventually(kWithCtx(ctx).UpdateStatus(proxy, func() {
				proxy.Status = configv1.ProxyStatus{}
			})).WithContext(ctx).Should(Succeed())

			By("Waiting for env vars to be cleared from the manager container")
			waitForContainer(ctx, "manager", notHaveProxyEnvVars())
		}, defaultNodeTimeout)
	})

	Context("with a Deployment that has no inject-proxy annotation", func() {
		BeforeEach(func(ctx context.Context) {
			addProxyRevisionAndWaitForSuccess(ctx, providerProxyNotAnnotated)
		}, defaultNodeTimeout)

		It("does not inject proxy env vars even when proxy is configured", func(ctx context.Context) {
			By("Creating the cluster-wide Proxy")
			createTestProxy(ctx, "http://proxy.example.com:3128")

			By("Verifying no proxy env vars are added to either container")
			// Give the proxy controller time to reconcile, then confirm state stays clean.
			Consistently(func(g Gomega) {
				deploy, err := getDeployment(ctx)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(deploy).To(haveContainer("manager", notHaveProxyEnvVars()))
				g.Expect(deploy).To(haveContainer("other", notHaveProxyEnvVars()))
			}).WithContext(ctx).WithTimeout(proxyEventuallyTimeout).Should(Succeed())
		}, defaultNodeTimeout)
	})

	Context("when the inject-proxy annotation is removed via a new revision", func() {
		It("clears proxy env vars after the annotation is removed", func(ctx context.Context) {
			By("Installing a revision with the inject-proxy annotation")
			addProxyRevisionAndWaitForSuccess(ctx, providerProxyAnnotated)

			By("Creating the cluster-wide Proxy")
			createTestProxy(ctx, "http://proxy.example.com:3128")

			By("Waiting for env vars to be injected into the manager container")
			waitForContainer(ctx, "manager", haveProxyEnvVars())

			By("Installing a new revision that removes the inject-proxy annotation")
			addProxyRevisionAndWaitForSuccess(ctx, providerProxyNotAnnotated)

			By("Waiting for env vars to be cleared after annotation removal")
			waitForContainer(ctx, "manager", notHaveProxyEnvVars())

			By("Verifying the other container also has no proxy env vars")
			waitForContainer(ctx, "other", notHaveProxyEnvVars())
		}, defaultNodeTimeout)
	})

	Context("SSA field ownership correctness", func() {
		It("does not affect env vars set by other field managers on the annotated container", func(ctx context.Context) {
			By("Installing a revision with the inject-proxy annotation")
			addProxyRevisionAndWaitForSuccess(ctx, providerProxyAnnotated)

			By("Adding a custom env var to the manager container via a separate field manager")

			deploy, err := getDeployment(ctx)
			Expect(err).NotTo(HaveOccurred())

			patch := deploy.DeepCopy()
			patch.Spec.Template.Spec.Containers[0].Env = append(
				patch.Spec.Template.Spec.Containers[0].Env,
				corev1.EnvVar{Name: "CUSTOM_VAR", Value: "custom-value"},
			)
			Expect(cl.Update(ctx, patch)).To(Succeed())

			By("Creating the cluster-wide Proxy")
			createTestProxy(ctx, "http://proxy.example.com:3128")

			By("Waiting for proxy env vars to be injected")
			waitForContainer(ctx, "manager", haveProxyEnvVars())

			By("Verifying the custom env var is still present alongside the proxy vars")

			deploy, err = getDeployment(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(deploy).To(haveContainer("manager", HaveField("Env", ContainElement(
				HaveField("Name", "CUSTOM_VAR"),
			))))
		}, defaultNodeTimeout)

		It("does not remove proxy vars set by other field managers on unannotated containers", func(ctx context.Context) {
			By("Installing a revision with the inject-proxy annotation targeting only manager")
			addProxyRevisionAndWaitForSuccess(ctx, providerProxyAnnotated)

			By("Setting HTTP_PROXY on the other container via a separate field manager")

			deploy, err := getDeployment(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Find the "other" container (index 1) and add an HTTP_PROXY env var.
			// This simulates a separate controller managing proxy vars on that container.
			patch := deploy.DeepCopy()
			for i := range patch.Spec.Template.Spec.Containers {
				if patch.Spec.Template.Spec.Containers[i].Name == "other" {
					patch.Spec.Template.Spec.Containers[i].Env = append(
						patch.Spec.Template.Spec.Containers[i].Env,
						corev1.EnvVar{Name: "HTTP_PROXY", Value: "http://other-proxy.example.com:9999"},
					)

					break
				}
			}

			Expect(cl.Update(ctx, patch)).To(Succeed())

			By("Creating the cluster-wide Proxy")
			createTestProxy(ctx, "http://proxy.example.com:3128")

			By("Waiting for proxy env vars to be injected into manager")
			waitForContainer(ctx, "manager", haveProxyEnvVars())

			By("Verifying the other container still has its externally-set HTTP_PROXY")

			deploy, err = getDeployment(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(deploy).To(haveContainer("other", HaveField("Env", ContainElement(
				SatisfyAll(
					HaveField("Name", "HTTP_PROXY"),
					HaveField("Value", "http://other-proxy.example.com:9999"),
				),
			))))
		}, defaultNodeTimeout)
	})
})

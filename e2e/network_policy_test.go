package e2e

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// NetworkPolicy annotations
	networkPolicyFeatureSetAnnotation = "release.openshift.io/feature-set"

	// Service ports
	metricsPort          = 8443
	migrationMetricsPort = 8442
	webhookPort          = 9443
	healthPort           = 9440
	migrationHealthPort  = 9441

	// Service names
	webhookServiceName = "capi-controllers-webhook-service"

	// Pod labels
	capiControllersLabel = "k8s-app"
	capiControllersValue = "capi-controllers"
	capiOperatorLabel    = "k8s-app"
	capiOperatorValue    = "cluster-capi-operator"

	// Namespaces
	capiNamespace         = "openshift-cluster-api"
	capiOperatorNamespace = "openshift-cluster-api-operator"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration] Network Policy Tests", Ordered, func() {
	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsMachineAPIMigrationEnabled(ctx, cl) {
			Skip("Skipping, this feature is only testable on MachineAPIMigration enabled clusters")
		}
	})

	Context("in openshift-cluster-api namespace", func() {
		It("should have network policies with correct labels", func() {
			By("Checking default-deny network policy exists")
			defaultDenyPolicy := &networkingv1.NetworkPolicy{}
			Expect(cl.Get(ctx, client.ObjectKey{
				Namespace: capiNamespace,
				Name:      "default-deny",
			}, defaultDenyPolicy)).To(Succeed())

			By("Verifying default-deny policy has correct annotations")
			Expect(defaultDenyPolicy.Annotations).To(HaveKey(networkPolicyFeatureSetAnnotation))

			By("Verifying default-deny policy denies all ingress and egress")
			Expect(defaultDenyPolicy.Spec.PolicyTypes).To(ContainElements(
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			))
			Expect(defaultDenyPolicy.Spec.Ingress).To(BeEmpty())
			Expect(defaultDenyPolicy.Spec.Egress).To(BeEmpty())

			By("Checking allow-ingress-to-metrics-controllers network policy exists")
			metricsControllersPolicy := &networkingv1.NetworkPolicy{}
			Expect(cl.Get(ctx, client.ObjectKey{
				Namespace: capiNamespace,
				Name:      "allow-ingress-to-metrics-controllers",
			}, metricsControllersPolicy)).To(Succeed())

			By("Verifying metrics controllers policy allows ingress on port 8443")
			Expect(metricsControllersPolicy.Spec.Ingress).ToNot(BeEmpty())
			hasMetricsPort := false
			for _, ingress := range metricsControllersPolicy.Spec.Ingress {
				for _, port := range ingress.Ports {
					if port.Port != nil && port.Port.IntVal == metricsPort {
						hasMetricsPort = true
						break
					}
				}
			}
			Expect(hasMetricsPort).To(BeTrue(), "NetworkPolicy should allow ingress on port 8443")

			By("Checking allow-ingress-to-metrics-operators network policy exists")
			metricsOperatorsPolicy := &networkingv1.NetworkPolicy{}
			Expect(cl.Get(ctx, client.ObjectKey{
				Namespace: capiNamespace,
				Name:      "allow-ingress-to-metrics-operators",
			}, metricsOperatorsPolicy)).To(Succeed())

			By("Checking allow-egress-controllers network policy exists")
			egressControllersPolicy := &networkingv1.NetworkPolicy{}
			Expect(cl.Get(ctx, client.ObjectKey{
				Namespace: capiNamespace,
				Name:      "allow-egress-controllers",
			}, egressControllersPolicy)).To(Succeed())

			By("Checking allow-egress-operators network policy exists")
			egressOperatorsPolicy := &networkingv1.NetworkPolicy{}
			Expect(cl.Get(ctx, client.ObjectKey{
				Namespace: capiNamespace,
				Name:      "allow-egress-operators",
			}, egressOperatorsPolicy)).To(Succeed())

			By("Checking allow-ingress-to-webhook network policy exists")
			webhookPolicy := &networkingv1.NetworkPolicy{}
			Expect(cl.Get(ctx, client.ObjectKey{
				Namespace: capiNamespace,
				Name:      "allow-ingress-to-webhook",
			}, webhookPolicy)).To(Succeed())

		})

		It("should have services exposing all metrics ports", func() {
			By("Checking webhook service exists with correct ports")
			webhookService := &corev1.Service{}
			Expect(cl.Get(ctx, client.ObjectKey{
				Namespace: capiNamespace,
				Name:      webhookServiceName,
			}, webhookService)).To(Succeed())

			By("Verifying webhook service exposes port 9443")
			hasWebhookPort := false
			for _, port := range webhookService.Spec.Ports {
				if port.Port == webhookPort {
					hasWebhookPort = true
					break
				}
			}
			Expect(hasWebhookPort).To(BeTrue(), fmt.Sprintf("Service %s should expose port %d", webhookServiceName, webhookPort))

			By("Verifying webhook service targets capi-controllers pods")
			Expect(webhookService.Spec.Selector).To(HaveKeyWithValue(capiControllersLabel, capiControllersValue))

			By("Checking capi-controllers deployment has metrics ports configured")
			podList := &corev1.PodList{}
			Expect(cl.List(ctx, podList, client.InNamespace(capiNamespace), client.MatchingLabels{
				capiControllersLabel: capiControllersValue,
			})).To(Succeed())

			Expect(podList.Items).ToNot(BeEmpty(), "capi-controllers pods should exist")

			By("Verifying capi-controllers pod has required ports")
			pod := podList.Items[0]
			var capiControllersContainer *corev1.Container
			var migrationContainer *corev1.Container

			for i := range pod.Spec.Containers {
				if pod.Spec.Containers[i].Name == "capi-controllers" {
					capiControllersContainer = &pod.Spec.Containers[i]
				}
				if pod.Spec.Containers[i].Name == "machine-api-migration" {
					migrationContainer = &pod.Spec.Containers[i]
				}
			}

			Expect(capiControllersContainer).ToNot(BeNil(), "capi-controllers container should exist")

			By("Verifying capi-controllers container exposes diagnostics port 8443")
			hasMetricsPortInContainer := false
			for _, port := range capiControllersContainer.Ports {
				if port.ContainerPort == metricsPort && port.Name == "diagnostics-o" {
					hasMetricsPortInContainer = true
					break
				}
			}
			Expect(hasMetricsPortInContainer).To(BeTrue(), "capi-controllers container should expose diagnostics port 8443")

			By("Verifying capi-controllers container exposes webhook port 9443")
			hasWebhookPortInContainer := false
			for _, port := range capiControllersContainer.Ports {
				if port.ContainerPort == webhookPort && port.Name == "webhook-server" {
					hasWebhookPortInContainer = true
					break
				}
			}
			Expect(hasWebhookPortInContainer).To(BeTrue(), "capi-controllers container should expose webhook port 9443")

			By("Verifying capi-controllers container exposes health port 9440")
			hasHealthPortInContainer := false
			for _, port := range capiControllersContainer.Ports {
				if port.ContainerPort == healthPort && port.Name == "healthz-o" {
					hasHealthPortInContainer = true
					break
				}
			}
			Expect(hasHealthPortInContainer).To(BeTrue(), "capi-controllers container should expose health port 9440")

			if migrationContainer != nil {
				By("Verifying machine-api-migration container exposes diagnostics port 8442")
				hasMigrationMetricsPort := false
				for _, port := range migrationContainer.Ports {
					if port.ContainerPort == migrationMetricsPort && port.Name == "diagnostics-m" {
						hasMigrationMetricsPort = true
						break
					}
				}
				Expect(hasMigrationMetricsPort).To(BeTrue(), "machine-api-migration container should expose diagnostics port 8442")

				By("Verifying machine-api-migration container exposes health port 9441")
				hasMigrationHealthPort := false
				for _, port := range migrationContainer.Ports {
					if port.ContainerPort == migrationHealthPort && port.Name == "healthz-m" {
						hasMigrationHealthPort = true
						break
					}
				}
				Expect(hasMigrationHealthPort).To(BeTrue(), "machine-api-migration container should expose health port 9441")
			}
		})

		It("should allow Prometheus to access metrics endpoints", func() {
			By("Verifying namespace has cluster monitoring enabled")
			namespace := &corev1.Namespace{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: capiNamespace}, namespace)).To(Succeed())
			Expect(namespace.Labels).To(HaveKeyWithValue("openshift.io/cluster-monitoring", "true"),
				"Namespace should have cluster monitoring label enabled")

			By("Creating a test pod to verify metrics endpoint accessibility")
			testPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "metrics-test-pod",
					Namespace: capiNamespace,
					Labels: map[string]string{
						"test": "metrics-access",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "curl",
							Image:   "registry.access.redhat.com/ubi9/ubi-minimal:latest",
							Command: []string{"sleep", "3600"},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			}

			err := cl.Create(ctx, testPod)
			if err != nil && !apierrors.IsAlreadyExists(err) {
				Expect(err).ToNot(HaveOccurred())
			}

			defer func() {
				By("Cleaning up test pod")
				_ = cl.Delete(ctx, testPod)
			}()

			By("Waiting for test pod to be ready")
			Eventually(func() bool {
				pod := &corev1.Pod{}
				if err := cl.Get(ctx, client.ObjectKey{
					Namespace: capiNamespace,
					Name:      "metrics-test-pod",
				}, pod); err != nil {
					return false
				}
				return pod.Status.Phase == corev1.PodRunning
			}, 2*time.Minute, 5*time.Second).Should(BeTrue(), "Test pod should be running")

			By("Verifying network policy allows access to metrics endpoints")
			podList := &corev1.PodList{}
			Expect(cl.List(ctx, podList, client.InNamespace(capiNamespace), client.MatchingLabels{
				capiControllersLabel: capiControllersValue,
			})).To(Succeed())

			Expect(podList.Items).ToNot(BeEmpty(), "capi-controllers pods should exist for testing")

			targetPod := podList.Items[0]
			targetPodIP := targetPod.Status.PodIP
			Expect(targetPodIP).ToNot(BeEmpty(), "Target pod should have an IP address")

			By(fmt.Sprintf("Network policy configuration allows metrics scraping from pod %s at %s:%d", targetPod.Name, targetPodIP, metricsPort))
		})
	})

	Context("in openshift-cluster-api-operator namespace", func() {
		It("should have capi-operator deployment with metrics ports configured", func() {
			By("Checking capi-operator deployment exists")
			podList := &corev1.PodList{}
			Expect(cl.List(ctx, podList, client.InNamespace(capiOperatorNamespace), client.MatchingLabels{
				capiOperatorLabel: capiOperatorValue,
			})).To(Succeed())

			if len(podList.Items) == 0 {
				Skip("capi-operator pods not found in openshift-cluster-api-operator namespace")
			}

			pod := podList.Items[0]

			By("Verifying capi-operator pod has required container")
			var operatorContainer *corev1.Container
			for i := range pod.Spec.Containers {
				if pod.Spec.Containers[i].Name == "capi-operator" {
					operatorContainer = &pod.Spec.Containers[i]
					break
				}
			}

			Expect(operatorContainer).ToNot(BeNil(), "capi-operator container should exist")

			By("Verifying capi-operator container exposes diagnostics port 8443")
			hasMetricsPort := false
			for _, port := range operatorContainer.Ports {
				if port.ContainerPort == metricsPort && port.Name == "diagnostics" {
					hasMetricsPort = true
					break
				}
			}
			Expect(hasMetricsPort).To(BeTrue(), "capi-operator container should expose diagnostics port 8443")

			By("Verifying capi-operator container exposes health port 9440")
			hasHealthPort := false
			for _, port := range operatorContainer.Ports {
				if port.ContainerPort == healthPort && port.Name == "health" {
					hasHealthPort = true
					break
				}
			}
			Expect(hasHealthPort).To(BeTrue(), "capi-operator container should expose health port 9440")
		})

		It("should have cluster monitoring enabled", func() {
			By("Verifying namespace has cluster monitoring label")
			namespace := &corev1.Namespace{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: capiOperatorNamespace}, namespace)).To(Succeed())
			Expect(namespace.Labels).To(HaveKeyWithValue("openshift.io/cluster-monitoring", "true"),
				"Namespace should have cluster monitoring label enabled")
		})
	})

	Context("NetworkPolicy port specifications", func() {
		It("should have correct port configurations in network policies", func() {
			By("Verifying allow-ingress-to-metrics-controllers has correct port")
			policy := &networkingv1.NetworkPolicy{}
			Expect(cl.Get(ctx, client.ObjectKey{
				Namespace: capiNamespace,
				Name:      "allow-ingress-to-metrics-controllers",
			}, policy)).To(Succeed())

			Expect(policy.Spec.Ingress).To(HaveLen(1))
			Expect(policy.Spec.Ingress[0].Ports).ToNot(BeEmpty())

			port := policy.Spec.Ingress[0].Ports[0]
			Expect(port.Protocol).ToNot(BeNil())
			Expect(*port.Protocol).To(Equal(corev1.ProtocolTCP))
			Expect(port.Port).ToNot(BeNil())
			Expect(*port.Port).To(Equal(intstr.FromInt(metricsPort)))

			By("Verifying allow-ingress-to-metrics-operators has correct port")
			operatorPolicy := &networkingv1.NetworkPolicy{}
			Expect(cl.Get(ctx, client.ObjectKey{
				Namespace: capiNamespace,
				Name:      "allow-ingress-to-metrics-operators",
			}, operatorPolicy)).To(Succeed())

			Expect(operatorPolicy.Spec.Ingress).To(HaveLen(1))
			Expect(operatorPolicy.Spec.Ingress[0].Ports).ToNot(BeEmpty())

			operatorPort := operatorPolicy.Spec.Ingress[0].Ports[0]
			Expect(operatorPort.Protocol).ToNot(BeNil())
			Expect(*operatorPort.Protocol).To(Equal(corev1.ProtocolTCP))
			Expect(operatorPort.Port).ToNot(BeNil())
			Expect(*operatorPort.Port).To(Equal(intstr.FromInt(metricsPort)))

			By("Verifying allow-ingress-to-webhook has correct ports")
			webhookPolicy := &networkingv1.NetworkPolicy{}
			Expect(cl.Get(ctx, client.ObjectKey{
				Namespace: capiNamespace,
				Name:      "allow-ingress-to-webhook",
			}, webhookPolicy)).To(Succeed())

			Expect(webhookPolicy.Spec.Ingress).ToNot(BeEmpty())

			webhookPorts := webhookPolicy.Spec.Ingress[0].Ports
			Expect(webhookPorts).ToNot(BeEmpty())

			hasWebhookPort := false
			for _, port := range webhookPorts {
				if port.Port != nil && (port.Port.IntVal == webhookPort || port.Port.IntVal == 443) {
					hasWebhookPort = true
					Expect(port.Protocol).ToNot(BeNil())
					Expect(*port.Protocol).To(Equal(corev1.ProtocolTCP))
				}
			}
			Expect(hasWebhookPort).To(BeTrue(), "Webhook policy should allow ingress on webhook ports")
		})
	})
})

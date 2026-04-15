// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"

	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/commoncmdoptions"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

type testIfFunc func(ctx context.Context) bool

// tlsEndpoint describes a TLS-secured endpoint exposed by a CAPI operator component.
type tlsEndpoint struct {
	name          string            // human-readable label, e.g. "capi-operator metrics"
	namespace     string            // pod namespace
	containerName string            // container name
	labels        map[string]string // pod label selector
	port          int               // container port to connect to
	serverName    string            // TLS ServerName for cert validation (service DNS name)
	testIf        []testIfFunc      // functions to determine if the endpoint should be tested. They must all return true.
	expectRestart bool              // whether the endpoint is expected to restart (as opposed to being recreated) when the TLS configuration is changed
}

func ifPlatform(onlyPlatform configv1.PlatformType) testIfFunc {
	return func(context.Context) bool {
		return platform == onlyPlatform
	}
}

func ifFeatureGateEnabled(featureGate configv1.FeatureGateName) testIfFunc {
	return func(ctx context.Context) bool {
		return framework.IsFeatureGateEnabled(ctx, cl, featureGate)
	}
}

var endpoints = []tlsEndpoint{
	// Core endpoints are present on all clusters where CAPI is enabled.
	{
		name:          "capi-operator metrics",
		namespace:     framework.CAPIOperatorNamespace,
		containerName: "capi-operator",
		labels:        map[string]string{"k8s-app": "capi-operator"},
		port:          8443,
		serverName:    "capi-operator-metrics.openshift-cluster-api-operator.svc",
		expectRestart: true,
	},
	{
		name:          "capi-controllers metrics",
		namespace:     framework.CAPINamespace,
		containerName: "capi-controllers",
		labels:        map[string]string{"k8s-app": "capi-controllers"},
		port:          8443,
		serverName:    "capi-controllers-metrics.openshift-cluster-api.svc",
		expectRestart: true,
	},
	{
		name:          "capi-controllers webhook",
		namespace:     framework.CAPINamespace,
		containerName: "capi-controllers",
		labels:        map[string]string{"k8s-app": "capi-controllers"},
		port:          9443,
		serverName:    "capi-controllers-webhook-service.openshift-cluster-api.svc",
		expectRestart: true,
	},
	{
		name:          "compatibility-requirements metrics",
		namespace:     framework.CompatibilityRequirementsNamespace,
		containerName: "compatibility-requirements-controllers",
		labels:        map[string]string{"k8s-app": "compatibility-requirements-controllers"},
		port:          8443,
		serverName:    "compatibility-requirements-controllers-metrics.openshift-compatibility-requirements-operator.svc",
		expectRestart: true,
	},
	{
		name:          "compatibility-requirements webhook",
		namespace:     framework.CompatibilityRequirementsNamespace,
		containerName: "compatibility-requirements-controllers",
		labels:        map[string]string{"k8s-app": "compatibility-requirements-controllers"},
		port:          9443,
		serverName:    "compatibility-requirements-controllers-webhook-service.openshift-compatibility-requirements-operator.svc",
		expectRestart: true,
	},

	// Machine API Migration-specific endpoints.
	// These endpoints are only present on clusters which support Machine API Migration.
	{
		name:          "machine-api-migration metrics",
		namespace:     framework.CAPINamespace,
		containerName: "machine-api-migration",
		labels:        map[string]string{"k8s-app": "capi-controllers"},
		port:          8442,
		serverName:    "capi-controllers-metrics.openshift-cluster-api.svc",
		testIf:        []testIfFunc{ifFeatureGateEnabled(features.FeatureGateMachineAPIMigration)},
		expectRestart: true,
	},

	/* TODO: Uncomment when CAPA TLS support is merged
	// AWS-specific endpoints.
	{
		name:          "capa-controller-manager metrics",
		namespace:     framework.CAPINamespace,
		containerName: "manager",
		labels:        map[string]string{"control-plane": "capa-controller-manager"},
		port:          8443,
		// CAPA metrics endpoint uses a self-signed cert
		testIf: []testIfFunc{ifPlatform(configv1.AWSPlatformType)},
	},
	{
		name:          "capa-controller-manager webhook",
		namespace:     framework.CAPINamespace,
		containerName: "manager",
		labels:        map[string]string{"control-plane": "capa-controller-manager"},
		port:          9443,
		serverName:    "capa-webhook-service.openshift-cluster-api.svc",
		testIf:        []testIfFunc{ifPlatform(configv1.AWSPlatformType)},
	},
	*/

	/* TODO: Uncomment when CAPO TLS support is merged
	// OpenStack-specific endpoints.
	{
		name:          "capo-controller-manager metrics",
		namespace:     framework.CAPINamespace,
		containerName: "manager",
		labels:        map[string]string{"control-plane": "capo-controller-manager"},
		port:          8080,
		// OpenStack metrics endpoint uses a self-signed cert
		testIf: []testIfFunc{ifPlatform(configv1.OpenStackPlatformType)},
	},
	{
		name:          "capo-controller-manager webhook",
		namespace:     framework.CAPINamespace,
		containerName: "manager",
		labels:        map[string]string{"control-plane": "capo-controller-manager"},
		port:          9443,
		serverName:    "capo-webhook-service.openshift-cluster-api.svc",
		testIf:        []testIfFunc{ifPlatform(configv1.OpenStackPlatformType)},
	},
	*/
}

// tlsTestCase defines a TLS version probe and its expected outcome.
type tlsTestCase struct {
	version       uint16
	versionName   string
	shouldSucceed bool
}

var _ = FDescribe("TLS Security Profile", Ordered, func() {
	var (
		caCertPool *x509.CertPool
	)

	BeforeAll(func(ctx context.Context) {
		By("Checking that required feature gates are enabled")
		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagement) {
			Skip("ClusterAPIMachineManagement feature gate is not enabled")
		}
		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateTLSAdherence) {
			Skip("TLSAdherence feature gate is not enabled")
		}

		By("Saving original APIServer TLS configuration")
		apiServer := &configv1.APIServer{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, apiServer)).To(Succeed())

		DeferCleanup(func(ctx context.Context) {
			By("Restoring original APIServer TLS configuration")
			restoreAdherence := apiServer.Spec.TLSAdherence
			// tlsAdherence cannot be removed once set, so if the original was
			// empty (unset), restore to LegacyAdheringComponentsOnly which is
			// the behavioral equivalent.
			if restoreAdherence == configv1.TLSAdherencePolicyNoOpinion {
				restoreAdherence = configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly
			}

			previousPods := waitForReadyPods(ctx, endpoints)
			rolloutExpected := setTLSConfig(ctx, restoreAdherence, apiServer.Spec.TLSSecurityProfile)
			if rolloutExpected {
				waitForRollout(ctx, endpoints, previousPods)
			}
		}, NodeTimeout(framework.WaitMedium))

		By("Loading service-ca CA certificate")
		caCertPool = loadServiceCACert(ctx)
	}, NodeTimeout(framework.WaitShort))

	// intermediateTests defines TLS version assertions for the Intermediate profile.
	// Intermediate has MinTLSVersion=1.2, so TLS 1.0 must be rejected while 1.2 and 1.3 must be accepted.
	intermediateTests := []tlsTestCase{
		{tls.VersionTLS10, "TLS 1.0", false},
		{tls.VersionTLS12, "TLS 1.2", true},
		{tls.VersionTLS13, "TLS 1.3", true},
	}

	// modernTests defines TLS version assertions for the Modern profile.
	// Modern has MinTLSVersion=1.3, so TLS 1.0 and 1.2 must be rejected while 1.3 must be accepted.
	modernTests := []tlsTestCase{
		{tls.VersionTLS10, "TLS 1.0", false},
		{tls.VersionTLS12, "TLS 1.2", false},
		{tls.VersionTLS13, "TLS 1.3", true},
	}

	endpointTests := func(tests []tlsTestCase) {
		DescribeTable("endpoints",
			func(ctx context.Context, ep tlsEndpoint, tlsVersion uint16, shouldSucceed bool) {
				for _, testIf := range ep.testIf {
					if !testIf(ctx) {
						Skip("Skipping endpoint")
					}
				}

				assertTLSVersion(ctx, ep, tlsVersion, shouldSucceed, caCertPool)
			},
			tlsVersionEntries(endpoints, tests),
		)
	}

	Context("with LegacyAdheringComponentsOnly and Intermediate profile", Ordered, func() {
		BeforeAll(func(ctx context.Context) {
			previousPods := waitForReadyPods(ctx, endpoints)

			log := GinkgoLogr
			log.Info("Got previous pods", "previousPods", describePods(previousPods))

			By("Setting TLS configuration to LegacyAdheringComponentsOnly + Intermediate")
			rolloutExpected := setTLSConfig(ctx,
				configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly,
				&configv1.TLSSecurityProfile{
					Type:         configv1.TLSProfileIntermediateType,
					Intermediate: &configv1.IntermediateTLSProfile{},
				},
			)

			if rolloutExpected {
				waitForRollout(ctx, endpoints, previousPods)
			}
		}, NodeTimeout(framework.WaitMedium))

		endpointTests(intermediateTests)
	})

	Context("with StrictAllComponents and Modern profile", Ordered, func() {
		BeforeAll(func(ctx context.Context) {
			previousPods := waitForReadyPods(ctx, endpoints)

			log := GinkgoLogr
			log.Info("Got previous pods", "previousPods", describePods(previousPods))

			By("Setting TLS configuration to StrictAllComponents + Modern")
			rolloutExpected := setTLSConfig(ctx,
				configv1.TLSAdherencePolicyStrictAllComponents,
				&configv1.TLSSecurityProfile{
					Type:   configv1.TLSProfileModernType,
					Modern: &configv1.ModernTLSProfile{},
				},
			)

			if rolloutExpected {
				waitForRollout(ctx, endpoints, previousPods)
			}
		}, NodeTimeout(framework.WaitMedium))

		endpointTests(modernTests)
	})
})

// tlsVersionEntries generates DescribeTable entries for all combinations of
// endpoints and TLS test cases.
func tlsVersionEntries(endpoints []tlsEndpoint, tests []tlsTestCase) []TableEntry {
	GinkgoHelper()

	var entries []TableEntry
	for _, ep := range endpoints {
		for _, tc := range tests {
			action := "reject"
			if tc.shouldSucceed {
				action = "accept"
			}
			desc := fmt.Sprintf("should %s %s on %s", action, tc.versionName, ep.name)
			entries = append(entries, Entry(desc, ep, tc.version, tc.shouldSucceed, NodeTimeout(framework.WaitShort)))
		}
	}
	return entries
}

func describePods(pods []corev1.Pod) string {
	return strings.Join(util.SliceMap(pods, func(pod corev1.Pod) string {
		var conditions []string
		for _, cond := range pod.Status.Conditions {
			conditions = append(conditions, fmt.Sprintf("%s=%s", cond.Type, cond.Status))
		}
		for _, containerStatus := range pod.Status.ContainerStatuses {
			conditions = append(conditions, fmt.Sprintf("%s.Restarts=%d", containerStatus.Name, containerStatus.RestartCount))
		}
		return pod.Name + " " + strings.Join(conditions, ",")
	}), ", ")
}

// assertTLSVersion tests that a TLS connection to the given endpoint either
// succeeds or fails at the specified TLS version.
func assertTLSVersion(ctx context.Context, ep tlsEndpoint, tlsVersion uint16, shouldSucceed bool, caCertPool *x509.CertPool) {
	log := GinkgoLogr

	var readyPods []corev1.Pod
	By(fmt.Sprintf("Waiting for %s endpoint to be ready", ep.name), func() {
		Eventually(func(g Gomega) []corev1.Pod {
			readyPods = readyPodsForEndpoint(g, ctx, ep)
			log.Info("Got ready pods", "currentPods", describePods(readyPods))
			return readyPods
		}).WithContext(ctx).WithTimeout(framework.WaitShort).Should(HaveLen(1))
	})
	pod := readyPods[0]

	var expectation types.GomegaMatcher
	var message string
	if shouldSucceed {
		expectation = Not(HaveOccurred())
		message = fmt.Sprintf("TLS connection should succeed on %s at version 0x%04x", ep.name, tlsVersion)
	} else {
		expectation = MatchError("remote error: tls: protocol version not supported")
		message = fmt.Sprintf("connection to %s at version 0x%04x should be rejected with a TLS protocol version error", ep.name, tlsVersion)
	}

	Eventually(func(g Gomega) error {
		localPort, err := portForwardToPod(ctx, restConfig, ep.namespace, pod.Name, ep.port)
		g.Expect(err).NotTo(HaveOccurred(), "port-forward to %s/%s:%d should succeed", ep.namespace, pod.Name, ep.port)

		addr := fmt.Sprintf("127.0.0.1:%d", localPort)
		return tryTLSConnect(ctx, addr, tlsVersion, caCertPool, ep.serverName)
	}).WithContext(ctx).
		WithTimeout(10*time.Second).WithPolling(1*time.Second).Should(expectation, message)
}

// tryTLSConnect attempts a TLS handshake to addr forcing the given TLS version.
// Both MinVersion and MaxVersion are set to tlsVersion to test exactly that version.
func tryTLSConnect(ctx context.Context, addr string, tlsVersion uint16, caCertPool *x509.CertPool, serverName string) error {
	tlsCfg := &tls.Config{
		MinVersion: tlsVersion,
		MaxVersion: tlsVersion,
	}

	// Some endpoints use self-signed certs, so skip verification.
	if serverName != "" {
		tlsCfg.RootCAs = caCertPool
		tlsCfg.ServerName = serverName
	} else {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec
	}

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 5 * time.Second},
		Config:    tlsCfg,
	}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

// loadServiceCACert loads the service-ca CA certificate from the
// kube-public/openshift-service-ca.crt ConfigMap.
func loadServiceCACert(ctx context.Context) *x509.CertPool {
	GinkgoHelper()

	cm := &corev1.ConfigMap{}
	Expect(cl.Get(ctx, client.ObjectKey{
		Namespace: "kube-public",
		Name:      "openshift-service-ca.crt",
	}, cm)).To(Succeed(), "should be able to read service-ca CA ConfigMap")

	caPEM, ok := cm.Data["service-ca.crt"]
	Expect(ok).To(BeTrue(), "service-ca.crt key should exist in ConfigMap")

	pool := x509.NewCertPool()
	Expect(pool.AppendCertsFromPEM([]byte(caPEM))).To(BeTrue(),
		"service-ca CA certificate should parse successfully")

	return pool
}

// setTLSConfig updates the APIServer CR with the given TLS adherence policy
// and security profile. It returns the server-side timestamp of the update
// from the managed fields entry, which can be used to verify that pods have
// restarted since the configuration change, and a bool indicating whether the
// change is expected to trigger a rollout. The rollout detection mirrors the
// SecurityProfileWatcher logic: a rollout is expected when either the resolved
// TLS profile spec changes or the adherence policy changes.
func setTLSConfig(ctx context.Context, adherence configv1.TLSAdherencePolicy, profile *configv1.TLSSecurityProfile) bool {
	GinkgoHelper()

	apiServer := &configv1.APIServer{}
	Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, apiServer)).To(Succeed())

	// Resolve the current and new TLS profile specs to determine whether the
	// SecurityProfileWatcher will detect a change and trigger a restart.
	currentProfileSpec, err := commoncmdoptions.TLSProfileSpecFromClusterConfig(apiServer.Spec.TLSAdherence, apiServer.Spec.TLSSecurityProfile)
	Expect(err).NotTo(HaveOccurred(), "should resolve TLS profile spec")

	newProfileSpec, err := commoncmdoptions.TLSProfileSpecFromClusterConfig(adherence, profile)
	Expect(err).NotTo(HaveOccurred(), "should resolve new TLS profile spec")

	rolloutExpected := !reflect.DeepEqual(currentProfileSpec, newProfileSpec) ||
		apiServer.Spec.TLSAdherence != adherence

	apiServer.Spec.TLSAdherence = adherence
	apiServer.Spec.TLSSecurityProfile = profile

	Expect(cl.Update(ctx, apiServer, client.FieldOwner("tls-e2e-test"))).To(Succeed(),
		"should update APIServer TLS configuration")

	return rolloutExpected
}

func filterEndpoints(ctx context.Context, endpoints []tlsEndpoint) []tlsEndpoint {
	GinkgoHelper()

	return util.SliceFilter(endpoints, func(ep tlsEndpoint) bool {
		for _, testIf := range ep.testIf {
			if !testIf(ctx) {
				return false
			}
		}
		return true
	})
}

func waitForReadyPods(ctx context.Context, endpoints []tlsEndpoint) []corev1.Pod {
	GinkgoHelper()
	log := GinkgoLogr

	filteredEndpoints := filterEndpoints(ctx, endpoints)

	var allPods []corev1.Pod
	Eventually(func(g Gomega) []corev1.Pod {
		allPods = nil
		for _, ep := range filteredEndpoints {
			// Wait until there is exactly one Running pod for the endpoint.
			var endpointPods []corev1.Pod
			Eventually(func(g Gomega) []corev1.Pod {
				endpointPods = readyPodsForEndpoint(g, ctx, ep)
				log.Info("Ready pods", "endpoint", ep.name, "pods", describePods(endpointPods))
				return endpointPods
			}).WithContext(ctx).WithTimeout(framework.WaitMedium).Should(HaveLen(1))

			allPods = append(allPods, endpointPods...)
		}
		return allPods
	}).WithContext(ctx).WithTimeout(framework.WaitMedium).Should(HaveLen(len(filteredEndpoints)))
	return allPods
}

func waitForRollout(ctx context.Context, endpoints []tlsEndpoint, previousPods []corev1.Pod) {
	GinkgoHelper()
	log := GinkgoLogr

	filteredEndpoints := filterEndpoints(ctx, endpoints)

	for _, ep := range filteredEndpoints {
		By(fmt.Sprintf("Waiting for rollout of %s", ep.name), func() {
			Eventually(func(g Gomega) []corev1.Pod {
				currentPods := podsForEndpoint(g, ctx, ep)
				log.Info("Current pods", "endpoint", ep.name, "pods", describePods(currentPods))
				return currentPods
			}).WithContext(ctx).WithTimeout(framework.WaitMedium).Should(SatisfyAll(
				// There should be exactly one pod, and it should be Ready
				HaveLen(1),
				ContainElement(
					HaveField("Status.Conditions", test.HaveCondition(corev1.PodReady).WithStatus(corev1.ConditionTrue)),
				),

				SatisfyAny(
					// It should either be a new pod, i.e. not in previousPods, or
					Not(ContainElement(Satisfy(
						func(currentPod corev1.Pod) bool {
							for _, previousPod := range previousPods {
								if currentPod.Name == previousPod.Name {
									return true
								}
							}
							return false
						},
					))),

					// if expectRestart, an old pod with a higher restart count than it had before
					ContainElement(Satisfy(
						func(currentPod corev1.Pod) bool {
							if !ep.expectRestart {
								return false
							}

							for _, previousPod := range previousPods {
								if currentPod.Name == previousPod.Name {
									for i, containerStatus := range currentPod.Status.ContainerStatuses {
										if containerStatus.Name == ep.containerName {
											// Assume containers are in the same order in both statuses
											return containerStatus.RestartCount > previousPod.Status.ContainerStatuses[i].RestartCount
										}
									}
									log.Info("Expected container not found", "pod", currentPod.Name, "container", ep.containerName)
									return false
								}
							}
							return false
						},
					)),
				),
			))
		})
	}
}

func podsForEndpoint(g Gomega, ctx context.Context, ep tlsEndpoint) []corev1.Pod {
	GinkgoHelper()

	pods := &corev1.PodList{}
	g.Expect(cl.List(ctx, pods,
		client.InNamespace(ep.namespace),
		client.MatchingLabels(ep.labels),
	)).To(Succeed())

	return pods.Items
}

// readyPodsForEndpoint returns all Ready pods matching the given labels in the
// specified namespace.
func readyPodsForEndpoint(g Gomega, ctx context.Context, ep tlsEndpoint) []corev1.Pod {
	GinkgoHelper()

	pods := podsForEndpoint(g, ctx, ep)

	var readyPods []corev1.Pod
	for i := range pods {
		for _, cond := range pods[i].Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				readyPods = append(readyPods, pods[i])
				break
			}
		}
	}

	return readyPods
}

// portForwardToPod establishes a port-forward to the specified pod's container
// port. It returns the local port and a cancel function. Call the cancel
// function to tear down the port-forward. The port-forward is also
// automatically stopped when the context is cancelled.
func portForwardToPod(ctx context.Context, cfg *rest.Config, namespace, podName string, remotePort int) (int, error) {
	roundTripper, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return 0, fmt.Errorf("creating SPDY round tripper: %w", err)
	}

	serverURL, err := url.Parse(cfg.Host)
	if err != nil {
		return 0, fmt.Errorf("parsing API server URL: %w", err)
	}
	serverURL.Path = fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", namespace, podName)

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, serverURL)

	stopCh := make(chan struct{})
	context.AfterFunc(ctx, func() { close(stopCh) })
	readyCh := make(chan struct{})

	pf, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", remotePort)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return 0, fmt.Errorf("creating port forwarder: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- pf.ForwardPorts()
	}()

	select {
	case <-readyCh:
	case err := <-errCh:
		return 0, fmt.Errorf("port forwarding failed: %w", err)
	case <-ctx.Done():
		return 0, ctx.Err()
	}

	ports, err := pf.GetPorts()
	if err != nil {
		return 0, fmt.Errorf("getting forwarded ports: %w", err)
	}

	return int(ports[0].Local), nil
}

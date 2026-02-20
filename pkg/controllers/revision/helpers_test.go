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

package revision

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	testmatchers "github.com/openshift/cluster-capi-operator/pkg/test/matchers"
)

// Helper to start and stop a manager for a test.
type managerWrapper struct {
	ctrl.Manager

	cancel context.CancelFunc
	done   chan struct{}
}

func newManagerWrapper(providerImgs []providerimages.ProviderImageManifests) *managerWrapper {
	// Don't use the BeforeEach context because it will be cancelled before the test starts.
	ctx := context.Background()

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: cl.Scheme(),
		Controller: ctrlconfig.Controller{
			SkipNameValidation: ptr.To(true),
		},
	})
	Expect(err).NotTo(HaveOccurred())

	err = (&RevisionController{
		Client:           mgr.GetClient(),
		ProviderProfiles: providerImgs,
		ReleaseVersion:   "4.18.0",
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	mgrCtx, mgrCancel := context.WithCancel(ctx)
	mgrDone := make(chan struct{})

	go func() {
		defer GinkgoRecover()
		defer close(mgrDone)
		Expect(mgr.Start(mgrCtx)).To(Succeed())
	}()

	return &managerWrapper{mgr, mgrCancel, mgrDone}
}

func (m *managerWrapper) stop() {
	By("Stopping the manager")
	m.cancel()
	Eventually(m.done).Should(BeClosed())
}

func waitForConditions(ctx context.Context, matchers ...types.GomegaMatcher) {
	Eventually(func(g Gomega) {
		co := &configv1.ClusterOperator{}
		g.Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster-api"}, co)).To(Succeed())

		for _, matcher := range matchers {
			g.Expect(co.Status.Conditions).To(matcher)
		}
	}).WithContext(ctx).Should(Succeed())
}

func waitForProgressingFalse(ctx context.Context) {
	waitForConditions(ctx, testmatchers.HaveCondition(conditionTypeProgressing).
		WithStatus(configv1.ConditionFalse))
}

// Helper to create test fixtures

// fixturesOption configures createFixtures behavior.
type fixturesOption func(*fixturesConfig)

type fixturesConfig struct {
	skipInfraStatus bool
	skipClusterAPI  bool
}

func withoutInfraStatus(c *fixturesConfig) { c.skipInfraStatus = true }
func withoutClusterAPI(c *fixturesConfig)  { c.skipClusterAPI = true }

// createFixtures creates test fixtures and sets the package-level vars.
// It registers DeferCleanup to clean up created resources.
func createFixtures(ctx context.Context, opts ...fixturesOption) {
	cfg := &fixturesConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	var cleanupObjs []client.Object

	DeferCleanup(func(ctx context.Context) {
		By("Cleaning up resources")
		Expect(test.CleanupAndWait(ctx, cl, cleanupObjs...)).To(Succeed())
	})

	// Create Infrastructure singleton
	infra = &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
	}
	Expect(cl.Create(ctx, infra)).To(Succeed())
	cleanupObjs = append(cleanupObjs, infra)

	if !cfg.skipInfraStatus {
		infraFixtureAddStatus(infra)
		Expect(cl.Status().Update(ctx, infra)).To(Succeed())
	}

	// Create ClusterAPI singleton
	if !cfg.skipClusterAPI {
		clusterAPI = &operatorv1alpha1.ClusterAPI{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			Spec:       &operatorv1alpha1.ClusterAPISpec{},
		}
		Expect(cl.Create(ctx, clusterAPI)).To(Succeed())
		cleanupObjs = append(cleanupObjs, clusterAPI)
	}

	// Create ClusterOperator singleton
	clusterOperator = &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-api"},
	}
	Expect(cl.Create(ctx, clusterOperator)).To(Succeed())
	cleanupObjs = append(cleanupObjs, clusterOperator)

	By("creating manifest files for default provider images")
	manifestDir, err := os.MkdirTemp("", "revision-test-manifests")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		os.RemoveAll(manifestDir)
	})

	coreManifestPath := filepath.Join(manifestDir, "core-manifests.yaml")
	Expect(os.WriteFile(coreManifestPath, []byte(configMapYAML("core-config")), 0644)).To(Succeed())
	defaultProviderImgs[0].ManifestsPath = coreManifestPath

	infraManifestPath := filepath.Join(manifestDir, "infra-aws-manifests.yaml")
	Expect(os.WriteFile(infraManifestPath, []byte(configMapYAML("infra-config")), 0644)).To(Succeed())
	defaultProviderImgs[1].ManifestsPath = infraManifestPath
}

func infraFixtureAddStatus(infra *configv1.Infrastructure) {
	infra.Status = configv1.InfrastructureStatus{
		ControlPlaneTopology:   configv1.HighlyAvailableTopologyMode,
		InfrastructureTopology: configv1.HighlyAvailableTopologyMode,
		PlatformStatus: &configv1.PlatformStatus{
			Type: configv1.AWSPlatformType,
		},
	}
}

// configMapYAML returns a minimal valid ConfigMap YAML document.
func configMapYAML(name string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: default
data:
  key: value`, name)
}

// writeTestManifestFile writes content to a file in dir and returns the path.
func writeTestManifestFile(dir, filename, content string) string {
	path := filepath.Join(dir, filename)
	ExpectWithOffset(1, os.WriteFile(path, []byte(content), 0644)).To(Succeed())

	return path
}

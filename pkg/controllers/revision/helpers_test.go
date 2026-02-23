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
	"slices"

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

	// Clone so callers' slices are not mutated.
	imgs := slices.Clone(providerImgs)
	ensureManifestPaths(imgs)

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: cl.Scheme(),
		Controller: ctrlconfig.Controller{
			SkipNameValidation: ptr.To(true),
		},
	})
	Expect(err).NotTo(HaveOccurred())

	err = (&RevisionController{
		Client:           mgr.GetClient(),
		ProviderProfiles: imgs,
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
	GinkgoHelper()

	co := &configv1.ClusterOperator{}
	co.SetName("cluster-api")
	Eventually(kWithCtx(ctx).Object(co)).
		WithContext(ctx).
		Should(HaveField("Status.Conditions", SatisfyAll(matchers...)))
}

func waitForProgressingFalse(ctx context.Context) {
	GinkgoHelper()
	waitForConditions(ctx, test.HaveCondition(conditionTypeProgressing).
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
	GinkgoHelper()

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

// latestRevision returns the revision with the highest Revision number.
func latestRevision(revisions []operatorv1alpha1.ClusterAPIInstallerRevision) operatorv1alpha1.ClusterAPIInstallerRevision {
	latest := revisions[0]
	for _, rev := range revisions[1:] {
		if rev.Revision > latest.Revision {
			latest = rev
		}
	}

	return latest
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

// ensureManifestPaths creates manifest files for providers that don't already
// have a ManifestsPath set. Each provider's ContentID is used as the ConfigMap
// name so that different providers produce different revision contentIDs.
func ensureManifestPaths(providerImgs []providerimages.ProviderImageManifests) {
	GinkgoHelper()

	manifestDir, err := os.MkdirTemp("", "revision-test-manifests")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		Expect(os.RemoveAll(manifestDir)).To(Succeed())
	})

	for i := range providerImgs {
		if providerImgs[i].ManifestsPath == "" {
			path := filepath.Join(manifestDir, providerImgs[i].Name+"-manifests.yaml")
			Expect(os.WriteFile(path, []byte(configMapYAML(providerImgs[i].ContentID)), 0644)).To(Succeed())
			providerImgs[i].ManifestsPath = path
		}
	}
}

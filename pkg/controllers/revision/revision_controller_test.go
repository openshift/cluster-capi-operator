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
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	testmatchers "github.com/openshift/cluster-capi-operator/pkg/test/matchers"
)

var (
	// Test fixtures - set by createFixtures.
	infra           *configv1.Infrastructure
	clusterAPI      *operatorv1alpha1.ClusterAPI
	clusterOperator *configv1.ClusterOperator

	defaultProviderImgs []providerimages.ProviderImageManifests = []providerimages.ProviderImageManifests{
		{
			ProviderMetadata: providerimages.ProviderMetadata{
				Name:         "core",
				InstallOrder: 10,
			},
			ContentID: "core-content-id",
			ImageRef:  "registry.example.com/core@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Profile:   "default",
		},
		{
			ProviderMetadata: providerimages.ProviderMetadata{
				Name:         "infra-aws",
				InstallOrder: 20,
				OCPPlatform:  configv1.AWSPlatformType,
			},
			ContentID: "infra-aws-content-id",
			ImageRef:  "registry.example.com/infra-aws@sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
			Profile:   "default",
		},
	}
)

const (
	conditionTypeProgressing = "RevisionControllerProgressing"
	conditionTypeDegraded    = "RevisionControllerDegraded"

	subResourceStatus = "status"
)

func TestGetLatestRevision(t *testing.T) {
	tests := []struct {
		name             string
		revisions        []operatorv1alpha1.ClusterAPIInstallerRevision
		expectedNil      bool
		expectedRevision int64
		expectedName     operatorv1alpha1.RevisionName
	}{
		{
			name:        "returns nil for empty slice",
			revisions:   []operatorv1alpha1.ClusterAPIInstallerRevision{},
			expectedNil: true,
		},
		{
			name: "returns single revision",
			revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
				{Name: "rev-1", Revision: 1},
			},
			expectedRevision: 1,
		},
		{
			name: "returns highest from ascending order",
			revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
				{Name: "rev-1", Revision: 1},
				{Name: "rev-2", Revision: 2},
				{Name: "rev-3", Revision: 3},
			},
			expectedRevision: 3,
		},
		{
			name: "returns highest from descending order",
			revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
				{Name: "rev-3", Revision: 3},
				{Name: "rev-2", Revision: 2},
				{Name: "rev-1", Revision: 1},
			},
			expectedRevision: 3,
		},
		{
			name: "returns highest from unsorted",
			revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
				{Name: "rev-2", Revision: 2},
				{Name: "rev-5", Revision: 5},
				{Name: "rev-3", Revision: 3},
			},
			expectedRevision: 5,
		},
		{
			name: "returns first when duplicate revision numbers",
			revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
				{Name: "first", Revision: 1},
				{Name: "second", Revision: 1},
			},
			expectedRevision: 1,
			expectedName:     "first",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result := getLatestRevision(tt.revisions)

			if tt.expectedNil {
				g.Expect(result).To(BeNil())
				return
			}

			g.Expect(result).ToNot(BeNil())
			g.Expect(result.Revision).To(Equal(tt.expectedRevision))

			if tt.expectedName != "" {
				g.Expect(result.Name).To(Equal(tt.expectedName))
			}
		})
	}
}

func TestBuildComponentList(t *testing.T) {
	tests := []struct {
		name               string
		providers          []providerimages.ProviderImageManifests
		platform           configv1.PlatformType
		expectedContentIDs []string
	}{
		{
			name: "orders components by type and platform scope",
			providers: []providerimages.ProviderImageManifests{
				{ProviderMetadata: providerimages.ProviderMetadata{Name: "infra-aws", InstallOrder: 20, OCPPlatform: configv1.AWSPlatformType}, ContentID: "infra-aws-content"},
				{ProviderMetadata: providerimages.ProviderMetadata{Name: "core", InstallOrder: 10}, ContentID: "core-content"},
				{ProviderMetadata: providerimages.ProviderMetadata{Name: "infra-global", InstallOrder: 20}, ContentID: "infra-global-content"},
				{ProviderMetadata: providerimages.ProviderMetadata{Name: "core-aws", InstallOrder: 10, OCPPlatform: configv1.AWSPlatformType}, ContentID: "core-aws-content"},
			},
			platform: configv1.AWSPlatformType,
			// Expected order: core+global, core+platform, infra+global, infra+platform
			expectedContentIDs: []string{"core-content", "core-aws-content", "infra-global-content", "infra-aws-content"},
		},
		{
			name: "filters out providers for other platforms",
			providers: []providerimages.ProviderImageManifests{
				{ProviderMetadata: providerimages.ProviderMetadata{Name: "infra-aws", InstallOrder: 20, OCPPlatform: configv1.AWSPlatformType}, ContentID: "infra-aws-content"},
				{ProviderMetadata: providerimages.ProviderMetadata{Name: "core", InstallOrder: 10}, ContentID: "core-content"},
				{ProviderMetadata: providerimages.ProviderMetadata{Name: "infra-gcp", InstallOrder: 20, OCPPlatform: configv1.GCPPlatformType}, ContentID: "infra-gcp-content"},
				{ProviderMetadata: providerimages.ProviderMetadata{Name: "infra-azure", InstallOrder: 20, OCPPlatform: configv1.AzurePlatformType}, ContentID: "infra-azure-content"},
			},
			platform:           configv1.AWSPlatformType,
			expectedContentIDs: []string{"core-content", "infra-aws-content"},
		},
		{
			name:               "returns empty list when no providers",
			providers:          []providerimages.ProviderImageManifests{},
			platform:           configv1.AWSPlatformType,
			expectedContentIDs: []string{},
		},
		{
			name: "includes all global providers regardless of platform",
			providers: []providerimages.ProviderImageManifests{
				{ProviderMetadata: providerimages.ProviderMetadata{Name: "core", InstallOrder: 10}, ContentID: "core-content"},
				{ProviderMetadata: providerimages.ProviderMetadata{Name: "addon", InstallOrder: 20}, ContentID: "addon-content"},
			},
			platform:           configv1.GCPPlatformType,
			expectedContentIDs: []string{"core-content", "addon-content"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			r := &RevisionController{
				ProviderProfiles: tt.providers,
			}

			components := r.buildComponentList(tt.platform)

			g.Expect(components).To(HaveLen(len(tt.expectedContentIDs)))

			for i, expectedID := range tt.expectedContentIDs {
				g.Expect(components[i].ContentID).To(Equal(expectedID))
			}
		})
	}
}

var _ = Describe("RevisionController", Serial, func() {
	var (
		mgr *managerWrapper
	)

	BeforeEach(func(ctx context.Context) {
		createFixtures(ctx)

		// Create manager and controller
		mgr = newManagerWrapper(defaultProviderImgs)
		DeferCleanup(func(ctx context.Context) {
			mgr.stop()
		})

		waitForProgressingFalse(ctx)
	}, defaultNodeTimeout)

	It("creates first revision on empty ClusterAPI", func(ctx context.Context) {
		updatedClusterAPI := &operatorv1alpha1.ClusterAPI{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, updatedClusterAPI)).To(Succeed())
		Expect(updatedClusterAPI.Status.Revisions).To(HaveLen(1))

		rev := updatedClusterAPI.Status.Revisions[0]
		Expect(rev.Revision).To(Equal(int64(1)))
		Expect(rev.ContentID).NotTo(BeEmpty())

		// Should have 2 components: core (global) and infra-aws
		Expect(rev.Components).To(HaveLen(2))
		Expect(rev.Components[0].Image.Ref).To(Equal(operatorv1alpha1.ImageDigestFormat(defaultProviderImgs[0].ImageRef)))
		Expect(rev.Components[1].Image.Ref).To(Equal(operatorv1alpha1.ImageDigestFormat(defaultProviderImgs[1].ImageRef)))

		// DesiredRevision should point to the created revision
		Expect(updatedClusterAPI.Status.DesiredRevision).To(Equal(rev.Name))

		// Conditions should indicate success
		co := &configv1.ClusterOperator{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster-api"}, co)).To(Succeed())
		Expect(co.Status.Conditions).To(testmatchers.HaveCondition(conditionTypeProgressing).
			WithStatus(configv1.ConditionFalse).
			WithReason(operatorstatus.ReasonAsExpected))
		Expect(co.Status.Conditions).To(testmatchers.HaveCondition(conditionTypeDegraded).
			WithStatus(configv1.ConditionFalse).
			WithReason(operatorstatus.ReasonAsExpected))
	}, defaultNodeTimeout)

	It("does not modify up to date revision list", func(ctx context.Context) {
		// Capture state after first reconcile
		initialClusterAPI := &operatorv1alpha1.ClusterAPI{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, initialClusterAPI)).To(Succeed())
		Expect(initialClusterAPI.Status.Revisions).To(HaveLen(1))
		originalRevisions := initialClusterAPI.Status.Revisions
		originalDesiredRevision := initialClusterAPI.Status.DesiredRevision

		// Stop the manager so we can modify conditions
		mgr.stop()

		// Re-fetch the ClusterOperator to get the latest resourceVersion,
		// then make conditions stale so we can detect that a second reconcile ran
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster-api"}, clusterOperator)).To(Succeed())
		clusterOperator.Status.Conditions = []configv1.ClusterOperatorStatusCondition{}
		Expect(cl.Status().Update(ctx, clusterOperator)).To(Succeed())

		// Restart the manager with the same provider images
		mgr = newManagerWrapper(defaultProviderImgs)

		// Wait for conditions to be written back, proving the second reconcile ran
		waitForConditions(ctx,
			testmatchers.HaveCondition(conditionTypeProgressing).
				WithStatus(configv1.ConditionFalse).
				WithReason(operatorstatus.ReasonAsExpected),
			testmatchers.HaveCondition(conditionTypeDegraded).
				WithStatus(configv1.ConditionFalse).
				WithReason(operatorstatus.ReasonAsExpected),
		)

		// Verify revisions are unchanged
		updatedClusterAPI := &operatorv1alpha1.ClusterAPI{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, updatedClusterAPI)).To(Succeed())
		Expect(updatedClusterAPI.Status.Revisions).To(Equal(originalRevisions))
		Expect(updatedClusterAPI.Status.DesiredRevision).To(Equal(originalDesiredRevision))
	}, defaultNodeTimeout)

	It("creates additional revision when contentID changes", func(ctx context.Context) {
		// Stop first manager
		mgr.stop()

		// Capture the first revision
		initialClusterAPI := &operatorv1alpha1.ClusterAPI{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, initialClusterAPI)).To(Succeed())
		Expect(initialClusterAPI.Status.Revisions).To(HaveLen(1))
		originalRev1 := initialClusterAPI.Status.Revisions[0]

		// Create manifest files with different content to produce a different contentID
		manifestDir := GinkgoT().TempDir()
		updatedProviderImgs := []providerimages.ProviderImageManifests{
			{
				ProviderMetadata: providerimages.ProviderMetadata{
					Name:         "core",
					InstallOrder: 10,
				},
				ContentID:     "core-content-id-2",
				ImageRef:      "registry.example.com/core@sha256:1111111111111111111111111111111111111111111111111111111111111111",
				Profile:       "default",
				ManifestsPath: writeTestManifestFile(manifestDir, "core-manifests.yaml", configMapYAML("core-config-updated")),
			},
			{
				ProviderMetadata: providerimages.ProviderMetadata{
					Name:         "infra-aws",
					InstallOrder: 20,
					OCPPlatform:  configv1.AWSPlatformType,
				},
				ContentID:     "infra-aws-content-id-2",
				ImageRef:      "registry.example.com/infra-aws@sha256:2222222222222222222222222222222222222222222222222222222222222222",
				Profile:       "default",
				ManifestsPath: writeTestManifestFile(manifestDir, "infra-aws-manifests.yaml", configMapYAML("infra-config-updated")),
			},
		}

		mgr = newManagerWrapper(updatedProviderImgs)

		Eventually(func(g Gomega) {
			updatedClusterAPI := &operatorv1alpha1.ClusterAPI{}
			g.Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, updatedClusterAPI)).To(Succeed())
			g.Expect(updatedClusterAPI.Status.Revisions).To(HaveLen(2))
			g.Expect(updatedClusterAPI.Status.Revisions[1].Revision).To(Equal(int64(2)))
		}).WithContext(ctx).Should(Succeed())

		// Verify both revisions have the expected contents
		updatedClusterAPI := &operatorv1alpha1.ClusterAPI{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, updatedClusterAPI)).To(Succeed())

		// First revision should be completely unchanged
		Expect(updatedClusterAPI.Status.Revisions[0]).To(Equal(originalRev1))

		// Second revision should have the updated provider images
		rev2 := updatedClusterAPI.Status.Revisions[1]
		Expect(rev2.ContentID).NotTo(Equal(originalRev1.ContentID))
		Expect(rev2.Components[0].Image.Ref).To(Equal(operatorv1alpha1.ImageDigestFormat(updatedProviderImgs[0].ImageRef)))
		Expect(rev2.Components[1].Image.Ref).To(Equal(operatorv1alpha1.ImageDigestFormat(updatedProviderImgs[1].ImageRef)))

		// DesiredRevision should point to the new revision
		Expect(updatedClusterAPI.Status.DesiredRevision).To(Equal(rev2.Name))
	}, defaultNodeTimeout)

	It("sets Degraded=True with NonRetryableError when max revisions reached", func(ctx context.Context) {
		// Refresh the clusterAPI object before updating the revisions
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, clusterAPI)).To(Succeed())

		// Add more revisions until we have 16
		for {
			i := len(clusterAPI.Status.Revisions)
			if i >= 16 {
				break
			}

			// Create a valid 64-character hex digest that varies by index
			hexDigit := string("0123456789abcdef"[i])
			digest := hexDigit + "123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			clusterAPI.Status.Revisions = append(clusterAPI.Status.Revisions, operatorv1alpha1.ClusterAPIInstallerRevision{
				Name:      operatorv1alpha1.RevisionName("rev-" + string(rune('a'+i))),
				Revision:  int64(i + 1),
				ContentID: "content-id-" + string(rune('a'+i)),
				Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
					{
						Type: operatorv1alpha1.InstallerComponentTypeImage,
						Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
							Ref:     operatorv1alpha1.ImageDigestFormat("quay.io/openshift/cluster-capi-operator@sha256:" + digest),
							Profile: "default",
						},
					},
				},
			})
		}

		clusterAPI.Status.DesiredRevision = clusterAPI.Status.Revisions[len(clusterAPI.Status.Revisions)-1].Name
		Expect(cl.Status().Update(ctx, clusterAPI)).To(Succeed())

		waitForConditions(ctx,
			testmatchers.HaveCondition(conditionTypeProgressing).
				WithStatus(configv1.ConditionFalse).
				WithReason(operatorstatus.ReasonNonRetryableError),
			testmatchers.HaveCondition(conditionTypeDegraded).
				WithStatus(configv1.ConditionTrue).
				WithReason(operatorstatus.ReasonNonRetryableError),
		)

		// Revisions should still be 16 (no new revision created)
		updatedClusterAPI := &operatorv1alpha1.ClusterAPI{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, updatedClusterAPI)).To(Succeed())
		Expect(updatedClusterAPI.Status.Revisions).To(HaveLen(16))

	}, defaultNodeTimeout)
})

var _ = Describe("RevisionController waiting states", Serial, func() {
	Context("when Infrastructure PlatformStatus is nil", func() {
		BeforeEach(func(ctx context.Context) {
			createFixtures(ctx, withoutInfraStatus)

			mgr := newManagerWrapper(defaultProviderImgs)
			DeferCleanup(func(ctx context.Context) {
				mgr.stop()
			})

			waitForConditions(ctx,
				testmatchers.HaveCondition(conditionTypeProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonWaitingOnExternal),
			)
		}, defaultNodeTimeout)

		It("sets Progressing=True with WaitingOnExternal reason", func(ctx context.Context) {
			Eventually(func(g Gomega) {
				// No revisions should be created
				updatedClusterAPI := &operatorv1alpha1.ClusterAPI{}
				g.Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, updatedClusterAPI)).To(Succeed())
				g.Expect(updatedClusterAPI.Status.Revisions).To(BeEmpty())
			}).WithContext(ctx).Should(Succeed())
		}, defaultNodeTimeout)

		It("creates revision after Infrastructure gets PlatformStatus", func(ctx context.Context) {
			// Now update Infrastructure with PlatformStatus
			Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, infra)).To(Succeed())
			infraFixtureAddStatus(infra)
			Expect(cl.Status().Update(ctx, infra)).To(Succeed())

			waitForProgressingFalse(ctx)

			// Should have created a revision
			updatedClusterAPI := &operatorv1alpha1.ClusterAPI{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, updatedClusterAPI)).To(Succeed())
			Expect(updatedClusterAPI.Status.Revisions).To(HaveLen(1))
		}, defaultNodeTimeout)
	})

	Context("when ClusterAPI does not exist", func() {
		BeforeEach(func(ctx context.Context) {
			createFixtures(ctx, withoutClusterAPI)

			mgr := newManagerWrapper(defaultProviderImgs)
			DeferCleanup(func(ctx context.Context) {
				mgr.stop()
			})
		}, defaultNodeTimeout)

		It("creates revision after ClusterAPI is created", func(ctx context.Context) {
			// Wait for WaitingOnExternal state
			waitForConditions(ctx,
				testmatchers.HaveCondition(conditionTypeProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonWaitingOnExternal),
			)

			// Create ClusterAPI
			clusterAPI = &operatorv1alpha1.ClusterAPI{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: &operatorv1alpha1.ClusterAPISpec{},
			}
			Expect(cl.Create(ctx, clusterAPI)).To(Succeed())
			DeferCleanup(func(ctx context.Context) {
				By("Deleting ClusterAPI")
				Expect(test.CleanupAndWait(ctx, cl, clusterAPI)).To(Succeed())
			})

			waitForProgressingFalse(ctx)

			// Should have created a revision
			updatedClusterAPI := &operatorv1alpha1.ClusterAPI{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, updatedClusterAPI)).To(Succeed())
			Expect(updatedClusterAPI.Status.Revisions).To(HaveLen(1))
		}, defaultNodeTimeout)
	})
})

var _ = Describe("RevisionController error handling", Serial, func() {
	var (
		testErr = errors.New("simulated status update error")

		interceptorCl client.Client
		r             *RevisionController
	)

	BeforeEach(func(ctx context.Context) {
		createFixtures(ctx)

		interceptorCl = interceptor.NewClient(cl, interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if _, ok := obj.(*operatorv1alpha1.ClusterAPI); ok && subResourceName == subResourceStatus {
					return testErr
				}

				return c.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		})

		r = &RevisionController{
			Client:           interceptorCl,
			ProviderProfiles: defaultProviderImgs,
			ReleaseVersion:   "4.18.0",
		}
	}, defaultNodeTimeout)

	It("sets Progressing=True with EphemeralError on client status update error", func(ctx context.Context) {
		req := reconcile.Request{NamespacedName: client.ObjectKey{Name: "cluster"}}
		_, err := r.Reconcile(ctx, req)
		Expect(err).To(HaveOccurred())

		// Verify conditions show ephemeral error
		co := &configv1.ClusterOperator{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster-api"}, co)).To(Succeed())

		Expect(co.Status.Conditions).To(testmatchers.HaveCondition(conditionTypeProgressing).
			WithStatus(configv1.ConditionTrue).
			WithReason(operatorstatus.ReasonEphemeralError).
			WithMessage(ContainSubstring(testErr.Error())))
		Expect(co.Status.Conditions).To(testmatchers.HaveCondition(conditionTypeDegraded).
			WithStatus(configv1.ConditionFalse).
			WithReason(operatorstatus.ReasonProgressing))
	}, defaultNodeTimeout)
})

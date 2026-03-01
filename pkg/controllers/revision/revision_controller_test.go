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
)

var (
	// Test fixtures - set by createFixtures.
	infra           *configv1.Infrastructure
	clusterAPI      *operatorv1alpha1.ClusterAPI
	clusterOperator *configv1.ClusterOperator

	defaultProviderImgs = []providerimages.ProviderImageManifests{
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

	updatedProviderImgs = []providerimages.ProviderImageManifests{
		{
			ProviderMetadata: providerimages.ProviderMetadata{
				Name:         "core",
				InstallOrder: 10,
			},
			ContentID: "core-content-id-2",
			ImageRef:  "registry.example.com/core@sha256:1111111111111111111111111111111111111111111111111111111111111111",
			Profile:   "default",
		},
		{
			ProviderMetadata: providerimages.ProviderMetadata{
				Name:         "infra-aws",
				InstallOrder: 20,
				OCPPlatform:  configv1.AWSPlatformType,
			},
			ContentID: "infra-aws-content-id-2",
			ImageRef:  "registry.example.com/infra-aws@sha256:2222222222222222222222222222222222222222222222222222222222222222",
			Profile:   "default",
		},
	}

	nonMatchingProviderImgs = []providerimages.ProviderImageManifests{
		{
			ProviderMetadata: providerimages.ProviderMetadata{
				Name:         "infra-gcp",
				InstallOrder: 20,
				OCPPlatform:  configv1.GCPPlatformType,
			},
			ContentID: "infra-gcp-content-id",
			ImageRef:  "registry.example.com/infra-gcp@sha256:3333333333333333333333333333333333333333333333333333333333333333",
			Profile:   "default",
		},
	}
)

const (
	conditionTypeProgressing = "RevisionControllerProgressing"
	conditionTypeDegraded    = "RevisionControllerDegraded"

	subResourceStatus = "status"
)

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
		Expect(co.Status.Conditions).To(test.HaveCondition(conditionTypeProgressing).
			WithStatus(configv1.ConditionFalse).
			WithReason(operatorstatus.ReasonAsExpected))
		Expect(co.Status.Conditions).To(test.HaveCondition(conditionTypeDegraded).
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

		// Clear conditions so we can detect that a second reconcile ran
		Eventually(kWithCtx(ctx).UpdateStatus(clusterOperator, func() {
			clusterOperator.Status.Conditions = []configv1.ClusterOperatorStatusCondition{}
		})).WithContext(ctx).Should(Succeed())

		// Trigger a reconcile by touching the watched ClusterAPI resource
		Eventually(kWithCtx(ctx).Update(clusterAPI, func() {
			metav1.SetMetaDataAnnotation(&clusterAPI.ObjectMeta, "test", "trigger-reconcile")
		})).WithContext(ctx).Should(Succeed())

		// Wait for conditions to be written back, proving the second reconcile ran
		waitForConditions(ctx,
			test.HaveCondition(conditionTypeProgressing).
				WithStatus(configv1.ConditionFalse).
				WithReason(operatorstatus.ReasonAsExpected),
			test.HaveCondition(conditionTypeDegraded).
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

		// Restart the manager with different provider images
		mgr = newManagerWrapper(updatedProviderImgs)

		// Wait for the controller to create the second revision
		Eventually(kWithCtx(ctx).Object(clusterAPI)).
			WithContext(ctx).
			Should(HaveField("Status.Revisions", HaveLen(2)))

		// Find old and new revisions
		var oldRev, newRev operatorv1alpha1.ClusterAPIInstallerRevision
		for _, rev := range clusterAPI.Status.Revisions {
			if rev.Name == originalRev1.Name {
				oldRev = rev
			} else {
				newRev = rev
			}
		}

		// Original revision should be completely unchanged
		Expect(oldRev).To(Equal(originalRev1))

		// New revision should have the updated provider images
		Expect(newRev.Revision).To(Equal(int64(2)))
		Expect(newRev.ContentID).NotTo(Equal(originalRev1.ContentID))
		Expect(newRev.Components[0].Image.Ref).To(Equal(operatorv1alpha1.ImageDigestFormat(updatedProviderImgs[0].ImageRef)))
		Expect(newRev.Components[1].Image.Ref).To(Equal(operatorv1alpha1.ImageDigestFormat(updatedProviderImgs[1].ImageRef)))

		// DesiredRevision should point to the new revision
		Expect(clusterAPI.Status.DesiredRevision).To(Equal(newRev.Name))
	}, defaultNodeTimeout)

	It("creates revision with empty components when no providers match the platform", func(ctx context.Context) {
		// Stop manager with default (matching) providers
		mgr.stop()

		// Restart with only GCP providers on an AWS cluster — no providers match
		mgr = newManagerWrapper(nonMatchingProviderImgs)

		// Wait for the controller to create the second revision
		Eventually(kWithCtx(ctx).Object(clusterAPI)).
			WithContext(ctx).
			Should(HaveField("Status.Revisions", HaveLen(2)))

		// The new revision should have empty components
		newRev := latestRevision(clusterAPI.Status.Revisions)
		Expect(newRev.Components).To(BeEmpty())

		// DesiredRevision should point to the new revision
		Expect(clusterAPI.Status.DesiredRevision).To(Equal(newRev.Name))
	}, defaultNodeTimeout)

	It("trims old revisions when current matches latest", func(ctx context.Context) {
		// BeforeEach created rev1 from defaultProviderImgs.
		mgr.stop()

		// Create rev2 with different content.
		mgr = newManagerWrapper(updatedProviderImgs)

		// Wait for the controller to create the second revision
		Eventually(kWithCtx(ctx).Object(clusterAPI)).
			WithContext(ctx).
			Should(HaveField("Status.Revisions", HaveLen(2)))

		// Set CurrentRevision to the latest (highest Revision number) via
		// merge patch so we don't disrupt SSA field managers on other fields.
		latest := latestRevision(clusterAPI.Status.Revisions)
		patch := client.MergeFrom(clusterAPI.DeepCopy())
		clusterAPI.Status.CurrentRevision = latest.Name
		Expect(cl.Status().Patch(ctx, clusterAPI, patch)).To(Succeed())

		// Wait for the controller to reconcile and trim old revisions.
		Eventually(kWithCtx(ctx).Object(clusterAPI)).
			WithContext(ctx).
			Should(HaveField("Status.Revisions", HaveLen(1)))

		// Verify the remaining revision is the latest.
		Expect(clusterAPI.Status.Revisions[0].Name).To(Equal(latest.Name))
		Expect(clusterAPI.Status.DesiredRevision).To(Equal(latest.Name))
	}, defaultNodeTimeout)

	It("preserves all revisions when content changes and current is set", func(ctx context.Context) {
		// BeforeEach created rev1 from defaultProviderImgs.
		mgr.stop()

		// Set CurrentRevision to rev1 via merge patch.
		Expect(kWithCtx(ctx).Get(clusterAPI)()).To(Succeed())
		Expect(clusterAPI.Status.Revisions).To(HaveLen(1))
		rev1Name := clusterAPI.Status.Revisions[0].Name
		patch := client.MergeFrom(clusterAPI.DeepCopy())
		clusterAPI.Status.CurrentRevision = rev1Name
		Expect(cl.Status().Patch(ctx, clusterAPI, patch)).To(Succeed())

		// Start with different content, producing a new revision.
		mgr = newManagerWrapper(updatedProviderImgs)

		Eventually(kWithCtx(ctx).Object(clusterAPI)).
			WithContext(ctx).
			Should(HaveField("Status.Revisions", HaveLen(2)))

		// Both revisions preserved: trim did NOT fire because
		// CurrentRevision (rev1) != newest revision (rev2).
		Expect(clusterAPI.Status.DesiredRevision).NotTo(Equal(rev1Name))
		Expect(clusterAPI.Status.CurrentRevision).To(Equal(rev1Name))
	}, defaultNodeTimeout)

	It("sets Degraded=True with NonRetryableError when max revisions reached", func(ctx context.Context) {
		// Refresh the clusterAPI object before updating the revisions
		Expect(kWithCtx(ctx).Get(clusterAPI)()).To(Succeed())

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
			test.HaveCondition(conditionTypeProgressing).
				WithStatus(configv1.ConditionFalse).
				WithReason(operatorstatus.ReasonNonRetryableError),
			test.HaveCondition(conditionTypeDegraded).
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
				test.HaveCondition(conditionTypeProgressing).
					WithStatus(configv1.ConditionTrue).
					WithReason(operatorstatus.ReasonWaitingOnExternal),
			)
		}, defaultNodeTimeout)

		It("sets Progressing=True with WaitingOnExternal reason", func(ctx context.Context) {
			co := &configv1.ClusterOperator{}
			co.SetName("cluster-api")
			Eventually(kWithCtx(ctx).Object(co)).
				WithContext(ctx).
				Should(HaveField("Status.Conditions", SatisfyAll(
					test.HaveCondition(conditionTypeProgressing).
						WithStatus(configv1.ConditionTrue).
						WithReason(operatorstatus.ReasonWaitingOnExternal),
				)))

			// No revisions should be created while waiting
			ca := &operatorv1alpha1.ClusterAPI{}
			ca.SetName("cluster")
			Expect(kWithCtx(ctx).Get(ca)()).To(Succeed())
			Expect(ca.Status.Revisions).To(BeEmpty())
		}, defaultNodeTimeout)

		It("creates revision after Infrastructure gets PlatformStatus", func(ctx context.Context) {
			// Now update Infrastructure with PlatformStatus
			Expect(kWithCtx(ctx).UpdateStatus(infra, func() {
				infraFixtureAddStatus(infra)
			})()).To(Succeed())

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
				test.HaveCondition(conditionTypeProgressing).
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

		// Clone provider images and ensure manifest paths for direct reconcile use.
		providerImgs := make([]providerimages.ProviderImageManifests, len(defaultProviderImgs))
		copy(providerImgs, defaultProviderImgs)
		ensureManifestPaths(providerImgs)

		interceptorCl = interceptor.NewClient(cl, interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*operatorv1alpha1.ClusterAPI); ok && subResourceName == subResourceStatus {
					return testErr
				}

				return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		})

		r = &RevisionController{
			Client:           interceptorCl,
			ProviderProfiles: providerImgs,
			ReleaseVersion:   "4.18.0",
		}
	}, defaultNodeTimeout)

	It("sets Progressing=True with EphemeralError on client status patch error", func(ctx context.Context) {
		req := reconcile.Request{NamespacedName: client.ObjectKey{Name: "cluster"}}
		_, err := r.Reconcile(ctx, req)
		Expect(err).To(HaveOccurred())

		// Verify conditions show ephemeral error
		co := &configv1.ClusterOperator{}
		Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster-api"}, co)).To(Succeed())

		Expect(co.Status.Conditions).To(test.HaveCondition(conditionTypeProgressing).
			WithStatus(configv1.ConditionTrue).
			WithReason(operatorstatus.ReasonEphemeralError).
			WithMessage(ContainSubstring(testErr.Error())))
		Expect(co.Status.Conditions).To(test.HaveCondition(conditionTypeDegraded).
			WithStatus(configv1.ConditionFalse).
			WithReason(operatorstatus.ReasonProgressing))
	}, defaultNodeTimeout)
})

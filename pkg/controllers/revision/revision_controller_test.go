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
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
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

func TestCalculateContentID_Determinism(t *testing.T) {
	g := NewWithT(t)

	providers := []providerimages.ProviderImageManifests{
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "core", InstallOrder: 10}, ContentID: "abc123"},
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "infra", InstallOrder: 20}, ContentID: "def456"},
	}

	// Same providers should produce same hash
	contentID1 := calculateContentID(providers)
	contentID2 := calculateContentID(providers)

	g.Expect(contentID1).To(Equal(contentID2))
	g.Expect(contentID1).ToNot(BeEmpty())
}

func TestCalculateContentID_DifferentOrder(t *testing.T) {
	g := NewWithT(t)

	providers1 := []providerimages.ProviderImageManifests{
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "core", InstallOrder: 10}, ContentID: "abc123"},
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "infra", InstallOrder: 20}, ContentID: "def456"},
	}

	providers2 := []providerimages.ProviderImageManifests{
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "infra", InstallOrder: 20}, ContentID: "def456"},
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "core", InstallOrder: 10}, ContentID: "abc123"},
	}

	// Different order should produce different hash
	contentID1 := calculateContentID(providers1)
	contentID2 := calculateContentID(providers2)

	g.Expect(contentID1).ToNot(Equal(contentID2))
}

func TestFindLatestRevision(t *testing.T) {
	tests := []struct {
		name              string
		revisions         []operatorv1alpha1.ClusterAPIInstallerRevision
		desiredRevision   string
		expectNil         bool
		expectedRevision  int64
		expectedContentID string
	}{
		{
			name:      "returns nil for empty revisions",
			revisions: nil,
			expectNil: true,
		},
		{
			name: "returns single revision",
			revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
				{Name: "rev-1", Revision: 1, ContentID: "content-id-1"},
			},
			desiredRevision:   "rev-1",
			expectedRevision:  1,
			expectedContentID: "content-id-1",
		},
		{
			name: "returns highest revision number when out of order",
			revisions: []operatorv1alpha1.ClusterAPIInstallerRevision{
				{Name: "rev-1", Revision: 1, ContentID: "c1"},
				{Name: "rev-3", Revision: 3, ContentID: "c3"},
				{Name: "rev-2", Revision: 2, ContentID: "c2"},
			},
			desiredRevision:   "rev-3",
			expectedRevision:  3,
			expectedContentID: "c3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			clusterAPI := &operatorv1alpha1.ClusterAPI{}
			if tt.revisions != nil {
				clusterAPI.Status = operatorv1alpha1.ClusterAPIStatus{
					Revisions:       tt.revisions,
					DesiredRevision: operatorv1alpha1.RevisionName(tt.desiredRevision),
				}
			}

			latest := findLatestRevision(clusterAPI)

			if tt.expectNil {
				g.Expect(latest).To(BeNil())
			} else {
				g.Expect(latest).ToNot(BeNil())
				g.Expect(latest.Revision).To(Equal(tt.expectedRevision))
				g.Expect(latest.ContentID).To(Equal(tt.expectedContentID))
			}
		})
	}
}

func TestBuildRevisionName_Format(t *testing.T) {
	g := NewWithT(t)

	r := &RevisionController{}

	name := r.buildRevisionName("4.18.0", "abcdef1234567890", 1)

	g.Expect(name).To(Equal("4.18.0-abcdef12-1"))
}

func TestBuildRevisionName_Truncation(t *testing.T) {
	g := NewWithT(t)

	r := &RevisionController{}

	// Create a very long version string
	longVersion := ""
	for i := 0; i < 300; i++ {
		longVersion += "x"
	}

	name := r.buildRevisionName(longVersion, "abcdef1234567890", 1)

	g.Expect(len(name)).To(BeNumerically("<=", maxRevisionNameLen))
}

func TestBuildComponentList_StableOrdering(t *testing.T) {
	g := NewWithT(t)

	// Providers with same priority should retain original order
	providers := []providerimages.ProviderImageManifests{
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "zebra", InstallOrder: 10, OCPPlatform: configv1.AWSPlatformType}, ContentID: "zebra-content"},
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "alpha", InstallOrder: 10, OCPPlatform: configv1.AWSPlatformType}, ContentID: "alpha-content"},
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "beta", InstallOrder: 10, OCPPlatform: configv1.AWSPlatformType}, ContentID: "beta-content"},
	}

	r := &RevisionController{
		ProviderProfiles: providers,
	}

	components := r.buildComponentList(configv1.AWSPlatformType)

	g.Expect(components).To(Equal([]providerimages.ProviderImageManifests{
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "zebra", InstallOrder: 10, OCPPlatform: configv1.AWSPlatformType}, ContentID: "zebra-content"},
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "beta", InstallOrder: 10, OCPPlatform: configv1.AWSPlatformType}, ContentID: "beta-content"},
		{ProviderMetadata: providerimages.ProviderMetadata{Name: "alpha", InstallOrder: 10, OCPPlatform: configv1.AWSPlatformType}, ContentID: "alpha-content"},
	}))
}

func TestToAPIComponents(t *testing.T) {
	g := NewWithT(t)

	providers := []providerimages.ProviderImageManifests{
		{
			ProviderMetadata: providerimages.ProviderMetadata{Name: "core", InstallOrder: 10},
			ImageRef:         "quay.io/openshift/core@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Profile:          "default",
			ContentID:        "core-content-id",
		},
		{
			ProviderMetadata: providerimages.ProviderMetadata{Name: "infra", InstallOrder: 10},
			ImageRef:         "quay.io/openshift/infra@sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
			Profile:          "default",
			ContentID:        "infra-content-id",
		},
	}

	apiComponents := toAPIComponents(providers)

	g.Expect(apiComponents).To(HaveLen(2))
	g.Expect(string(apiComponents[0].Image.Ref)).To(Equal("quay.io/openshift/core@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"))
	g.Expect(apiComponents[0].Image.Profile).To(Equal("default"))
	g.Expect(string(apiComponents[1].Image.Ref)).To(Equal("quay.io/openshift/infra@sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"))
	g.Expect(apiComponents[1].Image.Profile).To(Equal("default"))
}

// findConditionApplyConfig finds a condition by type in a slice of apply configurations.
func findConditionApplyConfig(conditions []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration, condType configv1.ClusterStatusConditionType) *configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	for _, cond := range conditions {
		if cond.Type != nil && *cond.Type == condType {
			return cond
		}
	}

	return nil
}

//nolint:funlen
func TestBuildConditions(t *testing.T) {
	tests := []struct {
		name                      string
		result                    reconcileResult
		existingConditions        []configv1.ClusterOperatorStatusCondition
		expectedProgressingStatus configv1.ConditionStatus
		expectedProgressingReason string
		expectedProgressingMsg    string // empty means don't check
		expectedDegradedStatus    configv1.ConditionStatus
		expectedDegradedReason    string
	}{
		{
			name:                      "success",
			result:                    reconcileResult{},
			expectedProgressingStatus: configv1.ConditionFalse,
			expectedProgressingReason: conditionReasonSuccess,
			expectedProgressingMsg:    "Revision is current",
			expectedDegradedStatus:    configv1.ConditionFalse,
			expectedDegradedReason:    conditionReasonSuccess,
		},
		{
			name:                      "non-retryable error",
			result:                    reconcileResult{progressingReason: conditionReasonNonRetryableError, error: errMaxRevisionsAllowed},
			expectedProgressingStatus: configv1.ConditionFalse,
			expectedProgressingReason: conditionReasonNonRetryableError,
			expectedDegradedStatus:    configv1.ConditionTrue,
			expectedDegradedReason:    conditionReasonNonRetryableError,
		},
		{
			name:                      "ephemeral error (new)",
			result:                    reconcileResult{progressingReason: conditionReasonEphemeralError, error: errors.New("connection refused")},
			expectedProgressingStatus: configv1.ConditionTrue,
			expectedProgressingReason: conditionReasonEphemeralError,
			expectedDegradedStatus:    configv1.ConditionFalse,
			expectedDegradedReason:    conditionReasonProgressing,
		},
		{
			name:   "ephemeral error (existing)",
			result: reconcileResult{progressingReason: conditionReasonEphemeralError, error: errors.New("connection refused")},
			existingConditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:               conditionTypeProgressing,
					Status:             configv1.ConditionTrue,
					Reason:             conditionReasonEphemeralError,
					Message:            "connection refused",
					LastTransitionTime: metav1.NewTime(time.Now().Add(-3 * time.Minute)),
				},
			},
			expectedProgressingStatus: configv1.ConditionTrue,
			expectedProgressingReason: conditionReasonEphemeralError,
			expectedDegradedStatus:    configv1.ConditionFalse,
			expectedDegradedReason:    conditionReasonProgressing,
		},
		{
			name:                      "waiting on external",
			result:                    reconcileResult{progressingReason: conditionReasonWaitingOnExternal, message: "Infrastructure not found"},
			expectedProgressingStatus: configv1.ConditionTrue,
			expectedProgressingReason: conditionReasonWaitingOnExternal,
			expectedProgressingMsg:    "Infrastructure not found",
			expectedDegradedStatus:    configv1.ConditionFalse,
			expectedDegradedReason:    conditionReasonProgressing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			conditions := buildConditions(tt.result)

			g.Expect(conditions).To(HaveLen(2))

			progressing := findConditionApplyConfig(conditions, conditionTypeProgressing)
			g.Expect(progressing).ToNot(BeNil())
			g.Expect(*progressing.Status).To(Equal(tt.expectedProgressingStatus))
			g.Expect(*progressing.Reason).To(Equal(tt.expectedProgressingReason))

			if tt.expectedProgressingMsg != "" {
				g.Expect(*progressing.Message).To(Equal(tt.expectedProgressingMsg))
			}

			degraded := findConditionApplyConfig(conditions, conditionTypeDegraded)
			g.Expect(degraded).ToNot(BeNil())
			g.Expect(*degraded.Status).To(Equal(tt.expectedDegradedStatus))
			g.Expect(*degraded.Reason).To(Equal(tt.expectedDegradedReason))
		})
	}
}

func TestFindClusterOperatorCondition(t *testing.T) {
	g := NewWithT(t)

	conditions := []configv1.ClusterOperatorStatusCondition{
		{
			Type:   conditionTypeProgressing,
			Status: configv1.ConditionTrue,
			Reason: "Testing",
		},
		{
			Type:   conditionTypeDegraded,
			Status: configv1.ConditionFalse,
			Reason: "Success",
		},
	}

	// Find existing condition
	found := findClusterOperatorCondition(conditions, conditionTypeProgressing)
	g.Expect(found).ToNot(BeNil())
	g.Expect(found.Reason).To(Equal("Testing"))

	// Find non-existing condition
	notFound := findClusterOperatorCondition(conditions, "NonExistent")
	g.Expect(notFound).To(BeNil())
}

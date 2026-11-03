//go:build !e2e

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
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"testing"

	"github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"

	configv1 "github.com/openshift/api/config/v1"
)

// upgradeableFixturePath is the multi-document provider manifests fixture
// the pure fixture helpers are tested against. It is trimmed from a real
// /capi-operator-manifests/default/manifests.yaml extracted from a
// cluster-capi-controllers release image (full clusters.cluster.x-k8s.io
// CRD document plus one stub document), so the parsing path is exercised
// against genuine payload data.
const upgradeableFixturePath = "testdata/upgradeable/manifests.yaml"

func readUpgradeableFixture(t *testing.T) []byte {
	t.Helper()

	manifestsYAML, err := os.ReadFile(upgradeableFixturePath)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", upgradeableFixturePath, err)
	}
	return manifestsYAML
}

// reverseManifestDocuments returns the input manifests with its YAML
// documents in reverse order, to prove CRD selection does not rely on
// document ordering.
func reverseManifestDocuments(t *testing.T, manifestsYAML []byte) []byte {
	t.Helper()

	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(manifestsYAML)))
	var documents [][]byte
	for {
		document, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("failed to split fixture %s into YAML documents: %v", upgradeableFixturePath, err)
		}
		if len(bytes.TrimSpace(document)) == 0 {
			continue
		}
		documents = append(documents, document)
	}
	if len(documents) < 2 {
		t.Fatalf("fixture %s has %d documents, need at least 2 to test document ordering", upgradeableFixturePath, len(documents))
	}

	slices.Reverse(documents)
	return bytes.Join(documents, []byte("\n---\n"))
}

func servedVersions(crd *apiextensionsv1.CustomResourceDefinition) []apiextensionsv1.CustomResourceDefinitionVersion {
	var served []apiextensionsv1.CustomResourceDefinitionVersion
	for _, version := range crd.Spec.Versions {
		if version.Served {
			served = append(served, version)
		}
	}
	return served
}

func specSchemaOf(crd *apiextensionsv1.CustomResourceDefinition, versionName string) *apiextensionsv1.JSONSchemaProps {
	for i := range crd.Spec.Versions {
		version := &crd.Spec.Versions[i]
		if version.Name == versionName && version.Schema != nil {
			return version.Schema.OpenAPIV3Schema
		}
	}
	return nil
}

// TestClustersCRDFromManifests exercises the pure multi-doc YAML to Clusters
// CRD selection used by the upgradeability preflight e2e test. Run without a
// cluster via:
//
//	go test -run TestClustersCRDFromManifests ./e2e/...
func TestClustersCRDFromManifests(t *testing.T) {
	t.Run("returns the Clusters CRD from the checked-in fixture", func(t *testing.T) {
		g := gomega.NewWithT(t)

		crd, err := clustersCRDFromManifests(readUpgradeableFixture(t))
		g.Expect(err).NotTo(gomega.HaveOccurred(),
			"fixture %s should yield the Clusters CRD", upgradeableFixturePath)
		g.Expect(crd.Name).To(gomega.Equal(clusterCRDName),
			"fixture %s should select %s, got %s", upgradeableFixturePath, clusterCRDName, crd.Name)

		// The e2e test removes availabilityGates from both served versions,
		// so the fixture must provide it in both.
		for _, version := range servedVersions(crd) {
			specSchema := specSchemaOf(crd, version.Name)
			g.Expect(specSchema).NotTo(gomega.BeNil(),
				"served version %s of fixture %s should have an OpenAPI schema", version.Name, upgradeableFixturePath)
			g.Expect(specSchema.Properties["spec"].Properties).To(gomega.HaveKey(availabilityGates),
				"served version %s of fixture %s should have spec.properties.%s", version.Name, upgradeableFixturePath, availabilityGates)
		}
	})

	t.Run("does not depend on document order", func(t *testing.T) {
		g := gomega.NewWithT(t)

		reordered := reverseManifestDocuments(t, readUpgradeableFixture(t))
		crd, err := clustersCRDFromManifests(reordered)
		g.Expect(err).NotTo(gomega.HaveOccurred(),
			"the Clusters CRD should be selected from the documents of fixture %s regardless of order", upgradeableFixturePath)
		g.Expect(crd.Name).To(gomega.Equal(clusterCRDName),
			"reordered fixture %s should still select %s, got %s", upgradeableFixturePath, clusterCRDName, crd.Name)
	})

	t.Run("errors when the Clusters CRD is absent", func(t *testing.T) {
		g := gomega.NewWithT(t)

		manifestsWithoutClustersCRD := []byte(`apiVersion: v1
kind: Namespace
metadata:
  name: openshift-cluster-api
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: machinedeployments.cluster.x-k8s.io
spec:
  group: cluster.x-k8s.io
  names:
    kind: MachineDeployment
    plural: machinedeployments
  scope: Namespaced
  versions:
  - name: v1beta1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
`)

		_, err := clustersCRDFromManifests(manifestsWithoutClustersCRD)
		g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(clusterCRDName)),
			"manifests without the Clusters CRD should produce an error naming the missing CRD %s", clusterCRDName)
	})
}

// TestNextMinorVersion exercises the pure version arithmetic that maps the
// current OCP version to the fake next-minor target X.(Y+1).0. Run without a
// cluster via:
//
//	go test -run TestNextMinorVersion ./e2e/...
func TestNextMinorVersion(t *testing.T) {
	t.Run("maps X.Y.Z[-prerelease] to X.(Y+1).0", func(t *testing.T) {
		g := gomega.NewWithT(t)

		for _, tc := range []struct {
			current  string
			expected string
		}{
			{current: "5.0.0-0.nightly-2026-10-30-023658", expected: "5.1.0"},
			{current: "4.19.0-ec.4", expected: "4.20.0"},
			{current: "4.18.2", expected: "4.19.0"},
			{current: "5.0.0", expected: "5.1.0"},
			{current: "4.18.0-0.nightly-2026-01-01-000000", expected: "4.19.0"},
		} {
			target, err := nextMinorVersion(tc.current)
			g.Expect(err).NotTo(gomega.HaveOccurred(),
				"version %q should parse", tc.current)
			g.Expect(target).To(gomega.Equal(tc.expected),
				"current version %q should map to target %q", tc.current, tc.expected)
		}
	})

	t.Run("errors on unparseable input", func(t *testing.T) {
		g := gomega.NewWithT(t)

		for _, version := range []string{
			"",
			"garbage",
			"5",
			"5.1",
			"5.x.0",
			"x.y.z",
		} {
			_, err := nextMinorVersion(version)
			g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(fmt.Sprintf("%q", version))),
				"garbage input %q should produce an error naming the input", version)
		}
	})
}

// TestRemoveSpecPropertyFromAllVersions exercises the CRD schema helper the
// upgradeability preflight e2e test uses to produce an incompatible Clusters
// CRD. Run without a cluster via:
//
//	go test -run TestRemoveSpecPropertyFromAllVersions ./e2e/...
func TestRemoveSpecPropertyFromAllVersions(t *testing.T) {
	t.Run("removes the property from every served version", func(t *testing.T) {
		g := gomega.NewWithT(t)

		crd, err := clustersCRDFromManifests(readUpgradeableFixture(t))
		g.Expect(err).NotTo(gomega.HaveOccurred(),
			"fixture %s should yield the Clusters CRD", upgradeableFixturePath)

		removeSpecPropertyFromAllVersions(crd, availabilityGates)

		for _, version := range servedVersions(crd) {
			specSchema := specSchemaOf(crd, version.Name)
			g.Expect(specSchema).NotTo(gomega.BeNil(),
				"served version %s should still have an OpenAPI schema after removing spec.properties.%s", version.Name, availabilityGates)
			g.Expect(specSchema.Properties["spec"].Properties).NotTo(gomega.HaveKey(availabilityGates),
				"served version %s should not have spec.properties.%s after removal", version.Name, availabilityGates)
			g.Expect(specSchema.Properties["spec"].Properties).To(gomega.HaveKey("paused"),
				"served version %s should keep unrelated spec properties when removing spec.properties.%s", version.Name, availabilityGates)
		}
	})

	t.Run("is idempotent when the property is already absent", func(t *testing.T) {
		g := gomega.NewWithT(t)

		crd, err := clustersCRDFromManifests(readUpgradeableFixture(t))
		g.Expect(err).NotTo(gomega.HaveOccurred(),
			"fixture %s should yield the Clusters CRD", upgradeableFixturePath)

		removeSpecPropertyFromAllVersions(crd, availabilityGates)
		removeSpecPropertyFromAllVersions(crd, availabilityGates)

		for _, version := range servedVersions(crd) {
			specSchema := specSchemaOf(crd, version.Name)
			g.Expect(specSchema).NotTo(gomega.BeNil(),
				"served version %s should still have an OpenAPI schema after the second removal call", version.Name)
			g.Expect(specSchema.Properties["spec"].Properties).NotTo(gomega.HaveKey(availabilityGates),
				"served version %s should still not have spec.properties.%s after the second removal call", version.Name, availabilityGates)
		}
	})
}

// TestTargetUpdateOffered exercises the pure update-offer matching the
// upgradeability preflight e2e waits on. Both status.availableUpdates and
// status.conditionalUpdates must count as offered; a version match alone
// must not. Run without a cluster via:
//
//	go test -run TestTargetUpdateOffered ./e2e/...
func TestTargetUpdateOffered(t *testing.T) {
	const (
		targetVersion = "5.2.0"
		targetImage   = "registry.example/repo@sha256:target"
	)

	// demotedTarget mirrors the conditional update entry the CVO writes on
	// clusters where update risks apply (e.g. TechPreviewNoUpgrade feature
	// sets): the target is demoted out of availableUpdates but stays offered
	// with Recommended=False and the blocking risks listed.
	demotedTarget := configv1.ConditionalUpdate{
		Release: configv1.Release{Version: targetVersion, Image: targetImage},
		Risks: []configv1.ConditionalUpdateRisk{
			{Name: "ClusterOperatorUpgradeable-config-operator"},
			{Name: "ClusterVersionOverrides"},
		},
		Conditions: []metav1.Condition{{
			Type:   recommendedConditionType,
			Status: metav1.ConditionFalse,
			Reason: "ClusterOperatorUpgradeable-config-operator",
		}},
	}

	for _, tc := range []struct {
		name     string
		expected bool
		cv       *configv1.ClusterVersion
	}{
		{
			name:     "offered unconditionally in availableUpdates",
			expected: true,
			cv: &configv1.ClusterVersion{Status: configv1.ClusterVersionStatus{
				AvailableUpdates: []configv1.Release{{Version: targetVersion, Image: targetImage}},
			}},
		},
		{
			name:     "offered only in conditionalUpdates with Recommended=False",
			expected: true,
			cv: &configv1.ClusterVersion{Status: configv1.ClusterVersionStatus{
				ConditionalUpdates: []configv1.ConditionalUpdate{demotedTarget},
			}},
		},
		{
			name:     "offered in both lists",
			expected: true,
			cv: &configv1.ClusterVersion{Status: configv1.ClusterVersionStatus{
				AvailableUpdates:   []configv1.Release{{Version: targetVersion, Image: targetImage}},
				ConditionalUpdates: []configv1.ConditionalUpdate{demotedTarget},
			}},
		},
		{
			name:     "not offered",
			expected: false,
			cv: &configv1.ClusterVersion{Status: configv1.ClusterVersionStatus{
				AvailableUpdates: []configv1.Release{{Version: "5.1.1", Image: "registry.example/repo@sha256:other"}},
			}},
		},
		{
			name:     "matching version with a different image does not count",
			expected: false,
			cv: &configv1.ClusterVersion{Status: configv1.ClusterVersionStatus{
				AvailableUpdates:   []configv1.Release{{Version: targetVersion, Image: "registry.example/repo@sha256:other"}},
				ConditionalUpdates: []configv1.ConditionalUpdate{{Release: configv1.Release{Version: targetVersion, Image: "registry.example/repo@sha256:other"}}},
			}},
		},
		{
			name:     "matching image with a different version does not count",
			expected: false,
			cv: &configv1.ClusterVersion{Status: configv1.ClusterVersionStatus{
				AvailableUpdates:   []configv1.Release{{Version: "5.1.1", Image: targetImage}},
				ConditionalUpdates: []configv1.ConditionalUpdate{{Release: configv1.Release{Version: "5.1.1", Image: targetImage}}},
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := gomega.NewWithT(t)

			g.Expect(targetUpdateOffered(tc.cv, targetVersion, targetImage)).To(gomega.Equal(tc.expected))
		})
	}
}

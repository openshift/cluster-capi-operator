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

package revisiongenerator

import (
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
)

// Reusable YAML manifest fixtures.
const (
	configMapA = `apiVersion: v1
kind: ConfigMap
metadata:
  name: config-a
  namespace: default
data:
  key: value-a`

	configMapB = `apiVersion: v1
kind: ConfigMap
metadata:
  name: config-b
  namespace: default
data:
  key: value-b`

	configMapAModified = `apiVersion: v1
kind: ConfigMap
metadata:
  name: config-a
  namespace: default
data:
  key: value-modified`

	crdA = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    plural: widgets
    singular: widget
    kind: Widget
  scope: Namespaced`

	crdB = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gadgets.example.com
spec:
  group: example.com
  names:
    plural: gadgets
    singular: gadget
    kind: Gadget
  scope: Cluster`
)

func TestBuildRevisionName(t *testing.T) {
	for _, tc := range []struct {
		name           string
		releaseVersion string
		contentID      string
		index          int64
		wantName       string
	}{
		{
			name:           "normal case",
			releaseVersion: "4.18.0",
			contentID:      "abcdef1234567890",
			index:          1,
			wantName:       "4.18.0-abcdef12-1",
		},
		{
			name:           "short contentID is not padded",
			releaseVersion: "4.18.0",
			contentID:      "abcd",
			index:          1,
			wantName:       "4.18.0-abcd-1",
		},
		{
			name:           "version truncated to fit maxRevisionNameLen",
			releaseVersion: strings.Repeat("v", 300),
			contentID:      "abcdef1234567890",
			index:          1,
			wantName:       strings.Repeat("v", 255-len("-abcdef12-1")) + "-abcdef12-1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			name := buildRevisionName(tc.releaseVersion, tc.contentID, tc.index)

			g.Expect(string(name)).To(Equal(tc.wantName))
			g.Expect(len(name)).To(BeNumerically("<=", maxRevisionNameLen))
		})
	}
}

func TestContentID(t *testing.T) {
	for _, tc := range []struct {
		name      string
		profilesA []providerimages.ProviderImageManifests
		profilesB []providerimages.ProviderImageManifests
		wantEqual bool
	}{
		{
			name:      "identical profiles produce same contentID",
			profilesA: []providerimages.ProviderImageManifests{profile(t, "p1", "img1", "default", configMapA)},
			profilesB: []providerimages.ProviderImageManifests{profile(t, "p1", "img1", "default", configMapA)},
			wantEqual: true,
		},
		{
			name:      "modifying an object changes contentID",
			profilesA: []providerimages.ProviderImageManifests{profile(t, "p1", "img1", "default", configMapA)},
			profilesB: []providerimages.ProviderImageManifests{profile(t, "p1", "img1", "default", configMapAModified)},
			wantEqual: false,
		},
		{
			name:      "changing order of objects within a component changes contentID",
			profilesA: []providerimages.ProviderImageManifests{profile(t, "p1", "img1", "default", multiDoc(configMapA, configMapB))},
			profilesB: []providerimages.ProviderImageManifests{profile(t, "p1", "img1", "default", multiDoc(configMapB, configMapA))},
			wantEqual: false,
		},
		{
			name: "changing order of components changes contentID",
			profilesA: []providerimages.ProviderImageManifests{
				profile(t, "pa", "imgA", "default", configMapA),
				profile(t, "pb", "imgB", "default", configMapB),
			},
			profilesB: []providerimages.ProviderImageManifests{
				profile(t, "pb", "imgB", "default", configMapB),
				profile(t, "pa", "imgA", "default", configMapA),
			},
			wantEqual: false,
		},
		{
			name: "adding a component changes contentID",
			profilesA: []providerimages.ProviderImageManifests{
				profile(t, "pa", "imgA", "default", configMapA),
			},
			profilesB: []providerimages.ProviderImageManifests{
				profile(t, "pa", "imgA", "default", configMapA),
				profile(t, "pb", "imgB", "default", configMapB),
			},
			wantEqual: false,
		},
		{
			name:      "adding an object to a component changes contentID",
			profilesA: []providerimages.ProviderImageManifests{profile(t, "p1", "img1", "default", configMapA)},
			profilesB: []providerimages.ProviderImageManifests{profile(t, "p1", "img1", "default", multiDoc(configMapA, configMapB))},
			wantEqual: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			idA := contentIDForProfiles(g, tc.profilesA...)
			idB := contentIDForProfiles(g, tc.profilesB...)

			if tc.wantEqual {
				g.Expect(idA).To(Equal(idB))
			} else {
				g.Expect(idA).NotTo(Equal(idB))
			}
		})
	}

	t.Run("contentID is deterministic across calls", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "p1", "img1", "default", configMapA),
		}))(g)

		id1 := must(rev.ContentID())(g)
		id2 := must(rev.ContentID())(g)

		g.Expect(id1).To(Equal(id2))
	})

	t.Run("empty manifests", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "p1", "img1", "default", ""),
		}))(g)

		id := must(rev.ContentID())(g)
		g.Expect(id).NotTo(BeEmpty())
	})
}

func TestForInstall(t *testing.T) {
	t.Run("returns installer revision with correct name and index", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "core", "img1", "default", configMapA),
		}))(g)

		installer := forInstall(g, rev, "4.18.0", 1)

		contentID := must(rev.ContentID())(g)

		expectedName := fmt.Sprintf("4.18.0-%s-1", contentID[:revisionContentIDLen])
		g.Expect(string(installer.RevisionName())).To(Equal(expectedName))
		g.Expect(installer.RevisionIndex()).To(Equal(int64(1)))
	})

	t.Run("components accessible through installer revision", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "core", "img1", "default", multiDoc(crdA, configMapA)),
		}))(g)

		installer := forInstall(g, rev, "4.18.0", 1)

		components := installer.Components()
		g.Expect(components).To(HaveLen(1))
		g.Expect(components[0].CRDs()).To(HaveLen(1))
		g.Expect(components[0].Objects()).To(HaveLen(1))
	})

	t.Run("name truncation with long version", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "core", "img1", "default", configMapA),
		}))(g)

		longVersion := strings.Repeat("x", 300)
		installer := forInstall(g, rev, longVersion, 1)

		g.Expect(len(installer.RevisionName())).To(BeNumerically("<=", maxRevisionNameLen))
	})
}

func TestToAPIRevision(t *testing.T) {
	t.Run("single component fields", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "core", "quay.io/openshift/core@sha256:abcd", "default", configMapA),
		}))(g)

		apiRev := must(forInstall(g, rev, "4.18.0", 1).ToAPIRevision())(g)

		g.Expect(apiRev.Revision).To(Equal(int64(1)))
		g.Expect(apiRev.ContentID).NotTo(BeEmpty())
		g.Expect(apiRev.Components).To(HaveLen(1))
		g.Expect(apiRev.Components[0].Type).To(Equal(operatorv1alpha1.InstallerComponentTypeImage))
		g.Expect(string(apiRev.Components[0].Image.Ref)).To(Equal("quay.io/openshift/core@sha256:abcd"))
		g.Expect(apiRev.Components[0].Image.Profile).To(Equal("default"))
	})

	t.Run("multiple components preserve order", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "core", "img-core", "default", configMapA),
			profile(t, "infra", "img-infra", "aws", configMapB),
		}))(g)

		apiRev := must(forInstall(g, rev, "4.18.0", 1).ToAPIRevision())(g)

		g.Expect(apiRev.Components).To(HaveLen(2))
		g.Expect(string(apiRev.Components[0].Image.Ref)).To(Equal("img-core"))
		g.Expect(apiRev.Components[0].Image.Profile).To(Equal("default"))
		g.Expect(string(apiRev.Components[1].Image.Ref)).To(Equal("img-infra"))
		g.Expect(apiRev.Components[1].Image.Profile).To(Equal("aws"))
	})

	t.Run("name format", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "core", "img1", "default", configMapA),
		}))(g)

		apiRev := must(forInstall(g, rev, "4.18.0", 1).ToAPIRevision())(g)

		contentID := apiRev.ContentID
		expectedName := fmt.Sprintf("4.18.0-%s-1", contentID[:revisionContentIDLen])
		g.Expect(string(apiRev.Name)).To(Equal(expectedName))
	})

	t.Run("contentID matches standalone call", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "core", "img1", "default", configMapA),
		}))(g)

		standaloneID := must(rev.ContentID())(g)

		apiRev := must(forInstall(g, rev, "4.18.0", 1).ToAPIRevision())(g)

		g.Expect(apiRev.ContentID).To(Equal(standaloneID))
	})

	t.Run("zero components", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{}))(g)

		apiRev := must(forInstall(g, rev, "4.18.0", 1).ToAPIRevision())(g)

		g.Expect(apiRev.Components).To(HaveLen(0))
		g.Expect(apiRev.ContentID).NotTo(BeEmpty())
	})
}

func TestNewRenderedRevision(t *testing.T) {
	t.Run("error from nonexistent manifest path", func(t *testing.T) {
		g := NewWithT(t)

		_, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: "/nonexistent/path/manifests.yaml"},
		})
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("error from invalid yaml", func(t *testing.T) {
		g := NewWithT(t)

		_, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "p1", "img1", "default", "not: valid: yaml: ["),
		})
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("yaml transformation applied during construction", func(t *testing.T) {
		g := NewWithT(t)

		// Manifest with envsubst variable; revision built from this should
		// produce the same contentID as one built from the expanded form.
		pathWithVar := writeManifestFile(t, `apiVersion: v1
kind: ConfigMap
metadata:
  name: cm
data:
  v: "${EXP_BOOTSTRAP_FORMAT_IGNITION}"`)
		pathExpanded := writeManifestFile(t, `apiVersion: v1
kind: ConfigMap
metadata:
  name: cm
data:
  v: "true"`)

		p := providerimages.ProviderImageManifests{
			ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default",
		}

		p.ManifestsPath = pathWithVar
		rev1 := must(NewRenderedRevision([]providerimages.ProviderImageManifests{p}))(g)

		p.ManifestsPath = pathExpanded
		rev2 := must(NewRenderedRevision([]providerimages.ProviderImageManifests{p}))(g)

		id1 := must(rev1.ContentID())(g)
		id2 := must(rev2.ContentID())(g)

		g.Expect(id1).To(Equal(id2))
	})
}

func TestComponents(t *testing.T) {
	t.Run("returns correct component count and names", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "core", "img-core", "default", configMapA),
			profile(t, "infra", "img-infra", "aws", configMapB),
		}))(g)

		components := rev.Components()
		g.Expect(components).To(HaveLen(2))
		g.Expect(components[0].Name()).To(Equal("core"))
		g.Expect(components[1].Name()).To(Equal("infra"))
	})

	for _, tc := range []struct {
		name        string
		manifests   string
		wantCRDs    int
		wantObjects int
	}{
		{
			name:        "separates CRDs from other objects",
			manifests:   multiDoc(crdA, configMapA, crdB, configMapB),
			wantCRDs:    2,
			wantObjects: 2,
		},
		{
			name:        "component with only CRDs has empty Objects",
			manifests:   crdA,
			wantCRDs:    1,
			wantObjects: 0,
		},
		{
			name:        "component with only objects has empty CRDs",
			manifests:   configMapA,
			wantCRDs:    0,
			wantObjects: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
				profile(t, "core", "img1", "default", tc.manifests),
			}))(g)

			c := rev.Components()[0]
			g.Expect(c.CRDs()).To(HaveLen(tc.wantCRDs))
			g.Expect(c.Objects()).To(HaveLen(tc.wantObjects))
		})
	}

	t.Run("CRDs returns client.Object slices matching underlying objects", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "core", "img1", "default", crdA),
		}))(g)

		crds := rev.Components()[0].CRDs()
		g.Expect(crds).To(HaveLen(1))
		g.Expect(crds[0].GetName()).To(Equal("widgets.example.com"))
		g.Expect(crds[0].GetObjectKind().GroupVersionKind().Kind).To(Equal("CustomResourceDefinition"))
	})

	t.Run("Objects returns client.Object slices matching underlying objects", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{
			profile(t, "core", "img1", "default", configMapA),
		}))(g)

		objs := rev.Components()[0].Objects()
		g.Expect(objs).To(HaveLen(1))
		g.Expect(objs[0].GetName()).To(Equal("config-a"))
		g.Expect(objs[0].GetObjectKind().GroupVersionKind().Kind).To(Equal("ConfigMap"))
	})

	t.Run("zero components returns empty slice", func(t *testing.T) {
		g := NewWithT(t)

		rev := must(NewRenderedRevision([]providerimages.ProviderImageManifests{}))(g)

		g.Expect(rev.Components()).To(BeEmpty())
	})
}

func TestNewInstallerRevisionFromAPI(t *testing.T) {
	makeProfiles := func(t *testing.T) []providerimages.ProviderImageManifests {
		t.Helper()

		return []providerimages.ProviderImageManifests{
			profile(t, "core", "quay.io/openshift/core@sha256:aaaa", "default", configMapA),
			profile(t, "aws", "quay.io/openshift/aws@sha256:bbbb", "aws", configMapB),
			profile(t, "azure", "quay.io/openshift/azure@sha256:cccc", "azure", multiDoc(crdA, configMapA)),
		}
	}

	t.Run("happy path returns installer revision with correct components", func(t *testing.T) {
		g := NewWithT(t)

		profiles := makeProfiles(t)
		apiRev := operatorv1alpha1.ClusterAPIInstallerRevision{
			ContentID: contentIDForProfiles(g, profiles[0], profiles[1]),
			Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
				{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref: "quay.io/openshift/core@sha256:aaaa", Profile: "default",
				}},
				{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref: "quay.io/openshift/aws@sha256:bbbb", Profile: "aws",
				}},
			},
		}

		rev := must(NewInstallerRevisionFromAPI(apiRev, profiles))(g)

		components := rev.Components()
		g.Expect(components).To(HaveLen(2))
		g.Expect(components[0].Name()).To(Equal("core"))
		g.Expect(components[1].Name()).To(Equal("aws"))
	})

	t.Run("preserves revision name and index from API revision", func(t *testing.T) {
		g := NewWithT(t)

		profiles := makeProfiles(t)
		apiRev := operatorv1alpha1.ClusterAPIInstallerRevision{
			Name:      "4.18.0-abcd1234-3",
			Revision:  3,
			ContentID: contentIDForProfiles(g, profiles[0]),
			Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
				{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref: "quay.io/openshift/core@sha256:aaaa", Profile: "default",
				}},
			},
		}

		rev := must(NewInstallerRevisionFromAPI(apiRev, profiles))(g)

		g.Expect(rev.RevisionName()).To(Equal(operatorv1alpha1.RevisionName("4.18.0-abcd1234-3")))
		g.Expect(rev.RevisionIndex()).To(Equal(int64(3)))
	})

	t.Run("preserves component ordering", func(t *testing.T) {
		g := NewWithT(t)

		profiles := makeProfiles(t)
		apiRev := operatorv1alpha1.ClusterAPIInstallerRevision{
			ContentID: contentIDForProfiles(g, profiles[2], profiles[0]),
			Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
				{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref: "quay.io/openshift/azure@sha256:cccc", Profile: "azure",
				}},
				{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref: "quay.io/openshift/core@sha256:aaaa", Profile: "default",
				}},
			},
		}

		rev := must(NewInstallerRevisionFromAPI(apiRev, profiles))(g)

		components := rev.Components()
		g.Expect(components).To(HaveLen(2))
		g.Expect(components[0].Name()).To(Equal("azure"))
		g.Expect(components[1].Name()).To(Equal("core"))
	})

	t.Run("renders CRDs and objects from matched profiles", func(t *testing.T) {
		g := NewWithT(t)

		profiles := makeProfiles(t)
		apiRev := operatorv1alpha1.ClusterAPIInstallerRevision{
			ContentID: contentIDForProfiles(g, profiles[2]),
			Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
				{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref: "quay.io/openshift/azure@sha256:cccc", Profile: "azure",
				}},
			},
		}

		rev := must(NewInstallerRevisionFromAPI(apiRev, profiles))(g)

		c := rev.Components()[0]
		g.Expect(c.CRDs()).To(HaveLen(1))
		g.Expect(c.Objects()).To(HaveLen(1))
	})

	t.Run("returns error for missing component", func(t *testing.T) {
		g := NewWithT(t)

		apiRev := operatorv1alpha1.ClusterAPIInstallerRevision{
			Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
				{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref: "quay.io/openshift/nonexistent@sha256:ffff", Profile: "default",
				}},
			},
		}

		_, err := NewInstallerRevisionFromAPI(apiRev, makeProfiles(t))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("no provider profile found"))
		g.Expect(err.Error()).To(ContainSubstring("nonexistent"))
	})

	t.Run("returns error when image ref matches but profile does not", func(t *testing.T) {
		g := NewWithT(t)

		apiRev := operatorv1alpha1.ClusterAPIInstallerRevision{
			Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
				{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref: "quay.io/openshift/core@sha256:aaaa", Profile: "wrong-profile",
				}},
			},
		}

		_, err := NewInstallerRevisionFromAPI(apiRev, makeProfiles(t))
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("empty components succeeds with empty revision", func(t *testing.T) {
		g := NewWithT(t)

		apiRev := operatorv1alpha1.ClusterAPIInstallerRevision{
			ContentID:  contentIDForProfiles(g),
			Components: []operatorv1alpha1.ClusterAPIInstallerComponent{},
		}

		rev := must(NewInstallerRevisionFromAPI(apiRev, makeProfiles(t)))(g)
		g.Expect(rev.Components()).To(BeEmpty())
	})

	t.Run("succeeds when contentID matches rendered content", func(t *testing.T) {
		g := NewWithT(t)

		profiles := makeProfiles(t)
		expectedContentID := contentIDForProfiles(g, profiles[0], profiles[1])

		apiRev := operatorv1alpha1.ClusterAPIInstallerRevision{
			ContentID: expectedContentID,
			Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
				{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref: "quay.io/openshift/core@sha256:aaaa", Profile: "default",
				}},
				{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref: "quay.io/openshift/aws@sha256:bbbb", Profile: "aws",
				}},
			},
		}

		rev := must(NewInstallerRevisionFromAPI(apiRev, profiles))(g)
		g.Expect(rev.Components()).To(HaveLen(2))
	})

	t.Run("returns error when contentID does not match rendered content", func(t *testing.T) {
		g := NewWithT(t)

		apiRev := operatorv1alpha1.ClusterAPIInstallerRevision{
			ContentID: "does-not-match-any-rendered-content",
			Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
				{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
					Ref: "quay.io/openshift/core@sha256:aaaa", Profile: "default",
				}},
			},
		}

		_, err := NewInstallerRevisionFromAPI(apiRev, makeProfiles(t))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("content ID mismatch"))
	})
}

func TestRevisionsToApplyConfig(t *testing.T) {
	t.Run("single revision preserves all fields", func(t *testing.T) {
		g := NewWithT(t)

		revs := []operatorv1alpha1.ClusterAPIInstallerRevision{
			{
				Name:      "4.18.0-abcd1234-1",
				Revision:  1,
				ContentID: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
				Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
					{
						Type: operatorv1alpha1.InstallerComponentTypeImage,
						Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
							Ref:     "quay.io/openshift/core@sha256:aaaa",
							Profile: "default",
						},
					},
				},
			},
		}

		acs, err := RevisionsToApplyConfig(revs)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(acs).To(HaveLen(1))

		ac := acs[0]
		g.Expect(ac.Name).NotTo(BeNil())
		g.Expect(*ac.Name).To(Equal(operatorv1alpha1.RevisionName("4.18.0-abcd1234-1")))
		g.Expect(ac.Revision).NotTo(BeNil())
		g.Expect(*ac.Revision).To(Equal(int64(1)))
		g.Expect(ac.ContentID).NotTo(BeNil())
		g.Expect(*ac.ContentID).To(Equal("abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"))

		g.Expect(ac.Components).To(HaveLen(1))
		g.Expect(ac.Components[0].Type).NotTo(BeNil())
		g.Expect(*ac.Components[0].Type).To(Equal(operatorv1alpha1.InstallerComponentTypeImage))
		g.Expect(ac.Components[0].Image).NotTo(BeNil())
		g.Expect(*ac.Components[0].Image.Ref).To(Equal(operatorv1alpha1.ImageDigestFormat("quay.io/openshift/core@sha256:aaaa")))
		g.Expect(*ac.Components[0].Image.Profile).To(Equal("default"))
	})

	t.Run("multiple revisions preserve ordering and fields", func(t *testing.T) {
		g := NewWithT(t)

		revs := []operatorv1alpha1.ClusterAPIInstallerRevision{
			{
				Name:      "4.18.0-aaaa1111-1",
				Revision:  1,
				ContentID: "id1",
				Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
					{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
						Ref: "img-a", Profile: "default",
					}},
				},
			},
			{
				Name:      "4.18.0-bbbb2222-2",
				Revision:  2,
				ContentID: "id2",
				Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
					{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
						Ref: "img-b", Profile: "aws",
					}},
					{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
						Ref: "img-c", Profile: "default",
					}},
				},
			},
		}

		acs, err := RevisionsToApplyConfig(revs)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(acs).To(HaveLen(2))

		g.Expect(*acs[0].Name).To(Equal(operatorv1alpha1.RevisionName("4.18.0-aaaa1111-1")))
		g.Expect(*acs[0].Revision).To(Equal(int64(1)))
		g.Expect(acs[0].Components).To(HaveLen(1))

		g.Expect(*acs[1].Name).To(Equal(operatorv1alpha1.RevisionName("4.18.0-bbbb2222-2")))
		g.Expect(*acs[1].Revision).To(Equal(int64(2)))
		g.Expect(acs[1].Components).To(HaveLen(2))
	})

	t.Run("empty slice returns empty slice", func(t *testing.T) {
		g := NewWithT(t)

		acs, err := RevisionsToApplyConfig([]operatorv1alpha1.ClusterAPIInstallerRevision{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(acs).To(BeEmpty())
	})

	t.Run("unmanagedCustomResourceDefinitions preserved", func(t *testing.T) {
		g := NewWithT(t)

		revs := []operatorv1alpha1.ClusterAPIInstallerRevision{
			{
				Name:                               "4.18.0-aaaa1111-1",
				Revision:                           1,
				ContentID:                          "id1",
				UnmanagedCustomResourceDefinitions: []string{"widgets.example.com", "gadgets.example.com"},
				Components: []operatorv1alpha1.ClusterAPIInstallerComponent{
					{Type: operatorv1alpha1.InstallerComponentTypeImage, Image: operatorv1alpha1.ClusterAPIInstallerComponentImage{
						Ref: "img-a", Profile: "default",
					}},
				},
			},
		}

		acs, err := RevisionsToApplyConfig(revs)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(acs).To(HaveLen(1))
		g.Expect(acs[0].UnmanagedCustomResourceDefinitions).To(Equal([]string{"widgets.example.com", "gadgets.example.com"}))
	})
}

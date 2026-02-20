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
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
)

// writeManifestFile writes YAML content to a temp file and returns the path.
func writeManifestFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifests.yaml")
	g := NewWithT(t)
	g.Expect(os.WriteFile(path, []byte(content), 0644)).To(Succeed())
	return path
}

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
)

// multiDoc joins YAML documents with the standard separator.
func multiDoc(docs ...string) string {
	return strings.Join(docs, "\n---\n")
}

func TestContentID(t *testing.T) {
	t.Run("identical profiles produce same contentID", func(t *testing.T) {
		g := NewWithT(t)

		// Two separate but content-identical manifest files.
		path1 := writeManifestFile(t, configMapA)
		path2 := writeManifestFile(t, configMapA)

		rev1, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: path1},
		})
		g.Expect(err).NotTo(HaveOccurred())

		rev2, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: path2},
		})
		g.Expect(err).NotTo(HaveOccurred())

		id1, err := rev1.ContentID()
		g.Expect(err).NotTo(HaveOccurred())
		id2, err := rev2.ContentID()
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(id1).To(Equal(id2))
	})

	t.Run("contentID is deterministic across calls", func(t *testing.T) {
		g := NewWithT(t)

		path := writeManifestFile(t, configMapA)
		rev, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: path},
		})
		g.Expect(err).NotTo(HaveOccurred())

		id1, err := rev.ContentID()
		g.Expect(err).NotTo(HaveOccurred())
		id2, err := rev.ContentID()
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(id1).To(Equal(id2))
	})

	t.Run("modifying an object changes contentID", func(t *testing.T) {
		g := NewWithT(t)

		pathOriginal := writeManifestFile(t, configMapA)
		pathModified := writeManifestFile(t, configMapAModified)

		rev1, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: pathOriginal},
		})
		g.Expect(err).NotTo(HaveOccurred())

		rev2, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: pathModified},
		})
		g.Expect(err).NotTo(HaveOccurred())

		id1, err := rev1.ContentID()
		g.Expect(err).NotTo(HaveOccurred())
		id2, err := rev2.ContentID()
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(id1).NotTo(Equal(id2))
	})

	t.Run("changing order of objects within a component changes contentID", func(t *testing.T) {
		g := NewWithT(t)

		pathAB := writeManifestFile(t, multiDoc(configMapA, configMapB))
		pathBA := writeManifestFile(t, multiDoc(configMapB, configMapA))

		rev1, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: pathAB},
		})
		g.Expect(err).NotTo(HaveOccurred())

		rev2, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: pathBA},
		})
		g.Expect(err).NotTo(HaveOccurred())

		id1, err := rev1.ContentID()
		g.Expect(err).NotTo(HaveOccurred())
		id2, err := rev2.ContentID()
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(id1).NotTo(Equal(id2))
	})

	t.Run("changing order of components changes contentID", func(t *testing.T) {
		g := NewWithT(t)

		pathA := writeManifestFile(t, configMapA)
		pathB := writeManifestFile(t, configMapB)

		profileA := providerimages.ProviderImageManifests{
			ProviderMetadata: providerimages.ProviderMetadata{Name: "pa"}, ImageRef: "imgA", Profile: "default", ManifestsPath: pathA,
		}
		profileB := providerimages.ProviderImageManifests{
			ProviderMetadata: providerimages.ProviderMetadata{Name: "pb"}, ImageRef: "imgB", Profile: "default", ManifestsPath: pathB,
		}

		rev1, err := NewRenderedRevision([]providerimages.ProviderImageManifests{profileA, profileB})
		g.Expect(err).NotTo(HaveOccurred())

		rev2, err := NewRenderedRevision([]providerimages.ProviderImageManifests{profileB, profileA})
		g.Expect(err).NotTo(HaveOccurred())

		id1, err := rev1.ContentID()
		g.Expect(err).NotTo(HaveOccurred())
		id2, err := rev2.ContentID()
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(id1).NotTo(Equal(id2))
	})

	t.Run("adding a component changes contentID", func(t *testing.T) {
		g := NewWithT(t)

		pathA := writeManifestFile(t, configMapA)
		pathB := writeManifestFile(t, configMapB)

		profileA := providerimages.ProviderImageManifests{
			ProviderMetadata: providerimages.ProviderMetadata{Name: "pa"}, ImageRef: "imgA", Profile: "default", ManifestsPath: pathA,
		}
		profileB := providerimages.ProviderImageManifests{
			ProviderMetadata: providerimages.ProviderMetadata{Name: "pb"}, ImageRef: "imgB", Profile: "default", ManifestsPath: pathB,
		}

		rev1, err := NewRenderedRevision([]providerimages.ProviderImageManifests{profileA})
		g.Expect(err).NotTo(HaveOccurred())

		rev2, err := NewRenderedRevision([]providerimages.ProviderImageManifests{profileA, profileB})
		g.Expect(err).NotTo(HaveOccurred())

		id1, err := rev1.ContentID()
		g.Expect(err).NotTo(HaveOccurred())
		id2, err := rev2.ContentID()
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(id1).NotTo(Equal(id2))
	})

	t.Run("adding an object to a component changes contentID", func(t *testing.T) {
		g := NewWithT(t)

		pathSingle := writeManifestFile(t, configMapA)
		pathMulti := writeManifestFile(t, multiDoc(configMapA, configMapB))

		rev1, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: pathSingle},
		})
		g.Expect(err).NotTo(HaveOccurred())

		rev2, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: pathMulti},
		})
		g.Expect(err).NotTo(HaveOccurred())

		id1, err := rev1.ContentID()
		g.Expect(err).NotTo(HaveOccurred())
		id2, err := rev2.ContentID()
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(id1).NotTo(Equal(id2))
	})

	t.Run("empty manifests", func(t *testing.T) {
		g := NewWithT(t)

		path := writeManifestFile(t, "")

		rev, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: path},
		})
		g.Expect(err).NotTo(HaveOccurred())

		id, err := rev.ContentID()
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(id).NotTo(BeEmpty())
	})
}

func TestToAPIRevision(t *testing.T) {
	t.Run("single component fields", func(t *testing.T) {
		g := NewWithT(t)

		path := writeManifestFile(t, configMapA)
		rev, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "core"}, ImageRef: "quay.io/openshift/core@sha256:abcd", Profile: "default", ManifestsPath: path},
		})
		g.Expect(err).NotTo(HaveOccurred())

		apiRev, err := rev.ToAPIRevision("4.18.0", 1)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(apiRev.Revision).To(Equal(int64(1)))
		g.Expect(apiRev.ContentID).NotTo(BeEmpty())
		g.Expect(apiRev.Components).To(HaveLen(1))
		g.Expect(apiRev.Components[0].Type).To(Equal(operatorv1alpha1.InstallerComponentTypeImage))
		g.Expect(string(apiRev.Components[0].Image.Ref)).To(Equal("quay.io/openshift/core@sha256:abcd"))
		g.Expect(apiRev.Components[0].Image.Profile).To(Equal("default"))
	})

	t.Run("multiple components preserve order", func(t *testing.T) {
		g := NewWithT(t)

		pathA := writeManifestFile(t, configMapA)
		pathB := writeManifestFile(t, configMapB)

		rev, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "core"}, ImageRef: "img-core", Profile: "default", ManifestsPath: pathA},
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "infra"}, ImageRef: "img-infra", Profile: "aws", ManifestsPath: pathB},
		})
		g.Expect(err).NotTo(HaveOccurred())

		apiRev, err := rev.ToAPIRevision("4.18.0", 1)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(apiRev.Components).To(HaveLen(2))
		g.Expect(string(apiRev.Components[0].Image.Ref)).To(Equal("img-core"))
		g.Expect(apiRev.Components[0].Image.Profile).To(Equal("default"))
		g.Expect(string(apiRev.Components[1].Image.Ref)).To(Equal("img-infra"))
		g.Expect(apiRev.Components[1].Image.Profile).To(Equal("aws"))
	})

	t.Run("name format", func(t *testing.T) {
		g := NewWithT(t)

		path := writeManifestFile(t, configMapA)
		rev, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "core"}, ImageRef: "img1", Profile: "default", ManifestsPath: path},
		})
		g.Expect(err).NotTo(HaveOccurred())

		apiRev, err := rev.ToAPIRevision("4.18.0", 1)
		g.Expect(err).NotTo(HaveOccurred())

		contentID := apiRev.ContentID
		expectedName := fmt.Sprintf("4.18.0-%s-1", contentID[:revisionContentIDLen])
		g.Expect(string(apiRev.Name)).To(Equal(expectedName))
	})

	t.Run("name truncation with long version", func(t *testing.T) {
		g := NewWithT(t)

		path := writeManifestFile(t, configMapA)
		rev, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "core"}, ImageRef: "img1", Profile: "default", ManifestsPath: path},
		})
		g.Expect(err).NotTo(HaveOccurred())

		longVersion := strings.Repeat("x", 300)
		apiRev, err := rev.ToAPIRevision(longVersion, 1)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(len(apiRev.Name)).To(BeNumerically("<=", maxRevisionNameLen))
	})

	t.Run("contentID matches standalone call", func(t *testing.T) {
		g := NewWithT(t)

		path := writeManifestFile(t, configMapA)
		rev, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "core"}, ImageRef: "img1", Profile: "default", ManifestsPath: path},
		})
		g.Expect(err).NotTo(HaveOccurred())

		standaloneID, err := rev.ContentID()
		g.Expect(err).NotTo(HaveOccurred())

		apiRev, err := rev.ToAPIRevision("4.18.0", 1)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(apiRev.ContentID).To(Equal(standaloneID))
	})

	t.Run("zero components", func(t *testing.T) {
		g := NewWithT(t)

		rev, err := NewRenderedRevision([]providerimages.ProviderImageManifests{})
		g.Expect(err).NotTo(HaveOccurred())

		apiRev, err := rev.ToAPIRevision("4.18.0", 1)
		g.Expect(err).NotTo(HaveOccurred())

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

		path := writeManifestFile(t, "not: valid: yaml: [")
		_, err := NewRenderedRevision([]providerimages.ProviderImageManifests{
			{ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default", ManifestsPath: path},
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

		profile := providerimages.ProviderImageManifests{
			ProviderMetadata: providerimages.ProviderMetadata{Name: "p1"}, ImageRef: "img1", Profile: "default",
		}

		profile.ManifestsPath = pathWithVar
		rev1, err := NewRenderedRevision([]providerimages.ProviderImageManifests{profile})
		g.Expect(err).NotTo(HaveOccurred())

		profile.ManifestsPath = pathExpanded
		rev2, err := NewRenderedRevision([]providerimages.ProviderImageManifests{profile})
		g.Expect(err).NotTo(HaveOccurred())

		id1, err := rev1.ContentID()
		g.Expect(err).NotTo(HaveOccurred())
		id2, err := rev2.ContentID()
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(id1).To(Equal(id2))
	})
}

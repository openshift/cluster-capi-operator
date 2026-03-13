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
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
)

func must[T any](value T, err error) func(g *WithT) T {
	return func(g *WithT) T {
		g.THelper()
		g.Expect(err).NotTo(HaveOccurred())

		return value
	}
}

// writeManifestFile writes YAML content to a temp file and returns the path.
func writeManifestFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifests.yaml")
	g := NewWithT(t)
	g.Expect(os.WriteFile(path, []byte(content), 0644)).To(Succeed())

	return path
}

// profile creates a ProviderImageManifests with the given fields and manifest
// content written to a temp file.
func profile(t *testing.T, name, imageRef, profileName, manifestContent string) providerimages.ProviderImageManifests {
	t.Helper()

	return providerimages.ProviderImageManifests{
		ProviderMetadata: providerimages.ProviderMetadata{Name: name},
		ImageRef:         imageRef,
		Profile:          profileName,
		ManifestsPath:    writeManifestFile(t, manifestContent),
	}
}

// contentIDForProfiles computes the contentID for a set of profiles.
func contentIDForProfiles(g *WithT, profiles ...providerimages.ProviderImageManifests) string {
	g.THelper()
	rev := must(NewRenderedRevision(profiles))(g)

	return must(rev.ContentID())(g)
}

// forInstall creates an InstallerRevision from a RenderedRevision, failing the test on error.
func forInstall(g *WithT, rev RenderedRevision, releaseVersion string, revisionIndex int64) InstallerRevision { //nolint:unparam
	g.THelper()
	return must(rev.ForInstall(releaseVersion, revisionIndex))(g)
}

// multiDoc joins YAML documents with the standard separator.
func multiDoc(docs ...string) string {
	return strings.Join(docs, "\n---\n")
}

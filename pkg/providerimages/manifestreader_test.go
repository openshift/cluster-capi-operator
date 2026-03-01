/*
Copyright 2025 Red Hat, Inc.

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
package providerimages

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
)

func TestManifests(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []string
	}{
		{
			name:    "single document",
			content: "apiVersion: v1\nkind: ConfigMap\n",
			expected: []string{
				"apiVersion: v1\nkind: ConfigMap\n",
			},
		},
		{
			name:    "multiple documents",
			content: "apiVersion: v1\nkind: ConfigMap\n---\napiVersion: v1\nkind: Secret\n",
			expected: []string{
				"apiVersion: v1\nkind: ConfigMap\n",
				"apiVersion: v1\nkind: Secret\n",
			},
		},
		{
			name:    "leading separator",
			content: "---\napiVersion: v1\nkind: ConfigMap\n",
			expected: []string{
				"apiVersion: v1\nkind: ConfigMap\n",
			},
		},
		{
			name:    "trailing separator",
			content: "apiVersion: v1\nkind: ConfigMap\n---\n",
			expected: []string{
				"apiVersion: v1\nkind: ConfigMap\n",
			},
		},
		{
			name:    "leading and trailing separators",
			content: "---\napiVersion: v1\nkind: ConfigMap\n---\napiVersion: v1\nkind: Secret\n---\n",
			expected: []string{
				"apiVersion: v1\nkind: ConfigMap\n",
				"apiVersion: v1\nkind: Secret\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			tmpDir := t.TempDir()
			manifestsPath := filepath.Join(tmpDir, "manifests.yaml")
			g.Expect(os.WriteFile(manifestsPath, []byte(tt.content), 0644)).To(Succeed())

			p := &ProviderImageManifests{
				ManifestsPath: manifestsPath,
			}

			var got []string

			for doc, err := range p.Manifests() {
				g.Expect(err).NotTo(HaveOccurred())

				got = append(got, doc)
			}

			g.Expect(got).To(Equal(tt.expected))
		})
	}
}

func TestManifests_FileNotFound(t *testing.T) {
	g := NewWithT(t)

	missingPath := filepath.Join(t.TempDir(), "manifests.yaml")
	p := &ProviderImageManifests{
		ManifestsPath: missingPath,
	}

	var gotErr error

	for _, err := range p.Manifests() {
		if err != nil {
			gotErr = err
			break
		}
	}

	g.Expect(gotErr).To(MatchError(os.ErrNotExist))
}

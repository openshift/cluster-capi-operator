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
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr/testr"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// createMetadataYAML generates valid metadata.yaml content.
func createMetadataYAML(name, providerType, version, ocpPlatform, selfImageRef string, installOrder int) string {
	return fmt.Sprintf(`name: %s
selfImageRef: %s
ocpPlatform: %s
installOrder: %d
attributes:
  type: %s
  version: %s
`, name, selfImageRef, ocpPlatform, installOrder, providerType, version)
}

// writeProfile creates a profile directory with metadata.yaml and manifests.yaml.
func writeProfile(t *testing.T, baseDir, provider, profile, metadata, manifests string) {
	t.Helper()

	profileDir := filepath.Join(baseDir, provider, capiOperatorManifestsDir, profile)
	if err := os.MkdirAll(profileDir, 0o750); err != nil {
		t.Fatalf("failed to create profile directory: %v", err)
	}

	if metadata != "" {
		if err := os.WriteFile(filepath.Join(profileDir, metadataFile), []byte(metadata), 0o640); err != nil {
			t.Fatalf("failed to write metadata.yaml: %v", err)
		}
	}

	if manifests != "" {
		if err := os.WriteFile(filepath.Join(profileDir, manifestsFile), []byte(manifests), 0o640); err != nil {
			t.Fatalf("failed to write manifests.yaml: %v", err)
		}
	}
}

func Test_BuildImageRefMap(t *testing.T) {
	tests := []struct {
		name          string
		podSpec       corev1.PodSpec
		containerName string
		expected      map[string]string
		wantErr       bool
		errContains   string
	}{
		{
			name: "mixed volume types excludes non-image volumes",
			podSpec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "my-container",
					VolumeMounts: []corev1.VolumeMount{
						{Name: "certs", MountPath: "/etc/certs"},
						{Name: "provider-aws", MountPath: "/var/lib/provider-images/aws-cluster-api-controllers"},
					},
				}},
				Volumes: []corev1.Volume{
					{
						Name: "certs",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "my-secret"},
						},
					},
					{
						Name: "provider-aws",
						VolumeSource: corev1.VolumeSource{
							Image: &corev1.ImageVolumeSource{
								Reference: "registry.example.com/aws:v1",
							},
						},
					},
				},
			},
			containerName: "my-container",
			expected: map[string]string{
				"aws-cluster-api-controllers": "registry.example.com/aws:v1",
			},
		},
		{
			name: "multiple image volumes",
			podSpec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "my-container",
					VolumeMounts: []corev1.VolumeMount{
						{Name: "provider-aws", MountPath: "/images/aws-controllers"},
						{Name: "provider-gcp", MountPath: "/images/gcp-controllers"},
						{Name: "provider-azure", MountPath: "/images/azure-controllers"},
					},
				}},
				Volumes: []corev1.Volume{
					{
						Name: "provider-aws",
						VolumeSource: corev1.VolumeSource{
							Image: &corev1.ImageVolumeSource{Reference: "registry.example.com/aws:v1"},
						},
					},
					{
						Name: "provider-gcp",
						VolumeSource: corev1.VolumeSource{
							Image: &corev1.ImageVolumeSource{Reference: "registry.example.com/gcp:v1"},
						},
					},
					{
						Name: "provider-azure",
						VolumeSource: corev1.VolumeSource{
							Image: &corev1.ImageVolumeSource{Reference: "registry.example.com/azure:v1"},
						},
					},
				},
			},
			containerName: "my-container",
			expected: map[string]string{
				"aws-controllers":   "registry.example.com/aws:v1",
				"gcp-controllers":   "registry.example.com/gcp:v1",
				"azure-controllers": "registry.example.com/azure:v1",
			},
		},
		{
			name: "no image volumes returns empty map",
			podSpec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "my-container",
					VolumeMounts: []corev1.VolumeMount{
						{Name: "certs", MountPath: "/etc/certs"},
					},
				}},
				Volumes: []corev1.Volume{
					{
						Name: "certs",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "my-secret"},
						},
					},
				},
			},
			containerName: "my-container",
			expected:      map[string]string{},
		},
		{
			name: "missing container returns error",
			podSpec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "different-container",
				}},
			},
			containerName: "my-container",
			wantErr:       true,
			errContains:   `container "my-container": container not found in pod spec`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result, err := BuildImageRefMap(tt.podSpec, tt.containerName)

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errContains))

				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func Test_VolumeNameForImageRef(t *testing.T) {
	g := NewWithT(t)

	name := VolumeNameForImageRef("registry.example.com/My.Provider_AWS@sha256:abc123")
	g.Expect(name).To(MatchRegexp(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`), "expected a DNS-label-safe volume name")

	g.Expect(VolumeNameForImageRef("registry.example.com/core@sha256:def456")).To(
		Equal(VolumeNameForImageRef("registry.example.com/core@sha256:def456")),
		"expected the same image ref to always generate the same volume name",
	)

	g.Expect(VolumeNameForImageRef("registry.example.com/aws@sha256:abc")).NotTo(
		Equal(VolumeNameForImageRef("registry.example.com/gcp@sha256:def")),
		"expected different image refs to generate different volume names",
	)
}

func Test_BuildImageRefMapFromRefs(t *testing.T) {
	g := NewWithT(t)

	imageRefs := sets.New(
		"registry.example.com/aws@sha256:abc",
		"registry.example.com/gcp@sha256:def",
	)

	g.Expect(BuildImageRefMapFromRefs(imageRefs)).To(Equal(map[string]string{
		VolumeNameForImageRef("registry.example.com/aws@sha256:abc"): "registry.example.com/aws@sha256:abc",
		VolumeNameForImageRef("registry.example.com/gcp@sha256:def"): "registry.example.com/gcp@sha256:def",
	}))
}

func Test_ScanProviderImages(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, dir string)
		imageRefMap map[string]string
		validate    func(t *testing.T, g Gomega, result []ProviderImageManifests)
		wantErr     bool
		errContains string
	}{
		{
			name: "single valid provider image",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeProfile(t, dir, "aws-cluster-api-controllers", "default",
					createMetadataYAML("aws", "infrastructure", "v1.0.0", "aws", "PLACEHOLDER_IMAGE", 20),
					"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n",
				)
			},
			imageRefMap: map[string]string{
				"aws-cluster-api-controllers": "registry.example.com/capi-aws:v1.0.0",
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests) {
				t.Helper()
				g.Expect(result).To(HaveLen(1))

				manifest := result[0]
				g.Expect(manifest.Name).To(Equal("aws"))
				g.Expect(manifest.Attributes[AttributeKeyType]).To(Equal("infrastructure"))
				g.Expect(manifest.Attributes[AttributeKeyVersion]).To(Equal("v1.0.0"))
				g.Expect(manifest.InstallOrder).To(Equal(20))
				g.Expect(manifest.OCPPlatform).To(BeEquivalentTo("aws"))
				g.Expect(manifest.Profile).To(Equal("default"))
				g.Expect(manifest.ImageRef).To(Equal("registry.example.com/capi-aws:v1.0.0"))
				g.Expect(manifest.ManifestsPath).To(ContainSubstring(capiOperatorManifestsDir + "/default/" + manifestsFile))

				content, err := os.ReadFile(manifest.ManifestsPath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(content)).To(ContainSubstring("kind: ConfigMap"))
			},
		},
		{
			name: "empty provider directory is skipped",
			setup: func(t *testing.T, dir string) {
				t.Helper()

				if err := os.MkdirAll(filepath.Join(dir, "empty-provider"), 0o750); err != nil {
					t.Fatalf("failed to create directory: %v", err)
				}
			},
			imageRefMap: map[string]string{
				"empty-provider": "registry.example.com/empty-provider:v1.0.0",
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests) {
				t.Helper()
				g.Expect(result).To(BeEmpty())
			},
		},
		{
			// This test covers OCPBUGS-85530, where a provider image is
			// actually a placeholder because the provider is not built for the
			// current architecture.
			name: "provider without capi-operator-manifests directory is skipped",
			setup: func(t *testing.T, dir string) {
				t.Helper()

				providerDir := filepath.Join(dir, "no-manifests-provider")
				if err := os.MkdirAll(filepath.Join(providerDir, "some-other-dir"), 0o750); err != nil {
					t.Fatalf("failed to create directory: %v", err)
				}

				if err := os.WriteFile(filepath.Join(providerDir, "some-file.txt"), []byte("not capi manifests"), 0o640); err != nil {
					t.Fatalf("failed to write file: %v", err)
				}
			},
			imageRefMap: map[string]string{
				"no-manifests-provider": "registry.example.com/no-manifests-provider:v1.0.0",
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests) {
				t.Helper()
				g.Expect(result).To(BeEmpty())
			},
		},
		{
			name: "missing metadata.yaml",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeProfile(t, dir, "bad-provider", "default",
					"", // no metadata
					"apiVersion: v1\nkind: ConfigMap\n",
				)
			},
			imageRefMap: map[string]string{
				"bad-provider": "registry.example.com/bad-provider:v1.0.0",
			},
			wantErr:     true,
			errContains: "missing metadata.yaml",
		},
		{
			name: "missing manifests.yaml",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeProfile(t, dir, "bad-provider", "default",
					createMetadataYAML("aws", "infrastructure", "v1.0.0", "aws", "", 20),
					"", // no manifests
				)
			},
			imageRefMap: map[string]string{
				"bad-provider": "registry.example.com/bad-provider:v1.0.0",
			},
			wantErr:     true,
			errContains: "missing manifests.yaml",
		},
		{
			name: "invalid metadata.yaml",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeProfile(t, dir, "bad-provider", "default",
					"not: valid: yaml: content: [[[",
					"apiVersion: v1\nkind: ConfigMap\n",
				)
			},
			imageRefMap: map[string]string{
				"bad-provider": "registry.example.com/bad-provider:v1.0.0",
			},
			wantErr:     true,
			errContains: "failed to parse metadata.yaml",
		},
		{
			name: "multiple profiles in one image",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeProfile(t, dir, "multi-profile", "default",
					createMetadataYAML("aws", "infrastructure", "v1.0.0", "aws", "", 20),
					"kind: ConfigMap\nname: default-config\n",
				)
				writeProfile(t, dir, "multi-profile", "techpreview",
					createMetadataYAML("aws", "infrastructure", "v1.0.0-techpreview", "aws", "", 20),
					"kind: ConfigMap\nname: techpreview-config\n",
				)
			},
			imageRefMap: map[string]string{
				"multi-profile": "registry.example.com/multi-profile:v1.0.0",
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests) {
				t.Helper()
				g.Expect(result).To(HaveLen(2))

				profiles := make(map[string]ProviderImageManifests)
				for _, m := range result {
					profiles[m.Profile] = m
				}

				g.Expect(profiles).To(HaveKey("default"))
				g.Expect(profiles).To(HaveKey("techpreview"))
				g.Expect(profiles["default"].Attributes[AttributeKeyVersion]).To(Equal("v1.0.0"))
				g.Expect(profiles["techpreview"].Attributes[AttributeKeyVersion]).To(Equal("v1.0.0-techpreview"))
			},
		},
		{
			name: "platform-specific profiles",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeProfile(t, dir, "ccapio", "default",
					createMetadataYAML("core-vaps", "core", "v1.0.0", "", "", 10),
					"kind: ValidatingAdmissionPolicy\nmetadata:\n  name: core-vap\n",
				)
				writeProfile(t, dir, "ccapio", "aws",
					createMetadataYAML("aws-vaps", "infrastructure", "v1.0.0", "AWS", "", 20),
					"kind: ValidatingAdmissionPolicy\nmetadata:\n  name: aws-vap\n",
				)
				writeProfile(t, dir, "ccapio", "gcp",
					createMetadataYAML("gcp-vaps", "infrastructure", "v1.0.0", "GCP", "", 20),
					"kind: ValidatingAdmissionPolicy\nmetadata:\n  name: gcp-vap\n",
				)
			},
			imageRefMap: map[string]string{
				"ccapio": "registry.example.com/ccapio:v1.0.0",
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests) {
				t.Helper()
				g.Expect(result).To(HaveLen(3))

				profiles := make(map[string]ProviderImageManifests)
				for _, m := range result {
					profiles[m.Profile] = m
				}

				g.Expect(profiles).To(HaveKey("default"))
				g.Expect(profiles).To(HaveKey("aws"))
				g.Expect(profiles).To(HaveKey("gcp"))

				g.Expect(profiles["default"].OCPPlatform).To(BeEquivalentTo(""))
				g.Expect(profiles["default"].MatchesPlatform("AWS")).To(BeTrue())
				g.Expect(profiles["default"].MatchesPlatform("GCP")).To(BeTrue())

				g.Expect(profiles["aws"].OCPPlatform).To(BeEquivalentTo("AWS"))
				g.Expect(profiles["aws"].MatchesPlatform("AWS")).To(BeTrue())
				g.Expect(profiles["aws"].MatchesPlatform("GCP")).To(BeFalse())

				g.Expect(profiles["gcp"].OCPPlatform).To(BeEquivalentTo("GCP"))
				g.Expect(profiles["gcp"].MatchesPlatform("GCP")).To(BeTrue())
				g.Expect(profiles["gcp"].MatchesPlatform("AWS")).To(BeFalse())
			},
		},
		{
			name: "non-profile subdirectory skipped",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeProfile(t, dir, "with-random-subdir", "default",
					createMetadataYAML("aws", "infrastructure", "v1.0.0", "aws", "", 20),
					"kind: ConfigMap\n",
				)
				// Create a non-profile subdirectory (has a file but not metadata.yaml/manifests.yaml)
				randomDir := filepath.Join(dir, "with-random-subdir", capiOperatorManifestsDir, "randomdir")
				if err := os.MkdirAll(randomDir, 0o750); err != nil {
					t.Fatalf("failed to create directory: %v", err)
				}

				if err := os.WriteFile(filepath.Join(randomDir, "somefile.txt"), []byte("not a profile"), 0o640); err != nil {
					t.Fatalf("failed to write file: %v", err)
				}
			},
			imageRefMap: map[string]string{
				"with-random-subdir": "registry.example.com/with-random-subdir:v1.0.0",
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests) {
				t.Helper()
				g.Expect(result).To(HaveLen(1))
				g.Expect(result[0].Profile).To(Equal("default"))
			},
		},
		{
			name: "multiple provider images",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeProfile(t, dir, "aws-provider", "default",
					createMetadataYAML("aws", "infrastructure", "v1.0.0", "AWS", "", 20),
					"kind: ConfigMap\nname: aws\n",
				)
				writeProfile(t, dir, "core-provider", "default",
					createMetadataYAML("core", "core", "v1.0.0", "", "", 10),
					"kind: ConfigMap\nname: core\n",
				)
			},
			imageRefMap: map[string]string{
				"aws-provider":  "registry.example.com/aws:v1.0.0",
				"core-provider": "registry.example.com/core:v1.0.0",
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests) {
				t.Helper()
				g.Expect(result).To(HaveLen(2))

				providers := make(map[string]ProviderImageManifests)
				for _, m := range result {
					providers[m.Name] = m
				}

				g.Expect(providers["aws"].ImageRef).To(Equal("registry.example.com/aws:v1.0.0"))
				g.Expect(providers["core"].ImageRef).To(Equal("registry.example.com/core:v1.0.0"))
			},
		},
		{
			name: "extra directories on disk not in map are ignored",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				writeProfile(t, dir, "unknown-provider", "default",
					createMetadataYAML("unknown", "infrastructure", "v1.0.0", "", "", 10),
					"kind: ConfigMap\n",
				)
			},
			imageRefMap: map[string]string{},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests) {
				t.Helper()
				g.Expect(result).To(BeEmpty())
			},
		},
		{
			name: "directory in map that does not exist on disk is skipped",
			setup: func(t *testing.T, dir string) {
				t.Helper()
			},
			imageRefMap: map[string]string{
				"missing-provider": "registry.example.com/missing-provider:v1.0.0",
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests) {
				t.Helper()
				g.Expect(result).To(BeEmpty())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			tmpDir := t.TempDir()

			tt.setup(t, tmpDir)

			result, err := ScanProviderImages(testr.New(t), tmpDir, tt.imageRefMap)

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())

				if tt.errContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errContains))
				}

				return
			}

			g.Expect(err).NotTo(HaveOccurred())

			if tt.validate != nil {
				tt.validate(t, g, result)
			}
		})
	}
}

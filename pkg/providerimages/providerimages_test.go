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
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	. "github.com/onsi/gomega"
)

// fakeImageFetcher implements imageFetcher for testing.
type fakeImageFetcher struct {
	images map[string]v1.Image
	errors map[string]error
}

func (f *fakeImageFetcher) Fetch(ctx context.Context, ref name.Reference) (v1.Image, error) {
	// Check context cancellation first
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	if err, ok := f.errors[ref.String()]; ok {
		return nil, err
	}

	if img, ok := f.images[ref.String()]; ok {
		return img, nil
	}

	return nil, fmt.Errorf("image not found: %s", ref.String())
}

// createTarLayer creates a v1.Layer from a map of file paths to content.
func createTarLayer(files map[string]string) (v1.Layer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	for path, content := range files {
		hdr := &tar.Header{
			Name: path,
			Mode: 0644,
			Size: int64(len(content)),
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}

		if _, err := tw.Write([]byte(content)); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	tarBytes := buf.Bytes()
	opener := func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(tarBytes)), nil
	}

	return tarball.LayerFromOpener(opener)
}

// createTestImage creates a v1.Image with the given files in a single layer.
func createTestImage(files map[string]string) (v1.Image, error) {
	layer, err := createTarLayer(files)
	if err != nil {
		return nil, err
	}

	return mutate.AppendLayers(empty.Image, layer)
}

// createTestImageWithLayers creates a v1.Image with multiple layers.
// Each element in the layers slice represents the files for one layer.
func createTestImageWithLayers(layers []map[string]string) (v1.Image, error) {
	img := empty.Image

	for _, files := range layers {
		layer, err := createTarLayer(files)
		if err != nil {
			return nil, err
		}

		img, err = mutate.AppendLayers(img, layer)
		if err != nil {
			return nil, err
		}
	}

	return img, nil
}

// Test path constants derived from production constants.
// Tar paths don't use leading slashes, so we use capiManifestsDirName directly.
const (
	testDefaultProfile = "default"
	testMetadataPath   = capiManifestsDirName + "/" + testDefaultProfile + "/" + metadataFile
	testManifestsPath  = capiManifestsDirName + "/" + testDefaultProfile + "/" + manifestsFile
)

// createCapiManifestsImage creates a test image with metadata and manifests in a profile subdirectory.
func createCapiManifestsImage(metadataContent, manifestsContent string) (v1.Image, error) {
	return createTestImage(map[string]string{
		testMetadataPath:  metadataContent,
		testManifestsPath: manifestsContent,
	})
}

// createCapiManifestsImageWithLayers creates a test image with multiple layers containing capi-operator-manifests.
func createCapiManifestsImageWithLayers(layers []struct{ metadata, manifests string }) (v1.Image, error) {
	fileLayers := make([]map[string]string, 0, len(layers))

	for _, l := range layers {
		fileLayers = append(fileLayers, map[string]string{
			testMetadataPath:  l.metadata,
			testManifestsPath: l.manifests,
		})
	}

	return createTestImageWithLayers(fileLayers)
}

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

//nolint:gocognit,cyclop
func Test_readProviderImages(t *testing.T) {
	tests := []struct {
		name            string
		containerImages []string
		setupFetcher    func(t *testing.T) *fakeImageFetcher
		setupContext    func(parent context.Context) (context.Context, context.CancelFunc)
		validate        func(t *testing.T, g Gomega, result []ProviderImageManifests, outputDir string)
		wantErr         bool
		errContains     string
	}{
		{
			name: "single valid provider image",
			containerImages: []string{
				"registry.example.com/capi-aws:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				img, err := createCapiManifestsImage(
					createMetadataYAML("aws", "infrastructure", "v1.0.0", "aws", "PLACEHOLDER_IMAGE", 20),
					"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n",
				)
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/capi-aws:v1.0.0": img,
					},
				}
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests, outputDir string) {
				t.Helper()
				g.Expect(result).To(HaveLen(1))

				manifest := result[0]
				g.Expect(manifest.Name).To(Equal("aws"))
				g.Expect(manifest.Attributes[AttributeKeyType]).To(Equal("infrastructure"))
				g.Expect(manifest.Attributes[AttributeKeyVersion]).To(Equal("v1.0.0"))
				g.Expect(manifest.InstallOrder).To(Equal(20))
				g.Expect(manifest.OCPPlatform).To(BeEquivalentTo("aws"))
				g.Expect(manifest.Profile).To(Equal(testDefaultProfile))
				g.Expect(manifest.ImageRef).To(Equal("registry.example.com/capi-aws:v1.0.0"))
				g.Expect(manifest.ContentID).To(HaveLen(64)) // sha256 hex string
				g.Expect(manifest.ManifestsPath).To(ContainSubstring(testDefaultProfile + "/" + manifestsFile))

				// Verify file was written
				content, err := os.ReadFile(manifest.ManifestsPath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(content)).To(ContainSubstring("kind: ConfigMap"))

				// Verify ContentID is sha256 of the file content
				expectedHash := sha256.Sum256(content)
				g.Expect(manifest.ContentID).To(Equal(hex.EncodeToString(expectedHash[:])))
			},
		},
		{
			name: "image without capi-manifests directory",
			containerImages: []string{
				"registry.example.com/no-capi:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				img, err := createTestImage(map[string]string{
					"some-other-file.txt": "hello world",
				})
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/no-capi:v1.0.0": img,
					},
				}
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests, outputDir string) {
				t.Helper()
				g.Expect(result).To(BeEmpty())
			},
		},
		{
			name: "missing metadata.yaml",
			containerImages: []string{
				"registry.example.com/missing-metadata:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				img, err := createTestImage(map[string]string{
					testManifestsPath: "apiVersion: v1\nkind: ConfigMap\n",
				})
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/missing-metadata:v1.0.0": img,
					},
				}
			},
			wantErr:     true,
			errContains: "missing metadata.yaml",
		},
		{
			name: "missing manifests.yaml",
			containerImages: []string{
				"registry.example.com/missing-manifests:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				img, err := createTestImage(map[string]string{
					testMetadataPath: createMetadataYAML("aws", "infrastructure", "v1.0.0", "aws", "", 20),
				})
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/missing-manifests:v1.0.0": img,
					},
				}
			},
			wantErr:     true,
			errContains: "missing manifests.yaml",
		},
		{
			name: "invalid metadata.yaml",
			containerImages: []string{
				"registry.example.com/invalid-metadata:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				img, err := createCapiManifestsImage(
					"not: valid: yaml: content: [[[",
					"apiVersion: v1\nkind: ConfigMap\n",
				)
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/invalid-metadata:v1.0.0": img,
					},
				}
			},
			wantErr:     true,
			errContains: "failed to parse metadata.yaml",
		},
		{
			name: "invalid image reference",
			containerImages: []string{
				"not a valid image reference!!!",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				return &fakeImageFetcher{}
			},
			wantErr:     true,
			errContains: "failed to parse image reference",
		},
		{
			name: "image fetch failure",
			containerImages: []string{
				"registry.example.com/fetch-fail:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				return &fakeImageFetcher{
					errors: map[string]error{
						"registry.example.com/fetch-fail:v1.0.0": fmt.Errorf("network error: connection refused"),
					},
				}
			},
			wantErr:     true,
			errContains: "network error: connection refused",
		},
		{
			name: "manifest image name replacement",
			containerImages: []string{
				"registry.example.com/capi-aws:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				img, err := createCapiManifestsImage(
					createMetadataYAML("aws", "infrastructure", "v1.0.0", "aws", "PLACEHOLDER_IMAGE", 20),
					"image: PLACEHOLDER_IMAGE\nanotherImage: PLACEHOLDER_IMAGE\n",
				)
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/capi-aws:v1.0.0": img,
					},
				}
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests, outputDir string) {
				t.Helper()
				g.Expect(result).To(HaveLen(1))

				content, err := os.ReadFile(result[0].ManifestsPath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(content)).To(Equal("image: registry.example.com/capi-aws:v1.0.0\nanotherImage: registry.example.com/capi-aws:v1.0.0\n"))
				g.Expect(string(content)).NotTo(ContainSubstring("PLACEHOLDER_IMAGE"))
			},
		},
		{
			name: "empty manifest image name",
			containerImages: []string{
				"registry.example.com/capi-aws:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				img, err := createCapiManifestsImage(
					createMetadataYAML("aws", "infrastructure", "v1.0.0", "aws", "", 20),
					"image: some-other-image:latest\n",
				)
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/capi-aws:v1.0.0": img,
					},
				}
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests, outputDir string) {
				t.Helper()
				g.Expect(result).To(HaveLen(1))

				content, err := os.ReadFile(result[0].ManifestsPath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(content)).To(Equal("image: some-other-image:latest\n"))
			},
		},
		{
			name: "missing manifest image name",
			containerImages: []string{
				"registry.example.com/capi-aws:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				// Create metadata YAML without manifestImageName field
				img, err := createCapiManifestsImage(
					`providerName: aws
providerType: infrastructure
providerVersion: v1.0.0
ocpPlatform: aws
contentID: id
`,
					"image: some-other-image:latest\n",
				)
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/capi-aws:v1.0.0": img,
					},
				}
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests, outputDir string) {
				t.Helper()
				g.Expect(result).To(HaveLen(1))

				content, err := os.ReadFile(result[0].ManifestsPath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(content)).To(Equal("image: some-other-image:latest\n"))
			},
		},
		{
			name: "multiple errors aggregated",
			containerImages: []string{
				"registry.example.com/fail-aws:v1.0.0",
				"registry.example.com/fail-azure:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				return &fakeImageFetcher{
					errors: map[string]error{
						"registry.example.com/fail-aws:v1.0.0":   fmt.Errorf("aws fetch error"),
						"registry.example.com/fail-azure:v1.0.0": fmt.Errorf("azure fetch error"),
					},
				}
			},
			wantErr:     true,
			errContains: "fetch error",
		},
		{
			name: "context cancellation",
			containerImages: []string{
				"registry.example.com/capi-aws:v1.0.0",
			},
			setupContext: func(parent context.Context) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(parent)
				cancel() // Cancel immediately

				return ctx, cancel
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				img, err := createCapiManifestsImage(
					createMetadataYAML("aws", "infrastructure", "v1.0.0", "aws", "", 20),
					"apiVersion: v1\n",
				)
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/capi-aws:v1.0.0": img,
					},
				}
			},
			wantErr:     true,
			errContains: "context canceled",
		},
		{
			name: "layers processed in correct order",
			containerImages: []string{
				"registry.example.com/layered:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				// Create image with 2 layers - higher layer should override
				img, err := createCapiManifestsImageWithLayers([]struct{ metadata, manifests string }{
					// Lower layer (added first)
					{
						metadata:  createMetadataYAML("aws-old", "OldType", "v0.0.1", "old", "", 10),
						manifests: "content: from-lower-layer\n",
					},
					// Higher layer (added second, should win)
					{
						metadata:  createMetadataYAML("aws-new", "NewType", "v2.0.0", "new", "", 20),
						manifests: "content: from-higher-layer\n",
					},
				})
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/layered:v1.0.0": img,
					},
				}
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests, outputDir string) {
				t.Helper()
				g.Expect(result).To(HaveLen(1))

				// Higher layer values should be used
				manifest := result[0]
				g.Expect(manifest.Name).To(Equal("aws-new"))
				g.Expect(manifest.Attributes[AttributeKeyType]).To(Equal("NewType"))
				g.Expect(manifest.Attributes[AttributeKeyVersion]).To(Equal("v2.0.0"))
				g.Expect(manifest.InstallOrder).To(Equal(20))

				content, err := os.ReadFile(manifest.ManifestsPath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(content)).To(Equal("content: from-higher-layer\n"))
			},
		},
		{
			name: "multiple profiles in one image",
			containerImages: []string{
				"registry.example.com/multi-profile:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				img, err := createTestImage(map[string]string{
					capiManifestsDirName + "/default/metadata.yaml":      createMetadataYAML("aws", "infrastructure", "v1.0.0", "aws", "", 20),
					capiManifestsDirName + "/default/manifests.yaml":     "kind: ConfigMap\nname: default-config\n",
					capiManifestsDirName + "/techpreview/metadata.yaml":  createMetadataYAML("aws", "infrastructure", "v1.0.0-techpreview", "aws", "", 20),
					capiManifestsDirName + "/techpreview/manifests.yaml": "kind: ConfigMap\nname: techpreview-config\n",
				})
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/multi-profile:v1.0.0": img,
					},
				}
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests, outputDir string) {
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

				defaultContent, err := os.ReadFile(profiles["default"].ManifestsPath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(defaultContent)).To(ContainSubstring("default-config"))

				techpreviewContent, err := os.ReadFile(profiles["techpreview"].ManifestsPath)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(string(techpreviewContent)).To(ContainSubstring("techpreview-config"))
			},
		},
		{
			name: "platform-specific profiles filtered by OCPPlatform",
			containerImages: []string{
				"registry.example.com/ccapio:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				img, err := createTestImage(map[string]string{
					capiManifestsDirName + "/default/metadata.yaml":  createMetadataYAML("core-vaps", "core", "v1.0.0", "", "", 10),
					capiManifestsDirName + "/default/manifests.yaml": "kind: ValidatingAdmissionPolicy\nmetadata:\n  name: core-vap\n",
					capiManifestsDirName + "/aws/metadata.yaml":      createMetadataYAML("aws-vaps", "infrastructure", "v1.0.0", "AWS", "", 20),
					capiManifestsDirName + "/aws/manifests.yaml":     "kind: ValidatingAdmissionPolicy\nmetadata:\n  name: aws-vap\n",
					capiManifestsDirName + "/gcp/metadata.yaml":      createMetadataYAML("gcp-vaps", "infrastructure", "v1.0.0", "GCP", "", 20),
					capiManifestsDirName + "/gcp/manifests.yaml":     "kind: ValidatingAdmissionPolicy\nmetadata:\n  name: gcp-vap\n",
				})
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/ccapio:v1.0.0": img,
					},
				}
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests, outputDir string) {
				t.Helper()
				g.Expect(result).To(HaveLen(3))

				profiles := make(map[string]ProviderImageManifests)
				for _, m := range result {
					profiles[m.Profile] = m
				}

				g.Expect(profiles).To(HaveKey("default"))
				g.Expect(profiles).To(HaveKey("aws"))
				g.Expect(profiles).To(HaveKey("gcp"))

				// Platform-independent profile matches everything
				g.Expect(profiles["default"].OCPPlatform).To(BeEquivalentTo(""))
				g.Expect(profiles["default"].MatchesPlatform("AWS")).To(BeTrue())
				g.Expect(profiles["default"].MatchesPlatform("GCP")).To(BeTrue())

				// AWS profile matches only AWS
				g.Expect(profiles["aws"].OCPPlatform).To(BeEquivalentTo("AWS"))
				g.Expect(profiles["aws"].MatchesPlatform("AWS")).To(BeTrue())
				g.Expect(profiles["aws"].MatchesPlatform("GCP")).To(BeFalse())

				// GCP profile matches only GCP
				g.Expect(profiles["gcp"].OCPPlatform).To(BeEquivalentTo("GCP"))
				g.Expect(profiles["gcp"].MatchesPlatform("GCP")).To(BeTrue())
				g.Expect(profiles["gcp"].MatchesPlatform("AWS")).To(BeFalse())
			},
		},
		{
			name: "non-profile subdirectory skipped",
			containerImages: []string{
				"registry.example.com/with-random-subdir:v1.0.0",
			},
			setupFetcher: func(t *testing.T) *fakeImageFetcher {
				t.Helper()
				img, err := createTestImage(map[string]string{
					capiManifestsDirName + "/default/metadata.yaml":  createMetadataYAML("aws", "infrastructure", "v1.0.0", "aws", "", 20),
					capiManifestsDirName + "/default/manifests.yaml": "kind: ConfigMap\n",
					capiManifestsDirName + "/randomdir/somefile.txt": "this is not a profile",
				})
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}

				return &fakeImageFetcher{
					images: map[string]v1.Image{
						"registry.example.com/with-random-subdir:v1.0.0": img,
					},
				}
			},
			validate: func(t *testing.T, g Gomega, result []ProviderImageManifests, outputDir string) {
				t.Helper()
				g.Expect(result).To(HaveLen(1))
				g.Expect(result[0].Profile).To(Equal("default"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			tmpDir := t.TempDir()
			fetcher := tt.setupFetcher(t)

			ctx := t.Context()

			if tt.setupContext != nil {
				var cancel context.CancelFunc
				ctx, cancel = tt.setupContext(ctx)

				defer cancel()
			}

			result, err := readProviderImages(ctx, logr.Discard(), tt.containerImages, tmpDir, fetcher)

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())

				if tt.errContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errContains))
				}

				return
			}

			g.Expect(err).NotTo(HaveOccurred())

			// Verify output directory structure for all successful results
			// 1. Each manifest's profile directory should correspond to a containerImage + profile
			usedProfileDirs := make(map[string]bool)

			for _, manifest := range result {
				// ManifestsPath is now outputDir/imageRef/profile/manifests.yaml
				profileDir := filepath.Dir(manifest.ManifestsPath)
				imageDir := filepath.Dir(profileDir)

				// Find a containerImage that would produce this directory
				found := false

				for _, imageRef := range tt.containerImages {
					expectedDir := filepath.Join(tmpDir, sanitizeImageRef(imageRef))
					if imageDir == expectedDir {
						// Ensure we don't have duplicate profile directories
						// (each image+profile combination produces unique output)
						g.Expect(usedProfileDirs[profileDir]).To(BeFalse(),
							"profile directory %s was used by multiple manifests", profileDir)

						usedProfileDirs[profileDir] = true
						found = true

						break
					}
				}

				g.Expect(found).To(BeTrue(),
					"manifest directory %s does not correspond to any containerImage", imageDir)

				// 2. Verify the manifest file exists and is readable
				_, err := os.Stat(manifest.ManifestsPath)
				g.Expect(err).NotTo(HaveOccurred(), "expected manifest file %s to exist", manifest.ManifestsPath)
			}

			if tt.validate != nil {
				tt.validate(t, g, result, tmpDir)
			}
		})
	}
}

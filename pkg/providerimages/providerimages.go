/*
Copyright 2024 Red Hat, Inc.

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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	configv1 "github.com/openshift/api/config/v1"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	capiManifestsDirName = "capi-operator-manifests"
	capiManifestsDir     = "/" + capiManifestsDirName
	metadataFile         = "metadata.yaml"
	manifestsFile        = "manifests.yaml"

	pullSecretName      = "pull-secret"
	pullSecretNamespace = "openshift-config"  //nolint:gosec // Not a credential, just a namespace name
	pullSecretKey       = ".dockerconfigjson" //nolint:gosec // Not a credential, just a key name

	// AttributeKeyType is the key for the provider type attribute.
	AttributeKeyType = "type"
	// AttributeKeyVersion is the key for the provider version attribute.
	AttributeKeyVersion = "version"
)

// ProviderImageManifests represents metadata and manifests read from a provider image.
type ProviderImageManifests struct {
	ProviderMetadata

	ImageRef string
	Profile  string

	ContentID     string
	ManifestsPath string
}

// ProviderMetadata is metadata about a provider image provided in the metadata.yaml file.
type ProviderMetadata struct {
	Name         string                `json:"name"`
	Attributes   map[string]string     `json:"attributes,omitempty"`
	OCPPlatform  configv1.PlatformType `json:"ocpPlatform,omitempty"`
	SelfImageRef string                `json:"selfImageRef,omitempty"`
	InstallOrder int                   `json:"installOrder,omitempty"`
}

// MatchesPlatform reports whether this profile should be installed on the
// given cluster platform. Profiles with no OCPPlatform set (empty string)
// are platform-independent and match every cluster.
func (m ProviderMetadata) MatchesPlatform(clusterPlatform configv1.PlatformType) bool {
	return m.OCPPlatform == "" || m.OCPPlatform == clusterPlatform
}

// imageFetcher abstracts fetching container images for testability.
type imageFetcher interface {
	Fetch(ctx context.Context, ref name.Reference) (v1.Image, error)
}

// remoteImageFetcher fetches images from a remote registry.
type remoteImageFetcher struct {
	keychain authn.Keychain
}

// Fetch fetches an image from a remote registry.
func (r remoteImageFetcher) Fetch(ctx context.Context, ref name.Reference) (v1.Image, error) {
	img, err := remote.Image(ref, remote.WithAuthFromKeychain(r.keychain), remote.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch remote image: %w", err)
	}

	return img, nil
}

// ReadProviderImages returns a list of ProviderImageManifests read directly
// from operand container images.
//
// containerImages is a map of provider names to provider image references
//
// A provider image may contain a /capi-operator-manifests directory containing the following 2 files:
// - metadata.yaml: a YAML file whose contents are a ProviderMetadata struct
// - manifests.yaml: a KRM containing the provider manifests
//
// If a provider image does not contain a /capi-operator-manifests directory, it is ignored.
// If a provider image contains /capi-operator-manifests but one of the required files is missing, an error is returned.
//
// ReadProviderImages fetches each provider image. If it contains valid CAPI Operator
// manifests, the contents are stored in a local cache directory specified by
// providerImageDir. Manifests are written to a subdirectory named after the
// image reference.
//
// When writing manifests to the cache, any occurrences of `manifestImageName` as
// specified in the provider's metadata.yaml are replaced with the image
// reference.
//
// The pull secret is fetched from the "pull-secret" Secret in the "openshift-config"
// namespace using the provided client.Reader.
func ReadProviderImages(ctx context.Context, k8sClient client.Reader, log logr.Logger, containerImages []string, providerImageDir string) ([]ProviderImageManifests, error) {
	var secret corev1.Secret
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: pullSecretName, Namespace: pullSecretNamespace}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get pull secret: %w", err)
	}

	pullSecret := secret.Data[pullSecretKey]

	keychain, err := parseDockerConfig(pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pull secret: %w", err)
	}

	return readProviderImages(ctx, log, containerImages, providerImageDir, remoteImageFetcher{keychain: keychain})
}

type providerImageResult struct {
	imageRef  string
	manifests []ProviderImageManifests
	err       error
}

func readProviderImages(ctx context.Context, log logr.Logger, containerImages []string, providerImageDir string, fetcher imageFetcher) ([]ProviderImageManifests, error) {
	log.Info("looking for provider manifests in container images")

	results := make(chan providerImageResult, len(containerImages))
	g, ctx := errgroup.WithContext(ctx)

	g.SetLimit(5) // Limit to 5 concurrent fetches

	for _, imageRef := range containerImages {
		g.Go(func() error {
			manifests, err := processProviderImage(ctx, imageRef, providerImageDir, fetcher)
			results <- providerImageResult{
				imageRef:  imageRef,
				manifests: manifests,
				err:       err,
			}

			return nil // we're returning
		})
	}

	_ = g.Wait() // We're not actually returning errors directly

	close(results)

	var providerImages []ProviderImageManifests

	var err error

	for result := range results {
		switch {
		case result.err != nil:
			log.Error(result.err, "failed to process provider image", "image", result.imageRef)
			err = errors.Join(err, fmt.Errorf("fetching provider from image %s: %w", result.imageRef, result.err))
		case len(result.manifests) == 0:
			log.Info("no provider manifests found in container image", "image", result.imageRef)
		default:
			for _, manifest := range result.manifests {
				log.Info("found provider manifests in container image", "image", result.imageRef,
					"provider", manifest.Name,
					"type", manifest.Attributes[AttributeKeyType],
					"version", manifest.Attributes[AttributeKeyVersion],
					"profile", manifest.Profile,
					"ocpPlatform", manifest.OCPPlatform)

				providerImages = append(providerImages, manifest)
			}
		}
	}

	log.Info("finished looking for provider manifests in container images")

	if err != nil {
		return nil, err
	}

	return providerImages, nil
}

func processProviderImage(ctx context.Context, imageRef, providerImageDir string, fetcher imageFetcher) ([]ProviderImageManifests, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image reference %s: %w", imageRef, err)
	}

	img, err := fetcher.Fetch(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch image %s: %w", imageRef, err)
	}

	// Create output directory for this provider image
	sanitizedRef := sanitizeImageRef(imageRef)
	outputDir := filepath.Join(providerImageDir, sanitizedRef)

	if err := os.MkdirAll(outputDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Extract files from the image to disk
	if err := extractCapiManifestsToDir(img, outputDir); err != nil {
		return nil, fmt.Errorf("failed to extract manifests from image to %s: %w", outputDir, err)
	}

	// Discover profiles from extracted files
	profiles, err := discoverProfiles(outputDir)
	if err != nil {
		if errors.Is(err, errNoCapiManifests) {
			// Image doesn't contain /capi-operator-manifests, skip it
			return nil, nil
		}

		return nil, fmt.Errorf("failed to discover profiles in %s: %w", outputDir, err)
	}

	// Process each profile
	results := make([]ProviderImageManifests, 0, len(profiles))

	for _, profile := range profiles {
		profileDir := filepath.Join(outputDir, profile.Profile)
		manifestsPath := filepath.Join(profileDir, manifestsFile)

		contentID, err := writeManifestsWithHash(manifestsPath, profile.Manifests, profile.Metadata.SelfImageRef, imageRef)
		if err != nil {
			return nil, fmt.Errorf("failed to write manifests for profile %s: %w", profile.Profile, err)
		}

		results = append(results, ProviderImageManifests{
			ProviderMetadata: *profile.Metadata,
			ImageRef:         imageRef,
			Profile:          profile.Profile,
			ContentID:        contentID,
			ManifestsPath:    manifestsPath,
		})
	}

	return results, nil
}

var (
	errNoCapiManifests  = errors.New("no capi-manifests directory found")
	errMissingMetadata  = errors.New("missing metadata.yaml in /capi-operator-manifests")
	errMissingManifests = errors.New("missing manifests.yaml in /capi-operator-manifests")
)

// profileManifests holds parsed metadata and manifest content for a single profile.
type profileManifests struct {
	Profile   string
	Metadata  *ProviderMetadata
	Manifests string
}

// discoverProfiles scans a directory for valid profiles.
// Each subdirectory must contain both metadata.yaml and manifests.yaml.
// Returns an error if no profiles are found or if any profile is incomplete.
func discoverProfiles(dir string) ([]profileManifests, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errNoCapiManifests
		}

		return nil, fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	var profiles []profileManifests

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		profileName := entry.Name()
		profileDir := filepath.Join(dir, profileName)

		profile, isProfile, err := loadProfile(profileName, profileDir)
		if err != nil {
			return nil, err
		}

		if isProfile {
			profiles = append(profiles, *profile)
		}
	}

	if len(profiles) == 0 {
		return nil, errNoCapiManifests
	}

	return profiles, nil
}

// loadProfile loads a single profile from a directory.
// Returns isProfile=false if the directory is not a profile (no metadata.yaml or manifests.yaml).
// Returns an error if the profile is incomplete (has one file but not the other).
func loadProfile(profileName, profileDir string) (profile *profileManifests, isProfile bool, err error) {
	metadataPath := filepath.Join(profileDir, metadataFile)
	manifestsPath := filepath.Join(profileDir, manifestsFile)

	metadataInfo, metadataErr := os.Stat(metadataPath)
	manifestsInfo, manifestsErr := os.Stat(manifestsPath)

	metadataExists := metadataErr == nil && !metadataInfo.IsDir()
	manifestsExists := manifestsErr == nil && !manifestsInfo.IsDir()

	if !metadataExists && !manifestsExists {
		return nil, false, nil
	}

	if !metadataExists {
		return nil, false, fmt.Errorf("profile %s: %w", profileName, errMissingMetadata)
	}

	if !manifestsExists {
		return nil, false, fmt.Errorf("profile %s: %w", profileName, errMissingManifests)
	}

	metadataContent, err := os.ReadFile(metadataPath) //nolint:gosec // path constructed from trusted input
	if err != nil {
		return nil, false, fmt.Errorf("failed to read metadata for profile %s: %w", profileName, err)
	}

	manifestsContent, err := os.ReadFile(manifestsPath) //nolint:gosec // path constructed from trusted input
	if err != nil {
		return nil, false, fmt.Errorf("failed to read manifests for profile %s: %w", profileName, err)
	}

	var metadata ProviderMetadata
	if err := yaml.Unmarshal(metadataContent, &metadata); err != nil {
		return nil, false, fmt.Errorf("failed to parse metadata.yaml for profile %s: %w", profileName, err)
	}

	return &profileManifests{
		Profile:   profileName,
		Metadata:  &metadata,
		Manifests: string(manifestsContent),
	}, true, nil
}

// extractCapiManifestsToDir extracts all files under /capi-operator-manifests from the
// image to destDir. Iterates layers top to bottom so higher layers take precedence.
func extractCapiManifestsToDir(img v1.Image, destDir string) error {
	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("failed to get image layers: %w", err)
	}

	// Iterate layers in reverse order (top to bottom) since higher layers
	// take precedence. extractFilesToDir skips files that already exist.
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]

		rc, err := layer.Uncompressed()
		if err != nil {
			return fmt.Errorf("failed to uncompress layer: %w", err)
		}

		err = extractFilesToDir(rc, capiManifestsDir, destDir)

		err = errors.Join(err, rc.Close())
		if err != nil {
			return err
		}
	}

	return nil
}

// extractFilesToDir extracts all files under prefix from a tar stream to destDir.
// Files are written preserving their relative path under the prefix.
// e.g., prefix="/capi-operator-manifests", file="/capi-operator-manifests/default/foo.yaml"
// → written to destDir/default/foo.yaml
// Files that already exist are skipped (caller iterates layers top to bottom).
func extractFilesToDir(r io.Reader, prefix, destDir string) error {
	tr := tar.NewReader(r)
	// Ensure prefix has leading slash and no trailing slash for consistent matching
	prefix = path.Clean("/" + prefix)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			return fmt.Errorf("failed to read tar: %w", err)
		}

		// Directory entries in tar are just markers with no content.
		// We create directories on-demand when writing files.
		if header.Typeflag == tar.TypeDir {
			continue
		}

		// Normalize the path (remove leading ./ or /)
		// Use path (not filepath) since tar always uses forward slashes
		normalized := path.Clean("/" + header.Name)

		// Check if file is under our prefix
		if !strings.HasPrefix(normalized, prefix+"/") {
			continue
		}

		// Get relative path under prefix
		relPath := strings.TrimPrefix(normalized, prefix+"/")

		// Write file to destination
		destPath := filepath.Join(destDir, relPath)

		// Skip if file already exists (higher layer already wrote it)
		if _, err := os.Stat(destPath); err == nil {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", destPath, err)
		}

		if err := writeFileFromTar(tr, destPath); err != nil {
			return fmt.Errorf("failed to write %s: %w", destPath, err)
		}
	}

	return nil
}

func writeFileFromTar(tr *tar.Reader, destPath string) (err error) {
	f, err := os.Create(destPath) //nolint:gosec // path is constructed internally
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	defer func() {
		err = errors.Join(err, f.Close())
	}()

	if _, err := io.Copy(f, tr); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func sanitizeImageRef(imageRef string) string {
	// Replace characters that aren't valid in directory names
	replacer := strings.NewReplacer(
		"/", "_",
		":", "_",
		"@", "_",
	)

	return replacer.Replace(imageRef)
}

// writeManifestsWithHash writes manifest content to a file while calculating its hash.
// If manifestImageName is non-empty, it replaces occurrences with imageRef during streaming.
// Returns the sha256 hex-encoded hash of the final content.
func writeManifestsWithHash(path, content, manifestImageName, imageRef string) (_ string, err error) {
	f, err := os.Create(path) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("failed to create manifests file: %w", err)
	}

	defer func() {
		err = errors.Join(err, f.Close())
	}()

	hash := sha256.New()
	mw := io.MultiWriter(f, hash)

	if manifestImageName != "" {
		replacer := strings.NewReplacer(manifestImageName, imageRef)
		if _, err := replacer.WriteString(mw, content); err != nil {
			return "", fmt.Errorf("failed to write manifests: %w", err)
		}
	} else {
		if _, err := io.WriteString(mw, content); err != nil {
			return "", fmt.Errorf("failed to write manifests: %w", err)
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

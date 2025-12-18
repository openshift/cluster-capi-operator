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

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/yaml"
)

const (
	capiManifestsDirName = "capi-operator-manifests"
	capiManifestsDir     = "/" + capiManifestsDirName
	metadataFile         = "metadata.yaml"
	manifestsFile        = "manifests.yaml"
)

type ProviderImageManifests struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Version       string `json:"version"`
	OCPPlatform   string `json:"ocpPlatform"`
	ContentID     string `json:"contentID"`
	ManifestsPath string `json:"manifestsPath"`
}

type ProviderMetadata struct {
	ProviderName      string `json:"providerName"`
	ProviderType      string `json:"providerType"`
	ProviderVersion   string `json:"providerVersion"`
	OCPPlatform       string `json:"ocpPlatform"`
	ManifestImageName string `json:"manifestImageName"`
}

// imageFetcher abstracts fetching container images for testability.
type imageFetcher interface {
	Fetch(ctx context.Context, ref name.Reference) (v1.Image, error)
}

// remoteImageFetcher fetches images from a remote registry.
type remoteImageFetcher struct {
	keychain authn.Keychain
}

func (r remoteImageFetcher) Fetch(ctx context.Context, ref name.Reference) (v1.Image, error) {
	return remote.Image(ref, remote.WithAuthFromKeychain(r.keychain), remote.WithContext(ctx))
}

// containerImages is a map of provider names to provider image references
//
// A provider image may contain a /capi-manifests directory containing the following 2 files:
// - metadata.yaml: a YAML file whose contents are a ProviderMetadata struct
// - manifests.yaml: a KRM containing the provider manifests
//
// If a provider image does not contain a /capi-manifests directory, it is ignored.
// If a provider image contains /capi-manifests but one of the required files is missing, an error is returned.
//
// ReadProviderImages fetches each provider image. If it contains valid CAPI
// manifests, the contents are stored in a local cache directory specified by
// providerImageDir. Manifests are written to a subdirectory named after the
// image reference.
//
// When writing manifests to the cache, any occurences of `manifestImageName` as
// specified in the provider's metadata.yaml are replaced with the image
// reference.
//
// ReadProviderImages returns a map of provider names to ProviderImageManifests.

func ReadProviderImages(ctx context.Context, containerImages map[string]string, providerImageDir string, pullSecret []byte) (map[string]ProviderImageManifests, error) {
	keychain, err := parseDockerConfig(pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pull secret: %w", err)
	}
	return readProviderImages(ctx, containerImages, providerImageDir, remoteImageFetcher{keychain: keychain})
}

type providerImageResult struct {
	providerName string
	manifests    *ProviderImageManifests
	err          error
}

func readProviderImages(ctx context.Context, containerImages map[string]string, providerImageDir string, fetcher imageFetcher) (map[string]ProviderImageManifests, error) {
	results := make(chan providerImageResult, len(containerImages))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(5) // Limit to 5 concurrent fetches

	for providerName, imageRef := range containerImages {
		providerName, imageRef := providerName, imageRef // capture loop variables
		g.Go(func() error {
			manifests, err := processProviderImage(ctx, imageRef, providerImageDir, fetcher)
			results <- providerImageResult{
				providerName: providerName,
				manifests:    manifests,
				err:          err,
			}

			return nil // we're returning
		})
	}

	_ = g.Wait() // We're not actually returning errors directly
	close(results)

	ret := make(map[string]ProviderImageManifests)
	var err error
	for result := range results {
		if result.err != nil {
			err = errors.Join(err, fmt.Errorf("fetching provider %s: %w", result.providerName, result.err))
		} else if result.manifests != nil {
			ret[result.providerName] = *result.manifests
		}
	}

	if err != nil {
		return nil, err
	}
	return ret, nil
}

func processProviderImage(ctx context.Context, imageRef, providerImageDir string, fetcher imageFetcher) (*ProviderImageManifests, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image reference %s: %w", imageRef, err)
	}

	img, err := fetcher.Fetch(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch image %s: %w", imageRef, err)
	}

	// Extract files from the image
	metadata, manifestsContent, err := extractCapiManifests(img)
	if err != nil {
		if errors.Is(err, errNoCapiManifests) {
			// Image doesn't contain /capi-manifests, skip it
			return nil, nil
		}
		return nil, err
	}

	// Create output directory for this provider
	// Use a sanitized version of the image reference as the subdirectory name
	sanitizedRef := sanitizeImageRef(imageRef)
	outputDir := filepath.Join(providerImageDir, sanitizedRef)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write manifests to the cache directory, performing image substitution and hash calculation
	manifestsPath := filepath.Join(outputDir, manifestsFile)
	contentID, err := writeManifestsWithHash(manifestsPath, manifestsContent, metadata.ManifestImageName, imageRef)
	if err != nil {
		return nil, err
	}

	return &ProviderImageManifests{
		Name:          metadata.ProviderName,
		Type:          metadata.ProviderType,
		Version:       metadata.ProviderVersion,
		OCPPlatform:   metadata.OCPPlatform,
		ContentID:     contentID,
		ManifestsPath: manifestsPath,
	}, nil
}

var errNoCapiManifests = errors.New("no capi-manifests directory found")

func extractCapiManifests(img v1.Image) (*ProviderMetadata, string, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get image layers: %w", err)
	}

	var metadataContent, manifestsContent string
	// Use path (not filepath) since tar always uses forward slashes
	metadataPath := path.Join(capiManifestsDir, metadataFile)
	manifestsPath := path.Join(capiManifestsDir, manifestsFile)

	// Iterate layers in reverse order (top to bottom) since higher layers
	// overwrite files from lower layers in OCI images
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		rc, err := layer.Uncompressed()
		if err != nil {
			return nil, "", fmt.Errorf("failed to uncompress layer: %w", err)
		}

		found, err := extractFilesFromTar(rc, metadataPath, manifestsPath)
		rc.Close()
		if err != nil {
			return nil, "", err
		}

		if content, ok := found[metadataPath]; ok {
			metadataContent = content
		}
		if content, ok := found[manifestsPath]; ok {
			manifestsContent = content
		}

		// Early exit once both files are found
		if metadataContent != "" && manifestsContent != "" {
			break
		}
	}

	if metadataContent == "" && manifestsContent == "" {
		return nil, "", errNoCapiManifests
	}

	if metadataContent == "" {
		return nil, "", fmt.Errorf("missing %s in %s", metadataFile, capiManifestsDir)
	}
	if manifestsContent == "" {
		return nil, "", fmt.Errorf("missing %s in %s", manifestsFile, capiManifestsDir)
	}

	var metadata ProviderMetadata
	if err := yaml.Unmarshal([]byte(metadataContent), &metadata); err != nil {
		return nil, "", fmt.Errorf("failed to parse metadata.yaml: %w", err)
	}

	return &metadata, manifestsContent, nil
}

func extractFilesFromTar(r io.Reader, paths ...string) (map[string]string, error) {
	tr := tar.NewReader(r)
	results := make(map[string]string)
	pathSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		pathSet[p] = struct{}{}
	}

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar: %w", err)
		}

		// Normalize the path (remove leading ./ or /)
		// Use path (not filepath) since tar always uses forward slashes
		normalized := path.Clean("/" + header.Name)

		if _, want := pathSet[normalized]; want {
			content, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("failed to read file %s: %w", normalized, err)
			}
			results[normalized] = string(content)

			// Early exit if all files found
			if len(results) == len(paths) {
				break
			}
		}
	}

	return results, nil
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
func writeManifestsWithHash(path, content, manifestImageName, imageRef string) (string, error) {
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("failed to create manifests file: %w", err)
	}
	defer f.Close()

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

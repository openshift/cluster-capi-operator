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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-logr/logr"
	"github.com/openshift/cluster-capi-operator/manifests-gen/providermetadata"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"
)

const (
	metadataFile             = "metadata.yaml"
	manifestsFile            = "manifests.yaml"
	capiOperatorManifestsDir = "capi-operator-manifests"
	// ProviderImageMountBase is the base path where provider image volumes are mounted.
	ProviderImageMountBase = "/var/lib/provider-images"

	// AttributeKeyType is the key for the provider type attribute.
	AttributeKeyType = providermetadata.AttributeKeyType
	// AttributeKeyVersion is the key for the provider version attribute.
	AttributeKeyVersion = providermetadata.AttributeKeyVersion
)

// ProviderImageManifests represents metadata and manifests read from a provider image.
type ProviderImageManifests struct {
	ProviderMetadata

	ImageRef string
	Profile  string

	ManifestsPath string
}

// ProviderMetadata is metadata about a provider image provided in the metadata.yaml file.
type ProviderMetadata = providermetadata.ProviderMetadata

var (
	errNoCapiManifests   = errors.New("no capi-manifests directory found")
	errMissingMetadata   = errors.New("missing metadata.yaml in /capi-operator-manifests")
	errMissingManifests  = errors.New("missing manifests.yaml in /capi-operator-manifests")
	errContainerNotFound = errors.New("container not found in pod spec")
)

// ScanProviderImages scans providerImageDir for subdirectories containing
// provider profiles (metadata.yaml + manifests.yaml). imageRefMap maps
// expected subdirectory names to image references.
func ScanProviderImages(logger logr.Logger, providerImageDir string, imageRefMap map[string]string) ([]ProviderImageManifests, error) {
	var result []ProviderImageManifests

	for _, subdir := range sets.List(sets.KeySet(imageRefMap)) {
		subdirPath := filepath.Join(providerImageDir, subdir)

		info, err := os.Stat(subdirPath)
		if err != nil {
			if os.IsNotExist(err) {
				logger.Info("Skipping provider directory: expected directory does not exist", "directory", subdir)
				continue
			}

			return nil, fmt.Errorf("failed to stat provider image directory %s: %w", subdirPath, err)
		}

		if !info.IsDir() {
			logger.Info("Skipping provider directory: expected path is not a directory", "directory", subdir)
			continue
		}

		profiles, err := discoverProfiles(subdirPath)
		if err != nil {
			if errors.Is(err, errNoCapiManifests) {
				logger.Info("Skipping provider directory: no valid profiles found", "directory", subdir)
				continue
			}

			return nil, fmt.Errorf("failed to discover profiles in %s: %w", subdirPath, err)
		}

		imageRef := imageRefMap[subdir]

		for _, profile := range profiles {
			manifestsPath := filepath.Join(subdirPath, capiOperatorManifestsDir, profile.Profile, manifestsFile)

			result = append(result, ProviderImageManifests{
				ProviderMetadata: *profile.Metadata,
				ImageRef:         imageRef,
				Profile:          profile.Profile,
				ManifestsPath:    manifestsPath,
			})
		}
	}

	return result, nil
}

// BuildImageRefMapFromRefs builds a mapping from expected mount subdirectory
// names to image references.
func BuildImageRefMapFromRefs(imageRefs sets.Set[string]) map[string]string {
	imageRefMap := make(map[string]string, imageRefs.Len())

	for _, imageRef := range sets.List(imageRefs) {
		imageRefMap[VolumeNameForImageRef(imageRef)] = imageRef
	}

	return imageRefMap
}

// BuildImageRefMap builds a mapping from mount subdirectory names to image
// references by correlating image volumes with their volume mounts for the
// named container in the given PodSpec.
func BuildImageRefMap(podSpec corev1.PodSpec, containerName string) (map[string]string, error) {
	volumeImageRefs := make(map[string]string)

	for i := range podSpec.Volumes {
		v := &podSpec.Volumes[i]
		if v.Image != nil && v.Image.Reference != "" {
			volumeImageRefs[v.Name] = v.Image.Reference
		}
	}

	imageRefMap := make(map[string]string)

	for i := range podSpec.Containers {
		c := &podSpec.Containers[i]
		if c.Name != containerName {
			continue
		}

		for j := range c.VolumeMounts {
			vm := &c.VolumeMounts[j]
			if imageRef, ok := volumeImageRefs[vm.Name]; ok {
				subdirName := filepath.Base(vm.MountPath)
				imageRefMap[subdirName] = imageRef
			}
		}

		return imageRefMap, nil
	}

	return nil, fmt.Errorf("container %q: %w", containerName, errContainerNotFound)
}

// VolumeNameForImageRef generates a deterministic, DNS-label-safe volume name
// from an image reference. The volume name consists of a prefix derived from
// the image name and a short hash of the full image reference.
func VolumeNameForImageRef(imageRef string) string {
	parts := strings.Split(imageRef, "@")
	if len(parts) == 0 {
		parts = []string{imageRef}
	}

	pathParts := strings.Split(parts[0], "/")
	imageName := pathParts[len(pathParts)-1]

	imageName = strings.ToLower(imageName)
	imageName = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}

		return '-'
	}, imageName)

	hash := sha256.Sum256([]byte(imageRef))
	shortHash := hex.EncodeToString(hash[:])[:8]

	volumeName := fmt.Sprintf("%s-%s", imageName, shortHash)

	if len(volumeName) > 0 && (volumeName[0] < 'a' || volumeName[0] > 'z') && (volumeName[0] < '0' || volumeName[0] > '9') {
		volumeName = "img-" + volumeName
	}

	return volumeName
}

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
	entries, err := os.ReadDir(filepath.Join(dir, capiOperatorManifestsDir))
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
		profileDir := filepath.Join(dir, capiOperatorManifestsDir, profileName)

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

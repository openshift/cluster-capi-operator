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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openshift/cluster-capi-operator/manifests-gen/providermetadata"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	metadataFile  = "metadata.yaml"
	manifestsFile = "manifests.yaml"

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
	errNoCapiManifests  = errors.New("no capi-manifests directory found")
	errMissingMetadata  = errors.New("missing metadata.yaml in /capi-operator-manifests")
	errMissingManifests = errors.New("missing manifests.yaml in /capi-operator-manifests")
	errImageRefNotFound = errors.New("image ref not found for provider")
)

// ScanProviderImages scans providerImageDir for subdirectories containing
// provider profiles (metadata.yaml + manifests.yaml). imageRefMap maps
// subdirectory names to image references (built from the pod spec).
func ScanProviderImages(providerImageDir string, imageRefMap map[string]string) ([]ProviderImageManifests, error) {
	entries, err := os.ReadDir(providerImageDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read provider image directory %s: %w", providerImageDir, err)
	}

	var result []ProviderImageManifests

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		subdir := entry.Name()
		subdirPath := filepath.Join(providerImageDir, subdir)

		profiles, err := discoverProfiles(subdirPath)
		if err != nil {
			if errors.Is(err, errNoCapiManifests) {
				continue
			}

			return nil, fmt.Errorf("failed to discover profiles in %s: %w", subdirPath, err)
		}

		imageRef := imageRefMap[subdir]

		for _, profile := range profiles {
			// If the provider has profiles but no image ref, return an error
			// instead of a provider with an empty image ref.
			if imageRef == "" {
				return nil, fmt.Errorf("%w: %s", errImageRefNotFound, subdir)
			}

			manifestsPath := filepath.Join(subdirPath, profile.Profile, manifestsFile)

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

// BuildImageRefMapFromPod reads the given pod's spec to build a mapping
// from mount subdirectory names to image references. It correlates image
// volumes with their volume mounts to determine which image is mounted where.
func BuildImageRefMapFromPod(ctx context.Context, k8sClient client.Reader, podName, podNamespace string) (map[string]string, error) {
	var pod corev1.Pod
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: podName, Namespace: podNamespace}, &pod); err != nil {
		return nil, fmt.Errorf("failed to get pod %s/%s: %w", podNamespace, podName, err)
	}

	// Build volume name → image reference map
	volumeImageRefs := make(map[string]string)

	for i := range pod.Spec.Volumes {
		v := &pod.Spec.Volumes[i]
		if v.Image != nil && v.Image.Reference != "" {
			volumeImageRefs[v.Name] = v.Image.Reference
		}
	}

	// Correlate volume mounts with image references
	imageRefMap := make(map[string]string)

	for i := range pod.Spec.Containers {
		c := &pod.Spec.Containers[i]
		for j := range c.VolumeMounts {
			vm := &c.VolumeMounts[j]
			if imageRef, ok := volumeImageRefs[vm.Name]; ok {
				subdirName := filepath.Base(vm.MountPath)
				imageRefMap[subdirName] = imageRef
			}
		}
	}

	return imageRefMap, nil
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

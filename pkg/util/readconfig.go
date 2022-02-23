package util

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"gopkg.in/yaml.v2"
)

// ReadImagesFile reads the images file and returns the map of container images
func ReadImagesFile(imagesFile string) (map[string]string, error) {
	jsonData, err := ioutil.ReadFile(filepath.Clean(imagesFile))
	if err != nil {
		return nil, fmt.Errorf("unable to read file %s: %w", imagesFile, err)
	}

	containerImages := map[string]string{}
	if err := json.Unmarshal(jsonData, &containerImages); err != nil {
		return nil, fmt.Errorf("unable to unmarshal image names from file %s: %w", imagesFile, err)
	}

	return containerImages, nil
}

type provider struct {
	Name string `json:"name"`
}

// ReadProvidersFile reads the providers file and returns the map of supported providers
func ReadProvidersFile(providersFile string) (map[string]bool, error) {
	yamlData, err := ioutil.ReadFile(filepath.Clean(providersFile))
	if err != nil {
		return nil, fmt.Errorf("unable to read file %s", providersFile)
	}

	providers := []provider{}
	if err := yaml.Unmarshal(yamlData, &providers); err != nil {
		return nil, fmt.Errorf("unable to unmarshal providers names from file %s", providersFile)
	}

	supportedProviders := map[string]bool{}
	for _, p := range providers {
		// Skip core cluster-api because it is not an infrastructure provider
		if p.Name == "cluster-api" {
			continue
		}
		supportedProviders[p.Name] = true
	}

	return supportedProviders, nil
}

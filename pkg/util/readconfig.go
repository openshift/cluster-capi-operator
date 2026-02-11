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
package util

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReadImagesFile reads the images file and returns the map of container images.
func ReadImagesFile(imagesFile string) (map[string]string, error) {
	jsonData, err := os.ReadFile(filepath.Clean(imagesFile))
	if err != nil {
		return nil, fmt.Errorf("unable to read file %s: %w", imagesFile, err)
	}

	containerImages := map[string]string{}
	if err := json.Unmarshal(jsonData, &containerImages); err != nil {
		return nil, fmt.Errorf("unable to unmarshal image names from file %s: %w", imagesFile, err)
	}

	return containerImages, nil
}

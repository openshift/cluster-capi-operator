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
	"fmt"
	"strings"

	"github.com/drone/envsubst/v2"

	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
)

func envSubstSubstitutions(key string) string {
	switch key {
	// Used only in the AWS provider. Eventually, we intend to move this into
	// provider metadata.
	case "EXP_BOOTSTRAP_FORMAT_IGNITION":
		return "true"
	default:
		return ""
	}
}

func transformYaml(providerProfile *providerimages.ProviderImageManifests, yaml string) (string, error) {
	// Expand envsubst variables
	yaml, err := envsubst.Eval(yaml, envSubstSubstitutions)
	if err != nil {
		return "", fmt.Errorf("failed to substitute variables: %w", err)
	}

	// Replace self-image-ref with actual image ref.
	if providerProfile.SelfImageRef != "" {
		yaml = strings.ReplaceAll(yaml, providerProfile.SelfImageRef, providerProfile.ImageRef)
	}

	return yaml, nil
}

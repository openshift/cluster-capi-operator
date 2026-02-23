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
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
)

func TestTransformYaml(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		profile  providerimages.ProviderImageManifests
		expected string
	}{
		{
			name: "known envsubst variable replaced",
			yaml: "bootstrap: ${EXP_BOOTSTRAP_FORMAT_IGNITION}",
			profile: providerimages.ProviderImageManifests{
				ImageRef: "example.com/img@sha256:abc",
			},
			expected: "bootstrap: true",
		},
		{
			name: "unknown variable replaced with empty",
			yaml: "value: ${UNKNOWN_VAR}",
			profile: providerimages.ProviderImageManifests{
				ImageRef: "example.com/img@sha256:abc",
			},
			expected: "value: ",
		},
		{
			name: "no variables passes through unchanged",
			yaml: "apiVersion: v1\nkind: ConfigMap",
			profile: providerimages.ProviderImageManifests{
				ImageRef: "example.com/img@sha256:abc",
			},
			expected: "apiVersion: v1\nkind: ConfigMap",
		},
		{
			name: "self image ref replaced",
			yaml: "image: placeholder-ref",
			profile: providerimages.ProviderImageManifests{
				ProviderMetadata: providerimages.ProviderMetadata{
					SelfImageRef: "placeholder-ref",
				},
				ImageRef: "real-ref",
			},
			expected: "image: real-ref",
		},
		{
			name: "self image ref all occurrences",
			yaml: "a: old-ref\nb: old-ref",
			profile: providerimages.ProviderImageManifests{
				ProviderMetadata: providerimages.ProviderMetadata{
					SelfImageRef: "old-ref",
				},
				ImageRef: "new-ref",
			},
			expected: "a: new-ref\nb: new-ref",
		},
		{
			name: "empty self image ref skips replacement",
			yaml: "image: something",
			profile: providerimages.ProviderImageManifests{
				ImageRef: "new-ref",
			},
			expected: "image: something",
		},
		{
			name: "both transformations applied",
			yaml: "format: ${EXP_BOOTSTRAP_FORMAT_IGNITION}\nimage: old-ref",
			profile: providerimages.ProviderImageManifests{
				ProviderMetadata: providerimages.ProviderMetadata{
					SelfImageRef: "old-ref",
				},
				ImageRef: "new-ref",
			},
			expected: "format: true\nimage: new-ref",
		},
		{
			name: "envsubst applied before image replacement",
			yaml: "image: ${EXP_BOOTSTRAP_FORMAT_IGNITION}",
			profile: providerimages.ProviderImageManifests{
				ProviderMetadata: providerimages.ProviderMetadata{
					SelfImageRef: "true",
				},
				ImageRef: "replaced",
			},
			expected: "image: replaced",
		},
		{
			name: "empty yaml",
			yaml: "",
			profile: providerimages.ProviderImageManifests{
				ImageRef: "example.com/img@sha256:abc",
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result, err := transformYaml(&tt.profile, tt.yaml)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

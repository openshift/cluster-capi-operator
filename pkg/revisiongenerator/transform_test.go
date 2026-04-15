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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
)

func TestTransformYaml(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		profile       providerimages.ProviderImageManifests
		substitutions map[string]string
		expected      string
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
		{
			name: "user substitution applied",
			yaml: "version: ${TLS_MIN_VERSION}",
			profile: providerimages.ProviderImageManifests{
				ImageRef: "example.com/img@sha256:abc",
			},
			substitutions: map[string]string{"TLS_MIN_VERSION": "VersionTLS12"},
			expected:      "version: VersionTLS12",
		},
		{
			name: "user substitution overrides hardcoded",
			yaml: "bootstrap: ${EXP_BOOTSTRAP_FORMAT_IGNITION}",
			profile: providerimages.ProviderImageManifests{
				ImageRef: "example.com/img@sha256:abc",
			},
			substitutions: map[string]string{"EXP_BOOTSTRAP_FORMAT_IGNITION": "false"},
			expected:      "bootstrap: false",
		},
		{
			name: "unknown var with no user substitution still empty",
			yaml: "value: ${UNKNOWN_VAR}",
			profile: providerimages.ProviderImageManifests{
				ImageRef: "example.com/img@sha256:abc",
			},
			substitutions: map[string]string{"OTHER_VAR": "something"},
			expected:      "value: ",
		},
		{
			name: "multiple substitutions applied",
			yaml: "version: ${TLS_MIN_VERSION}\nciphers: ${TLS_CIPHER_SUITES}",
			profile: providerimages.ProviderImageManifests{
				ImageRef: "example.com/img@sha256:abc",
			},
			substitutions: map[string]string{
				"TLS_MIN_VERSION":   "VersionTLS12",
				"TLS_CIPHER_SUITES": "TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384",
			},
			expected: "version: VersionTLS12\nciphers: TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result, err := transformYaml(&tt.profile, tt.yaml, tt.substitutions)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestTransformObject(t *testing.T) {
	tests := []struct {
		name          string
		labels        map[string]string
		componentName string
	}{
		{
			name:          "adds managed label to object with no labels",
			labels:        nil,
			componentName: "core",
		},
		{
			name:          "preserves existing labels",
			labels:        map[string]string{"existing-key": "existing-value"},
			componentName: "infra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			obj := unstructured.Unstructured{}
			obj.SetLabels(tt.labels)

			result := transformObject(obj, tt.componentName)

			g.Expect(result.GetLabels()).To(HaveKeyWithValue(ManagedLabelKey, tt.componentName))

			for k, v := range tt.labels {
				g.Expect(result.GetLabels()).To(HaveKeyWithValue(k, v))
			}
		})
	}
}

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

package test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// ProviderImageManifestsBuilder constructs a ProviderImageManifests for testing with sensible defaults.
// Manifest files are written to tb.TempDir() and cleaned up automatically.
type ProviderImageManifestsBuilder struct {
	tb           testing.TB
	name         string
	imageRef     string
	profile      string
	contentID    string
	installOrder int
	platform     configv1.PlatformType
	attributes   map[string]string
	selfImageRef string
	manifests    []string
}

// NewProviderImageManifests returns a builder with sensible defaults for a test provider.
func NewProviderImageManifests(tb testing.TB, name string) *ProviderImageManifestsBuilder {
	return &ProviderImageManifestsBuilder{
		tb:           tb,
		name:         name,
		imageRef:     fmt.Sprintf("registry.example.com/%s@sha256:%s", name, SHA256Pad(name)),
		profile:      "default",
		contentID:    fmt.Sprintf("%s-content-id", name),
		installOrder: 10,
	}
}

// WithImageRef sets a custom image reference.
func (b *ProviderImageManifestsBuilder) WithImageRef(ref string) *ProviderImageManifestsBuilder {
	b.imageRef = ref
	return b
}

// WithProfile sets a custom profile name.
func (b *ProviderImageManifestsBuilder) WithProfile(profile string) *ProviderImageManifestsBuilder {
	b.profile = profile
	return b
}

// WithContentID sets a custom content ID.
func (b *ProviderImageManifestsBuilder) WithContentID(id string) *ProviderImageManifestsBuilder {
	b.contentID = id
	return b
}

// WithInstallOrder sets a custom install order.
func (b *ProviderImageManifestsBuilder) WithInstallOrder(order int) *ProviderImageManifestsBuilder {
	b.installOrder = order
	return b
}

// WithPlatform sets the OCP platform type.
func (b *ProviderImageManifestsBuilder) WithPlatform(platform configv1.PlatformType) *ProviderImageManifestsBuilder {
	b.platform = platform
	return b
}

// WithAttributes merges the given attributes into the provider attributes map.
func (b *ProviderImageManifestsBuilder) WithAttributes(attrs map[string]string) *ProviderImageManifestsBuilder {
	if b.attributes == nil {
		b.attributes = attrs
	} else {
		maps.Copy(b.attributes, attrs)
	}

	return b
}

// WithSelfImageRef sets the self image reference.
func (b *ProviderImageManifestsBuilder) WithSelfImageRef(ref string) *ProviderImageManifestsBuilder {
	b.selfImageRef = ref
	return b
}

// WithManifests appends the given YAML documents to the manifest list.
func (b *ProviderImageManifestsBuilder) WithManifests(docs ...string) *ProviderImageManifestsBuilder {
	b.manifests = append(b.manifests, docs...)
	return b
}

// Build constructs the ProviderImageManifests, writing any manifests to a temp file.
func (b *ProviderImageManifestsBuilder) Build() providerimages.ProviderImageManifests {
	b.tb.Helper()

	p := providerimages.ProviderImageManifests{
		ProviderMetadata: providerimages.ProviderMetadata{
			Name:         b.name,
			InstallOrder: b.installOrder,
			OCPPlatform:  b.platform,
			Attributes:   b.attributes,
			SelfImageRef: b.selfImageRef,
		},
		ContentID: b.contentID,
		ImageRef:  b.imageRef,
		Profile:   b.profile,
	}

	if len(b.manifests) > 0 {
		content := MultiDoc(b.manifests...)

		dir := b.tb.TempDir()
		path := filepath.Join(dir, b.name+"-manifests.yaml")

		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			b.tb.Fatalf("writing manifest file: %v", err)
		}

		p.ManifestsPath = path
	}

	return p
}

// ConfigMapYAML returns a ConfigMap YAML document. If no data map is provided,
// a default of {"key": "value"} is used.
func ConfigMapYAML(name string, data ...map[string]string) string {
	d := map[string]string{"key": "value"}
	if len(data) > 0 {
		d = data[0]
	}

	var pairs strings.Builder
	for k, v := range d {
		fmt.Fprintf(&pairs, "  %s: %s\n", k, v)
	}

	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: default
data:
%s`, name, strings.TrimRight(pairs.String(), "\n"))
}

// ClusterRoleYAML returns a minimal ClusterRole YAML document.
func ClusterRoleYAML(name string) string {
	return fmt.Sprintf(`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: %s
rules: []`, name)
}

// NamespaceYAML returns a Namespace YAML document.
func NamespaceYAML(name string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s`, name)
}

// CRDToYAML marshals a CRD object to YAML. Use with GenerateCRD or
// GenerateSchemalessSpecStatusCRD from crdbuilder.go to create CRD fixtures
// for use with WithManifests.
func CRDToYAML(crd *apiextensionsv1.CustomResourceDefinition) string {
	out, err := yaml.Marshal(crd)
	if err != nil {
		panic(fmt.Sprintf("marshalling CRD to YAML: %v", err))
	}

	return string(out)
}

// DeploymentYAML returns a minimal Deployment YAML document with a single replica.
func DeploymentYAML(name string) string {
	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: test
        image: registry.example.com/test:latest`, name, name, name)
}

// SHA256Pad creates a deterministic 64-char hex string from a name (for fake digests).
func SHA256Pad(name string) string {
	if name == "" {
		// Otherwise we'll get into an infinite loop.
		panic("SHA256Pad: name is required")
	}

	padded := name
	for len(padded) < 64 {
		padded += name
	}

	var hex strings.Builder
	for _, c := range padded[:64] {
		fmt.Fprintf(&hex, "%02x", c)
	}

	return hex.String()[:64]
}

// MultiDoc joins YAML documents with the standard separator.
func MultiDoc(docs ...string) string {
	return strings.Join(docs, "\n---\n")
}

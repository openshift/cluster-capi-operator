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
	"os"
	"path/filepath"
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// ProviderOption configures a test provider created by NewTestProvider.
type ProviderOption func(*providerConfig, string)

type providerConfig struct {
	imageRef     string
	profile      string
	contentID    string
	installOrder int
	platform     configv1.PlatformType
	attributes   map[string]string
	selfImageRef string
	manifests    []string
}

// WithImageRef sets a custom image reference.
func WithImageRef(ref string) ProviderOption {
	return func(c *providerConfig, _ string) { c.imageRef = ref }
}

// WithProfile sets a custom profile name.
func WithProfile(profile string) ProviderOption {
	return func(c *providerConfig, _ string) { c.profile = profile }
}

// WithContentID sets a custom content ID.
func WithContentID(id string) ProviderOption {
	return func(c *providerConfig, _ string) { c.contentID = id }
}

// WithInstallOrder sets a custom install order.
func WithInstallOrder(order int) ProviderOption {
	return func(c *providerConfig, _ string) { c.installOrder = order }
}

// WithPlatform sets the OCP platform type.
func WithPlatform(platform configv1.PlatformType) ProviderOption {
	return func(c *providerConfig, _ string) { c.platform = platform }
}

// WithAttributes sets the provider attributes map.
func WithAttributes(attrs map[string]string) ProviderOption {
	return func(c *providerConfig, _ string) { c.attributes = attrs }
}

// WithSelfImageRef sets the self image reference.
func WithSelfImageRef(ref string) ProviderOption {
	return func(c *providerConfig, _ string) { c.selfImageRef = ref }
}

// WithManifests writes the given YAML documents (joined with "---") to the manifest file.
func WithManifests(docs ...string) ProviderOption {
	return func(c *providerConfig, _ string) {
		c.manifests = append(c.manifests, docs...)
	}
}

// NewTestProvider creates a ProviderImageManifests for testing with sensible defaults.
// Manifest files are written to tb.TempDir() and cleaned up automatically.
func NewTestProvider(tb testing.TB, name string, opts ...ProviderOption) providerimages.ProviderImageManifests {
	tb.Helper()

	cfg := &providerConfig{
		imageRef:     fmt.Sprintf("registry.example.com/%s@sha256:%s", name, SHA256Pad(name)),
		profile:      "default",
		contentID:    fmt.Sprintf("%s-content-id", name),
		installOrder: 10,
	}

	for _, opt := range opts {
		opt(cfg, name)
	}

	p := providerimages.ProviderImageManifests{
		ProviderMetadata: providerimages.ProviderMetadata{
			Name:         name,
			InstallOrder: cfg.installOrder,
			OCPPlatform:  cfg.platform,
			Attributes:   cfg.attributes,
			SelfImageRef: cfg.selfImageRef,
		},
		ContentID: cfg.contentID,
		ImageRef:  cfg.imageRef,
		Profile:   cfg.profile,
	}

	if len(cfg.manifests) > 0 {
		content := MultiDoc(cfg.manifests...)

		dir := tb.TempDir()
		path := filepath.Join(dir, name+"-manifests.yaml")

		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			tb.Fatalf("writing manifest file: %v", err)
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

// CRDAsYAML marshals a CRD object to YAML. Use with GenerateCRD or
// GenerateSchemalessSpecStatusCRD from crdbuilder.go to create CRD fixtures
// for use with WithManifests.
func CRDAsYAML(crd *apiextensionsv1.CustomResourceDefinition) string {
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

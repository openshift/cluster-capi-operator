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
package createrequirements

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/cluster-capi-operator/pkg/providerimages"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRequirements(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Upgradeable Requirements")
}

var _ = Describe("CRD discovery", func() {
	var root string

	writeProfile := func(profile, data string) {
		dir := filepath.Join(root, providerimages.VolumeNameForImageRef("provider:latest"), "capi-operator-manifests", profile)
		Expect(os.MkdirAll(dir, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "manifests.yaml"), []byte(data), 0600)).To(Succeed())
	}

	BeforeEach(func() { root = GinkgoT().TempDir() })
	It("should aggregate profiles and keep the first alphabetical occurrence", func() {
		writeProfile("z-last", "kind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: last.example.com\n---\nkind: CustomResourceDefinition\nmetadata:\n  name: others.example.com\n")
		writeProfile("a-first", "kind: ConfigMap\nmetadata:\n  name: ignored\n---\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: first.example.com\n")

		crds, err := DiscoverCRDs(root, []string{"provider:latest"})
		Expect(err).NotTo(HaveOccurred())
		Expect(crds).To(HaveLen(2))
		Expect(crds["widgets.example.com"].Spec.Group).To(Equal("first.example.com"))
	})
	It("should reject malformed YAML even after discovering valid CRDs", func() {
		writeProfile("a-first", "kind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\n")
		writeProfile("z-last", "kind: [unterminated")

		_, err := DiscoverCRDs(root, []string{"provider:latest"})
		Expect(err).To(HaveOccurred())
	})
	It("should reject an empty profile set", func() {
		_, err := DiscoverCRDs(root, nil)
		Expect(err).To(MatchError("no provider profiles discovered"))
	})
	It("should reject requirement names that exceed the API limit", func() {
		_, err := BuildRequirement(&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: strings.Repeat("a", 253)}})
		Expect(err).To(HaveOccurred())
	})
})

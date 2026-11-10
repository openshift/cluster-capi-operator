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
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

func TestRequirements(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Upgradeable Requirements")
}

var _ = Describe("CRD discovery", func() {
	var root string

	// profileMetadata is valid metadata.yaml content for a discovered profile.
	const profileMetadata = "name: provider\nselfImageRef: provider:latest\ninstallOrder: 10\nattributes:\n  type: infrastructure\n  version: v1.0.0\n"

	writeProfile := func(profile, data string) {
		dir := filepath.Join(root, providerimages.VolumeNameForImageRef("provider:latest"), "capi-operator-manifests", profile)
		Expect(os.MkdirAll(dir, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "metadata.yaml"), []byte(profileMetadata), 0600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "manifests.yaml"), []byte(data), 0600)).To(Succeed())
	}

	writeStubImage := func() {
		Expect(os.MkdirAll(filepath.Join(root, providerimages.VolumeNameForImageRef("placeholder:latest"), "some-other-dir"), 0700)).To(Succeed())
	}

	BeforeEach(func() { root = GinkgoT().TempDir() })
	It("should aggregate profiles and keep the first alphabetical occurrence", func() {
		writeProfile("z-last", "kind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: last.example.com\n---\nkind: CustomResourceDefinition\nmetadata:\n  name: others.example.com\n")
		writeProfile("a-first", "kind: ConfigMap\nmetadata:\n  name: ignored\n---\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\nspec:\n  group: first.example.com\n")

		crds, err := DiscoverCRDs(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(crds).To(HaveLen(2))
		Expect(crds["widgets.example.com"].Spec.Group).To(Equal("first.example.com"))
	})
	It("should reject malformed YAML even after discovering valid CRDs", func() {
		writeProfile("a-first", "kind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\n")
		writeProfile("z-last", "kind: [unterminated")

		_, err := DiscoverCRDs(root)
		Expect(err).To(HaveOccurred())
	})
	It("should assess an empty CRD set when no provider image ships manifests", func() {
		crds, err := DiscoverCRDs(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(crds).To(BeEmpty())
	})
	It("should skip a stub provider image and assess the remaining images", func() {
		writeStubImage()
		writeProfile("default", "kind: CustomResourceDefinition\nmetadata:\n  name: widgets.example.com\n")

		crds, err := DiscoverCRDs(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(crds).To(HaveKey("widgets.example.com"))
	})
	It("should assess an empty CRD set when the mount root does not exist", func() {
		crds, err := DiscoverCRDs(filepath.Join(root, "absent"))
		Expect(err).NotTo(HaveOccurred())
		Expect(crds).To(BeEmpty())
	})
	It("should fail on an incomplete profile", func() {
		dir := filepath.Join(root, providerimages.VolumeNameForImageRef("provider:latest"), "capi-operator-manifests", "default")
		Expect(os.MkdirAll(dir, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "manifests.yaml"), []byte("kind: ConfigMap\n"), 0600)).To(Succeed())

		_, err := DiscoverCRDs(root)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Requirement names", func() {
	// A human readable name for this CRD name is exactly the maximum permitted length.
	longestReadable := strings.Join([]string{
		strings.Repeat("a", 63), strings.Repeat("b", 63), strings.Repeat("c", 63), strings.Repeat("d", 35),
	}, ".")
	// One character longer forces the hash fallback.
	tooLong := longestReadable + "e"

	It("should prefer the human readable CRD name", func() {
		Expect(RequirementName("widgets.example.com")).To(Equal(RequirementNamePrefix + "widgets.example.com"))
		Expect(RequirementName(longestReadable)).To(Equal(RequirementNamePrefix + longestReadable))
		Expect(RequirementName(longestReadable)).To(HaveLen(validation.DNS1123SubdomainMaxLength))
	})
	It("should fall back to a deterministic hash when the readable name is too long", func() {
		name := RequirementName(tooLong)
		Expect(name).To(HavePrefix(RequirementNamePrefix))
		Expect(name).To(HaveLen(len(RequirementNamePrefix) + 64))
		Expect(validation.IsDNS1123Subdomain(name)).To(BeEmpty())
		Expect(RequirementName(tooLong)).To(Equal(name))
		Expect(RequirementName(tooLong + "f")).NotTo(Equal(name))
	})
	It("should build a requirement with the same name", func() {
		requirement, err := BuildRequirement(&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: tooLong}})
		Expect(err).NotTo(HaveOccurred())
		Expect(requirement.Name).To(Equal(RequirementName(tooLong)))
	})
})

var _ = Describe("Requirement adoption", func() {
	annotationsFor := func(value string) map[string]string {
		GinkgoHelper()

		crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "widgets.example.com"}}
		if value != "" {
			crd.Annotations = map[string]string{revisiongenerator.AdoptExistingAnnotation: value}
		}

		requirement, err := BuildRequirement(crd)
		Expect(err).NotTo(HaveOccurred())

		return requirement.Annotations
	}

	It("should carry the adopt-existing declaration of the assessed CRD", func() {
		Expect(annotationsFor(revisiongenerator.AdoptExistingAlways)).To(
			HaveKeyWithValue(revisiongenerator.AdoptExistingAnnotation, revisiongenerator.AdoptExistingAlways))
	})

	DescribeTable("should not carry an annotation that does not declare adoption",
		func(value string) {
			Expect(annotationsFor(value)).NotTo(HaveKey(revisiongenerator.AdoptExistingAnnotation))
		},
		Entry("without the annotation", ""),
		Entry("when adoption is declined", revisiongenerator.AdoptExistingNever),
		Entry("when the value is unrecognised", "sometimes"),
	)
})

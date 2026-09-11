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

package installer

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

func toUnstructuredCRD(crd *apiextensionsv1.CustomResourceDefinition) (*unstructured.Unstructured, error) {
	crd.SetGroupVersionKind(schema.GroupVersionKind{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"})
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(crd)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: data}, nil
}

var _ = Describe("buildCompatibilityRequirement", func() {
	var crd *unstructured.Unstructured

	BeforeEach(func() {
		typed := test.GenerateSchemalessSpecStatusCRD(testCRDGVK)
		u, err := toUnstructuredCRD(typed)
		Expect(err).NotTo(HaveOccurred())
		crd = u
	})

	It("should set the name with ccapio- prefix", func() {
		cr, err := buildCompatibilityRequirement(crd)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.GetName()).To(Equal("ccapio-" + crd.GetName()))
	})

	It("should set the correct GVK", func() {
		cr, err := buildCompatibilityRequirement(crd)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.GroupVersionKind()).To(Equal(schema.GroupVersionKind{
			Group:   "apiextensions.openshift.io",
			Version: "v1alpha1",
			Kind:    "CompatibilityRequirement",
		}))
	})

	It("should embed the CRD YAML in the spec", func() {
		cr, err := buildCompatibilityRequirement(crd)
		Expect(err).NotTo(HaveOccurred())

		data, found, err := unstructured.NestedString(cr.Object, "spec", "compatibilitySchema", "customResourceDefinition", "data")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(data).To(ContainSubstring(crd.GetName()))
	})

	It("should set defaultSelection to StorageOnly", func() {
		cr, err := buildCompatibilityRequirement(crd)
		Expect(err).NotTo(HaveOccurred())

		selection, found, err := unstructured.NestedString(cr.Object, "spec", "compatibilitySchema", "requiredVersions", "defaultSelection")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(selection).To(Equal("StorageOnly"))
	})

	It("should set action to Deny on both validation fields", func() {
		cr, err := buildCompatibilityRequirement(crd)
		Expect(err).NotTo(HaveOccurred())

		crdAction, _, _ := unstructured.NestedString(cr.Object, "spec", "customResourceDefinitionSchemaValidation", "action")
		Expect(crdAction).To(Equal("Deny"))

		objAction, _, _ := unstructured.NestedString(cr.Object, "spec", "objectSchemaValidation", "action")
		Expect(objAction).To(Equal("Deny"))
	})

	It("should set the namespace selector to openshift-cluster-api", func() {
		cr, err := buildCompatibilityRequirement(crd)
		Expect(err).NotTo(HaveOccurred())

		labels, found, err := unstructured.NestedStringMap(cr.Object, "spec", "objectSchemaValidation", "namespaceSelector", "matchLabels")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(labels).To(HaveKeyWithValue("kubernetes.io/metadata.name", "openshift-cluster-api"))
	})
})

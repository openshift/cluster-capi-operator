/*
Copyright 2025 Red Hat, Inc.

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

package crdchecker

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCRDChecker(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CRDChecker Suite")
}

type crdMutator func(*apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition

// getValidBaseCRD returns a valid CRD that can be used as the base for all tests
func getValidBaseCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apiextensions.k8s.io/v1",
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "tests.example.com",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "Test",
				ListKind: "TestList",
				Plural:   "tests",
				Singular: "test",
			},
			Scope: apiextensionsv1.ClusterScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: false,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"field1": {
											Type: "string",
										},
										"field2": {
											Type: "integer",
										},
									},
								},
							},
						},
					},
				},
				{
					Name:    "v2",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"field1": {
											Type: "string",
										},
										"field2": {
											Type: "integer",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func getCRDVersion(crd *apiextensionsv1.CustomResourceDefinition, version string) *apiextensionsv1.CustomResourceDefinitionVersion {
	for _, v := range crd.Spec.Versions {
		if v.Name == version {
			return &v
		}
	}
	return nil
}

var _ = Describe("CRD Compatibility Checker", func() {
	var (
		baseCRD *apiextensionsv1.CustomResourceDefinition
	)

	BeforeEach(func() {
		baseCRD = getValidBaseCRD()
	})

	runTest := func(mutator crdMutator) ([]string, []string) {
		errors, warnings, err := CheckCRDCompatibility(baseCRD, mutator(baseCRD.DeepCopy()))
		Expect(err).To(BeNil(), "CheckCRDCompatibility should not return an error")
		return errors, warnings
	}

	// Example test cases - these are just examples and should be replaced with actual tests
	Context("when CRD is compatible", func() {
		It("should pass with no modifications", func() {
			errors, warnings := runTest(
				func(target *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition {
					return target
				},
			)

			Expect(errors).To(BeEmpty(), "should have no errors")
			Expect(warnings).To(BeEmpty(), "should have no warnings")
		})

		It("should fail when a field is removed", func() {
			errors, _ := runTest(
				func(target *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition {
					version := getCRDVersion(target, "v2")
					delete(version.Schema.OpenAPIV3Schema.Properties["spec"].Properties, "field1")
					return target
				},
			)

			Expect(errors).NotTo(BeEmpty(), "should contain an error")
		})

		It("should permit an optional field to be added", func() {
			errors, warnings := runTest(
				func(crd *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition {
					version := getCRDVersion(crd, "v2")
					version.Schema.OpenAPIV3Schema.Properties["spec"].Properties["field3"] = apiextensionsv1.JSONSchemaProps{
						Type: "string",
					}
					return crd
				},
			)

			Expect(errors).To(BeEmpty(), "should have no errors")
			Expect(warnings).To(BeEmpty(), "should have no warnings")
		})

		It("should not permit a required field to be added", func() {
			Skip("This behavior is not yet implemented") // TODO: Implement this

			errors, _ := runTest(
				func(target *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition {
					crdSchema := target.Spec.Versions[0].Schema.OpenAPIV3Schema

					spec := crdSchema.Properties["spec"]
					spec.Properties["field4"] = apiextensionsv1.JSONSchemaProps{
						Type: "string",
					}
					spec.Required = append(spec.Required, "field4")
					crdSchema.Properties["spec"] = spec

					return target
				},
			)

			Expect(errors).NotTo(BeEmpty(), "should contain an error")
		})

		It("should not permit a served version to be removed", func() {
			Skip("This behavior is not yet implemented") // TODO: Implement this

			errors, _ := runTest(
				func(target *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition {
					target.Spec.Versions = target.Spec.Versions[1:]
					return target
				},
			)

			Expect(errors).NotTo(BeEmpty(), "should contain an error")
		})
	})
})

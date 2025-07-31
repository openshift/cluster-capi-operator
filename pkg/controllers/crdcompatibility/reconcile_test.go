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

package crdcompatibility

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
)

var _ = Describe("CRDCompatibilityRequirement", func() {
	const (
		testCRDName = "crdcompatibilityrequirements.operator.openshift.io"
	)

	validCRD := func(ctx context.Context) *apiextensionsv1.CustomResourceDefinition {
		// Fetch the CRDCompatibilityRequirement CRD itself, because we know it's definitely loaded
		crd := &apiextensionsv1.CustomResourceDefinition{}
		Expect(cl.Get(ctx, types.NamespacedName{Name: testCRDName}, crd)).To(Succeed(), "CRDCompatibilityRequirement CRD should be loaded")
		return crd
	}

	toYAML := func(crd *apiextensionsv1.CustomResourceDefinition) string {
		yaml, err := yaml.Marshal(crd)
		Expect(err).To(Succeed())
		return string(yaml)
	}

	Context("When creating a CRDCompatibilityRequirement", func() {
		It("Should admit the simplest possible CRDCompatibilityRequirement object", func(ctx context.Context) {
			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-requirement",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRDName,
					CompatibilityCRD:   toYAML(validCRD(ctx)),
					CRDAdmitAction:     "Warn",
					CreatorDescription: "Test Creator",
				},
			}

			// Create the CRDCompatibilityRequirement
			Expect(cl.Create(ctx, requirement)).To(Succeed())
			DeferCleanup(func() {
				Expect(cl.Delete(ctx, requirement)).To(Succeed())
			})

			Eventually(func() (*operatorv1alpha1.CRDCompatibilityRequirement, error) {
				var fetched operatorv1alpha1.CRDCompatibilityRequirement
				if err := cl.Get(ctx, types.NamespacedName{Name: requirement.Name}, &fetched); err != nil {
					return nil, err
				}
				return &fetched, nil
			}).Should(SatisfyAll(
				HaveField("Name", Equal("test-requirement")),
			))

			// Verify the object was created successfully
			var fetched operatorv1alpha1.CRDCompatibilityRequirement
			Expect(cl.Get(ctx, types.NamespacedName{Name: requirement.Name}, &fetched)).To(Succeed())
			Expect(fetched.Name).To(Equal("test-requirement"))
			Expect(fetched.Spec.CRDRef).To(Equal("test.example.com"))
			Expect(fetched.Spec.CRDAdmitAction).To(Equal("Warn"))
			Expect(fetched.Spec.CreatorDescription).To(Equal("Test Creator"))
			Expect(fetched.Spec.CompatibilityCRD).To(ContainSubstring("apiVersion: apiextensions.k8s.io/v1"))
		})
	})
})

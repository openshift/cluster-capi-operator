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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
)

var _ = Describe("CRDCompatibilityRequirement", func() {
	Context("When creating a CRDCompatibilityRequirement", func() {
		It("Should admit the simplest possible CRDCompatibilityRequirement object", func() {
			ctx := context.Background()

			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-requirement",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef: "test.example.com",
					CompatibilityCRD: `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: test.example.com
spec:
  group: test.example.com
  names:
    kind: Test
    listKind: TestList
    plural: tests
    singular: test
  scope: Namespaced
  versions:
  - name: v1
    schema:
      openAPIV3Schema:
        type: object
    served: true
    storage: true`,
					CRDAdmitAction:     "Warn",
					CreatorDescription: "Test Creator",
				},
			}

			// Create the CRDCompatibilityRequirement
			Expect(cl.Create(ctx, requirement)).To(Succeed())
			DeferCleanup(func() {
				Expect(cl.Delete(ctx, requirement)).To(Succeed())
			})

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

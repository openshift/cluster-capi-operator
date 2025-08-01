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

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
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

	createRequirement := func(ctx context.Context, requirement *operatorv1alpha1.CRDCompatibilityRequirement) {
		By("Creating the CRDCompatibilityRequirement")
		Expect(cl.Create(ctx, requirement)).To(Succeed())

		DeferCleanup(func(ctx context.Context) {
			By("Deleting the CRDCompatibilityRequirement")
			Expect(cl.Delete(ctx, requirement)).To(Succeed())
		})
	}

	Context("When creating a CRDCompatibilityRequirement", func() {
		It("Should set Progressing condition and observed CRD", func(ctx context.Context) {
			testCRD := validCRD(ctx)

			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-requirement-",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRDName,
					CompatibilityCRD:   toYAML(testCRD),
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}

			createRequirement(ctx, requirement)

			By("Waiting for the CRDCompatibilityRequirement to have the expected status")
			Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
				test.HaveCondition("Progressing", metav1.ConditionFalse, progressingReasonUpToDate, "The CRDCompatibilityRequirement is up to date"),
				HaveField("Status.ObservedCRD.UID", BeEquivalentTo(testCRD.UID)),
				HaveField("Status.ObservedCRD.Generation", BeEquivalentTo(testCRD.Generation)),
			))
		})

		It("Should set a terminal failure when compatibility CRD does not parse", func(ctx context.Context) {
			testCRD := validCRD(ctx)

			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-requirement-",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRDName,
					CompatibilityCRD:   "invalid",
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}

			createRequirement(ctx, requirement)

			By("Waiting for the CRDCompatibilityRequirement to have the expected status")
			Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
				test.HaveCondition("Progressing", metav1.ConditionFalse, progressingReasonConfigurationError, "failed to parse compatibilityCRD for crdcompatibilityrequirements.operator.openshift.io: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal string into Go value of type v1.CustomResourceDefinition"),

				// It should still set the observed CRD
				HaveField("Status.ObservedCRD.UID", BeEquivalentTo(testCRD.UID)),
				HaveField("Status.ObservedCRD.Generation", BeEquivalentTo(testCRD.Generation)),
			))
		})

		It("Should not set an error when the CRD is not found", func(ctx context.Context) {
			testCRD := validCRD(ctx)

			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-requirement-",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRDName + "-foo",
					CompatibilityCRD:   toYAML(testCRD),
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}

			createRequirement(ctx, requirement)

			By("Waiting for the CRDCompatibilityRequirement to have the expected status")
			Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
				test.HaveCondition("Progressing", metav1.ConditionFalse, progressingReasonUpToDate, "The CRDCompatibilityRequirement is up to date"),

				// observed CRD should be empty
				HaveField("Status.ObservedCRD", BeNil()),
			))
		})
	})
})

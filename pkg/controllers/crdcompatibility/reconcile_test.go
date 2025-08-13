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
	"crypto/rand"
	"fmt"
	"math/big"
	"unicode"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("CRDCompatibilityRequirement", func() {
	Context("When creating a CRDCompatibilityRequirement", func() {
		var testCRD *apiextensionsv1.CustomResourceDefinition

		BeforeEach(func(ctx context.Context) {
			testCRD = generateTestCRD(ctx)
			Expect(cl.Create(ctx, testCRD)).To(Succeed())
			DeferCleanup(func(ctx context.Context) {
				By("Deleting test CRD " + testCRD.Name)
				Expect(cl.Delete(ctx, testCRD)).To(Succeed())
			})
		})

		It("Should set all conditions and observed CRD", func(ctx context.Context) {
			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-requirement-",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRD.Name,
					CompatibilityCRD:   toYAML(testCRD),
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}

			createRequirement(ctx, requirement)

			By("Waiting for the CRDCompatibilityRequirement to have the expected status")
			Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
				test.HaveCondition("Progressing", metav1.ConditionFalse,
					test.WithConditionReason(progressingReasonUpToDate),
					test.WithConditionMessage("The CRDCompatibilityRequirement is up to date")),
				test.HaveCondition("Admitted", metav1.ConditionTrue,
					test.WithConditionReason(admittedReasonAdmitted),
					test.WithConditionMessage("The CRDCompatibilityRequirement has been admitted")),
				test.HaveCondition("Compatible", metav1.ConditionTrue,
					test.WithConditionReason(compatibleReasonCompatible),
					test.WithConditionMessage("The CRD is compatible with this requirement")),
				HaveField("Status.ObservedCRD.UID", BeEquivalentTo(testCRD.UID)),
				HaveField("Status.ObservedCRD.Generation", BeEquivalentTo(testCRD.Generation)),
			))
		})

		It("Should set a terminal failure when compatibility CRD does not parse", func(ctx context.Context) {
			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-requirement-",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRD.Name,
					CompatibilityCRD:   "invalid",
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}

			createRequirement(ctx, requirement)

			By("Waiting for the CRDCompatibilityRequirement to have the expected status")
			Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
				test.HaveCondition("Progressing", metav1.ConditionFalse,
					test.WithConditionReason(progressingReasonConfigurationError),
					test.WithConditionMessage("failed to parse compatibilityCRD: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal string into Go value of type v1.CustomResourceDefinition")),

				// It should still set the observed CRD
				HaveField("Status.ObservedCRD.UID", BeEquivalentTo(testCRD.UID)),
				HaveField("Status.ObservedCRD.Generation", BeEquivalentTo(testCRD.Generation)),
			))
		})

		It("Should not set an error when the CRD is not found", func(ctx context.Context) {
			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-requirement-",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             "tests.example.com",
					CompatibilityCRD:   toYAML(testCRD),
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}

			createRequirement(ctx, requirement)

			By("Waiting for the CRDCompatibilityRequirement to have the expected status")
			Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
				test.HaveCondition("Progressing", metav1.ConditionFalse, test.WithConditionReason(progressingReasonUpToDate), test.WithConditionMessage("The CRDCompatibilityRequirement is up to date")),

				// observed CRD should be empty
				HaveField("Status.ObservedCRD", BeNil()),
			))
		})
	})

	Context("When modifying a CRD", func() {
		var testCRD *apiextensionsv1.CustomResourceDefinition

		BeforeEach(func(ctx context.Context) {
			testCRD = generateTestCRD(ctx)

			By("Creating test CRD " + testCRD.Name)
			Expect(cl.Create(ctx, testCRD)).To(Succeed())

			DeferCleanup(func(ctx context.Context) {
				By("Deleting test CRD " + testCRD.Name)
				Expect(cl.Delete(ctx, testCRD)).To(Succeed())
			})
		})

		It("Should not permit a CRD with requirements to be deleted", func(ctx context.Context) {
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					Name: testCRD.Name,
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRD.Name,
					CompatibilityCRD:   toYAML(testCRD),
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}
			createRequirement(ctx, requirement)

			By("Waiting for the CRDCompatibilityRequirement to be admitted")
			Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
				test.HaveCondition("Admitted", metav1.ConditionTrue),
			))

			By("Attempting to delete the test CRD")
			Expect(cl.Delete(ctx, testCRD)).NotTo(Succeed(), "The test CRD should not be deleted")
		})

		It("Should not permit an invalid CRD modification", func(ctx context.Context) {
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					Name: testCRD.Name,
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRD.Name,
					CompatibilityCRD:   toYAML(testCRD),
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}
			createRequirement(ctx, requirement)

			By("Waiting for the CRDCompatibilityRequirement to be admitted")
			Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
				test.HaveCondition("Admitted", metav1.ConditionTrue),
			))

			By("Attempting to make an invalid modification by removing a field")
			updateCRD := kWithCtx(ctx).Update(testCRD, func() {
				delete(testCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties, "status")
			})
			Eventually(updateCRD).Should(WithTransform(func(err error) string {
				return err.Error()
			}, Equal(fmt.Sprintf("admission webhook \"crdcompatibility.operator.openshift.io\" denied the request: new CRD is not compatible with the following: requirement %s: removed field : v1beta1.^.status", requirement.Name))))
		})

		It("Should permit a valid CRD modification", func(ctx context.Context) {
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					Name: testCRD.Name,
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRD.Name,
					CompatibilityCRD:   toYAML(testCRD),
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}
			createRequirement(ctx, requirement)

			By("Waiting for the CRDCompatibilityRequirement to be admitted")
			Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
				test.HaveCondition("Admitted", metav1.ConditionTrue),
			))

			By("Attempting to make a valid modification by adding a field")
			updateCRD := kWithCtx(ctx).Update(testCRD, func() {
				testCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["foo"] = apiextensionsv1.JSONSchemaProps{
					Type: "string",
				}
			})
			Eventually(updateCRD).Should(Succeed(), "The test CRD should be modified")
		})
	})
})

func generateTestCRD(ctx context.Context) *apiextensionsv1.CustomResourceDefinition {
	const validChars = "abcdefghijklmnopqrstuvwxyz"
	randBytes := make([]byte, 10)
	for i := range randBytes {
		randInt, err := rand.Int(rand.Reader, big.NewInt(int64(len(validChars))))
		Expect(err).To(Succeed())
		randBytes[i] = validChars[randInt.Int64()]
	}
	gvk := schema.GroupVersionKind{
		Group:   "example.com",
		Version: "v1",
		Kind:    string(unicode.ToUpper(rune(randBytes[0]))) + string(randBytes[1:]),
	}
	return test.GenerateCRD(gvk, "v1beta1")
}

func invalidCRDModification(testCRD *apiextensionsv1.CustomResourceDefinition, createOrUpdate func(context.Context, client.Object) error) func(context.Context) {
	return func(ctx context.Context) {
		requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
			ObjectMeta: metav1.ObjectMeta{
				Name: testCRD.Name,
			},
			Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
				CRDRef:             testCRD.Name,
				CompatibilityCRD:   toYAML(testCRD),
				CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
				CreatorDescription: "Test Creator",
			},
		}
		createRequirement(ctx, requirement)

		By("Waiting for the CRDCompatibilityRequirement to be admitted")
		Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
			test.HaveCondition("Admitted", metav1.ConditionTrue),
		))

		By("Attempting to make a valid modification by adding a field")
		updateCRD := kWithCtx(ctx).Update(testCRD, func() {
			testCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["foo"] = apiextensionsv1.JSONSchemaProps{
				Type: "string",
			}
		})
		Eventually(updateCRD).Should(Succeed(), "The test CRD should be modified")
	}
}
func createRequirement(ctx context.Context, requirement *operatorv1alpha1.CRDCompatibilityRequirement) {
	By("Creating CRDCompatibilityRequirement " + requirement.Name)
	Expect(cl.Create(ctx, requirement)).To(Succeed())

	DeferCleanup(func(ctx context.Context) {
		By("Deleting CRDCompatibilityRequirement " + requirement.Name)
		Expect(cl.Delete(ctx, requirement)).To(Succeed())
		Eventually(kWithCtx(ctx).Get(requirement)).Should(test.BeK8SNotFound())
	})
}

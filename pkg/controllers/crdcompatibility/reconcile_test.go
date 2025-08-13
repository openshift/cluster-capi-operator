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
	"math/big"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

// getValidBaseCRD returns a valid CRD that can be used as the base for all tests
func getValidBaseCRD(name string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apiextensions.k8s.io/v1",
			Kind:       "CustomResourceDefinition",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name + "s.example.com",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.com",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "Test",
				ListKind: "TestList",
				Plural:   name + "s",
				Singular: name,
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

var _ = Describe("CRDCompatibilityRequirement", func() {
	const (
		testCRDName = "crdcompatibilityrequirements.operator.openshift.io"
	)

	compatibilityRequirementCRD := func(ctx context.Context) *apiextensionsv1.CustomResourceDefinition {
		// Fetch the CRDCompatibilityRequirement CRD itself, because we know it's definitely loaded
		crd := &apiextensionsv1.CustomResourceDefinition{}
		Expect(cl.Get(ctx, types.NamespacedName{Name: testCRDName}, crd)).To(Succeed(), "CRDCompatibilityRequirement CRD should be loaded")
		return crd
	}

	createRequirement := func(ctx context.Context, requirement *operatorv1alpha1.CRDCompatibilityRequirement) {
		By("Creating CRDCompatibilityRequirement " + requirement.Name)
		Expect(cl.Create(ctx, requirement)).To(Succeed())

		DeferCleanup(func(ctx context.Context) {
			By("Deleting CRDCompatibilityRequirement " + requirement.Name)
			Expect(cl.Delete(ctx, requirement)).To(Succeed())
			Eventually(kWithCtx(ctx).Get(requirement)).Should(test.BeK8SNotFound())
		})
	}

	Context("When creating a CRDCompatibilityRequirement", func() {
		It("Should set all conditions and observed CRD", func(ctx context.Context) {
			testCRD := compatibilityRequirementCRD(ctx)

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
			testCRD := compatibilityRequirementCRD(ctx)

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
				test.HaveCondition("Progressing", metav1.ConditionFalse, test.WithConditionReason(progressingReasonConfigurationError), test.WithConditionMessage("failed to parse compatibilityCRD for crdcompatibilityrequirements.operator.openshift.io: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal string into Go value of type v1.CustomResourceDefinition")),

				// It should still set the observed CRD
				HaveField("Status.ObservedCRD.UID", BeEquivalentTo(testCRD.UID)),
				HaveField("Status.ObservedCRD.Generation", BeEquivalentTo(testCRD.Generation)),
			))
		})

		It("Should not set an error when the CRD is not found", func(ctx context.Context) {
			testCRD := compatibilityRequirementCRD(ctx)

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
				test.HaveCondition("Progressing", metav1.ConditionFalse, test.WithConditionReason(progressingReasonUpToDate), test.WithConditionMessage("The CRDCompatibilityRequirement is up to date")),

				// observed CRD should be empty
				HaveField("Status.ObservedCRD", BeNil()),
			))
		})

		Context("When modifying a CRD", func() {
			var testCRD *apiextensionsv1.CustomResourceDefinition

			BeforeEach(func(ctx context.Context) {
				const validChars = "abcdefghijklmnopqrstuvwxyz"
				randBytes := make([]byte, 10)
				for i := range randBytes {
					randInt, err := rand.Int(rand.Reader, big.NewInt(int64(len(validChars))))
					Expect(err).To(Succeed())
					randBytes[i] = validChars[randInt.Int64()]
				}
				testCRD = getValidBaseCRD(string(randBytes))

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

				By("Attempting to modify the test CRD by deleting a version")
				updateCRD := kWithCtx(ctx).Update(testCRD, func() {
					testCRD.Spec.Versions = testCRD.Spec.Versions[1:]
				})
				Eventually(updateCRD).Should(Succeed(), "The test CRD should be modified")
			})
		})
	})
})

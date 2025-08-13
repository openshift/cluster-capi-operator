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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
			testCRD = generateTestCRD()
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

		It("Should correctly set observed generation on conditions", func(ctx context.Context) {
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

			// Update the requirement to bump the generation
			Eventually(kWithCtx(ctx).Update(requirement, func() {
				requirement.Spec.CRDAdmitAction = operatorv1alpha1.CRDAdmitActionWarn
			})).Should(Succeed())

			Expect(requirement).To(HaveField("ObjectMeta.Generation", Equal(int64(2))))

			By("Waiting for the CRDCompatibilityRequirement to have the expected status")
			Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
				test.HaveCondition("Progressing", metav1.ConditionFalse,
					test.WithConditionReason(progressingReasonUpToDate),
					test.WithConditionMessage("The CRDCompatibilityRequirement is up to date"),
					test.WithConditionObservedGeneration(requirement.GetGeneration()),
				),
				test.HaveCondition("Admitted", metav1.ConditionTrue,
					test.WithConditionReason(admittedReasonAdmitted),
					test.WithConditionMessage("The CRDCompatibilityRequirement has been admitted"),
					test.WithConditionObservedGeneration(requirement.GetGeneration()),
				),
				test.HaveCondition("Compatible", metav1.ConditionTrue,
					test.WithConditionReason(compatibleReasonCompatible),
					test.WithConditionMessage("The CRD is compatible with this requirement"),
					test.WithConditionObservedGeneration(requirement.GetGeneration()),
				),
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

	Context("When creating or modifying a CRD", func() {
		testInvalidCRD := func(ctx context.Context, testCRD *apiextensionsv1.CustomResourceDefinition, requirement *operatorv1alpha1.CRDCompatibilityRequirement, createOrUpdateCRD func(context.Context, client.Object, func()) func() error) {
			By("Attempting to make an invalid modification by removing a field")
			updateCRD := createOrUpdateCRD(ctx, testCRD, func() {
				delete(testCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties, "status")
			})
			Eventually(updateCRD).Should(WithTransform(func(err error) string { return err.Error() },
				Equal(fmt.Sprintf("admission webhook \"crdcompatibility.operator.openshift.io\" denied the request: new CRD is not compatible with the following: requirement %s: removed field : v1beta1.^.status", requirement.Name))))
		}

		testValidCRD := func(ctx context.Context, testCRD *apiextensionsv1.CustomResourceDefinition, createOrUpdateCRD func(context.Context, client.Object, func()) func() error) {
			By("Attempting to make a valid modification by adding a field")
			updateCRD := createOrUpdateCRD(ctx, testCRD, func() {
				testCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["foo"] = apiextensionsv1.JSONSchemaProps{
					Type: "string",
				}
			})
			Eventually(updateCRD).Should(Succeed(), "The test CRD should be modified")
		}

		Context("When modifying a CRD with a requirement", func() {
			var (
				testCRD     *apiextensionsv1.CustomResourceDefinition
				requirement *operatorv1alpha1.CRDCompatibilityRequirement
			)

			BeforeEach(func(ctx context.Context) {
				testCRD = generateTestCRD()

				By("Creating test CRD " + testCRD.Name)
				Expect(cl.Create(ctx, testCRD)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					By("Deleting test CRD " + testCRD.Name)
					Expect(cl.Delete(ctx, testCRD)).To(Succeed())
				})

				requirement = createTestRequirement(ctx, testCRD)
			})

			It("Should not permit a CRD with requirements to be deleted", func(ctx context.Context) {
				By("Attempting to delete CRD " + testCRD.Name)
				Expect(cl.Delete(ctx, testCRD)).NotTo(Succeed(), "The test CRD should not be deleted")
			})

			updateCRD := func(ctx context.Context, obj client.Object, updateFn func()) func() error {
				return kWithCtx(ctx).Update(obj, updateFn)
			}

			It("Should not permit an invalid CRD modification", func(ctx context.Context) {
				testInvalidCRD(ctx, testCRD, requirement, updateCRD)
			})

			It("Should permit a valid CRD modification", func(ctx context.Context) {
				testValidCRD(ctx, testCRD, updateCRD)
			})
		})

		Context("When creating a CRD with a requirement", func() {
			var (
				testCRD     *apiextensionsv1.CustomResourceDefinition
				requirement *operatorv1alpha1.CRDCompatibilityRequirement
			)

			BeforeEach(func(ctx context.Context) {
				testCRD = generateTestCRD()

				DeferCleanup(func(ctx context.Context) {
					// We need to register this before requirement is created so it will be deleted after
					By("Deleting test CRD " + testCRD.Name)
					Eventually(cl.Delete(ctx, testCRD)).Should(WithTransform(func(err error) error {
						if apierrors.IsNotFound(err) {
							return nil
						}
						return err
					}, Succeed()))
					Eventually(kWithCtx(ctx).Get(testCRD)).Should(test.BeK8SNotFound())
				})

				requirement = createTestRequirement(ctx, testCRD)
			})

			createCRD := func(ctx context.Context, obj client.Object, updateFn func()) func() error {
				return func() error {
					updateFn()

					By("Creating test CRD " + testCRD.Name)
					if err := cl.Create(ctx, testCRD); err != nil {
						return err
					}

					return nil
				}
			}

			It("Should not permit an invalid CRD to be created", func(ctx context.Context) {
				testInvalidCRD(ctx, testCRD, requirement, createCRD)
			})

			It("Should permit a valid CRD to be created", func(ctx context.Context) {
				testValidCRD(ctx, testCRD, createCRD)
			})
		})
	})
})

func createTestRequirement(ctx context.Context, testCRD *apiextensionsv1.CustomResourceDefinition) *operatorv1alpha1.CRDCompatibilityRequirement {
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

	return requirement
}

func generateTestCRD() *apiextensionsv1.CustomResourceDefinition {
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

func createRequirement(ctx context.Context, requirement *operatorv1alpha1.CRDCompatibilityRequirement) {
	By("Creating CRDCompatibilityRequirement " + requirement.Name)
	Expect(cl.Create(ctx, requirement)).To(Succeed())

	DeferCleanup(func(ctx context.Context) {
		By("Deleting CRDCompatibilityRequirement " + requirement.Name)
		Expect(cl.Delete(ctx, requirement)).To(Succeed())

		// Need to wait for it to be gone, or CRD deletion will fail
		Eventually(kWithCtx(ctx).Get(requirement)).Should(test.BeK8SNotFound())
	})
}

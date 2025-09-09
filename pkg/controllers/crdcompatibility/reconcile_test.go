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
	"sigs.k8s.io/controller-runtime/pkg/client"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("CRDCompatibilityRequirement", Ordered, ContinueOnFailure, func() {
	// Starting and stopping the manager is quite expensive, so we share one amongst all the tests.
	// Unfortunately ginkgo forces us to use Ordered when doing this.
	BeforeAll(func(ctx context.Context) {
		reconciler, startManager := InitManager(ctx)

		// Mark the reconciler synced to avoid the complexity of running the
		// wait for synced loop
		reconciler.synced = true

		startManager()
	})

	Context("When creating a CRDCompatibilityRequirement", func() {
		var (
			testCRDClean, testCRDWorking *apiextensionsv1.CustomResourceDefinition
		)

		BeforeEach(func(ctx context.Context) {
			testCRDClean = generateTestCRD()

			// Create a working copy of the CRD so we maintain a clean version
			// with no runtime metadata
			testCRDWorking = testCRDClean.DeepCopy()
			createTestObject(ctx, testCRDWorking, "test CRD")
		})

		It("Should set all conditions and observed CRD", func(ctx context.Context) {
			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-requirement-",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRDClean.Name,
					CompatibilityCRD:   toYAML(testCRDClean),
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}

			createTestObject(ctx, requirement, "test CRDCompatibilityRequirement")

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
				HaveField("Status.ObservedCRD.UID", BeEquivalentTo(testCRDWorking.UID)),
				HaveField("Status.ObservedCRD.Generation", BeEquivalentTo(testCRDWorking.Generation)),
			))
		})

		It("Should correctly set observed generation on conditions", func(ctx context.Context) {
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-requirement-",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRDClean.Name,
					CompatibilityCRD:   toYAML(testCRDClean),
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}

			createTestObject(ctx, requirement, "test CRDCompatibilityRequirement")

			generation := requirement.GetGeneration()
			checkForStatusWithGeneration := func(generation int64) {
				Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
					test.HaveCondition("Progressing", metav1.ConditionFalse,
						test.WithConditionReason(progressingReasonUpToDate),
						test.WithConditionMessage("The CRDCompatibilityRequirement is up to date"),
						test.WithConditionObservedGeneration(generation),
					),
					test.HaveCondition("Admitted", metav1.ConditionTrue,
						test.WithConditionReason(admittedReasonAdmitted),
						test.WithConditionMessage("The CRDCompatibilityRequirement has been admitted"),
						test.WithConditionObservedGeneration(generation),
					),
					test.HaveCondition("Compatible", metav1.ConditionTrue,
						test.WithConditionReason(compatibleReasonCompatible),
						test.WithConditionMessage("The CRD is compatible with this requirement"),
						test.WithConditionObservedGeneration(generation),
					),
				))
			}

			By("Waiting for the CRDCompatibilityRequirement to have the expected status with observed generation 1")
			checkForStatusWithGeneration(generation)

			// Update the requirement to bump the generation
			Eventually(kWithCtx(ctx).Update(requirement, func() {
				requirement.Spec.CRDAdmitAction = operatorv1alpha1.CRDAdmitActionWarn
			})).Should(Succeed())

			// Sanity check that the generation has been bumped
			Expect(requirement).To(HaveField("ObjectMeta.Generation", BeNumerically(">", generation)))
			generation = requirement.GetGeneration()

			By("Waiting for the CRDCompatibilityRequirement to have the expected status with observed generation 2")
			checkForStatusWithGeneration(generation)
		})

		It("Should not admit a CRDCompatibilityRequirement if the CompatibilityCRD which does not parse", func(ctx context.Context) {
			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-requirement-",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRDClean.Name,
					CompatibilityCRD:   "not YAML",
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}

			By("Attempting to create invalid CRDCompatibilityRequirement " + requirement.Name)
			expectedError := "admission webhook \"crdcompatibility.operator.openshift.io\" denied the request: expected a valid CustomResourceDefinition in YAML format: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal string into Go value of type v1.CustomResourceDefinition"
			Eventually(tryCreate(ctx, requirement)).Should(MatchError(expectedError))
		})

		It("Should not admit a CRDCompatibilityRequirement if the CompatibilityCRD parses but is not a CRD", func(ctx context.Context) {
			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-requirement-",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             testCRDClean.Name,
					CompatibilityCRD:   "{}",
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}

			By("Attempting to create invalid CRDCompatibilityRequirement " + requirement.Name)
			expectedError := "admission webhook \"crdcompatibility.operator.openshift.io\" denied the request: expected a valid CustomResourceDefinition in YAML format: expected APIVersion to be apiextensions.k8s.io/v1 and Kind to be CustomResourceDefinition, got /"
			Eventually(tryCreate(ctx, requirement)).Should(MatchError(expectedError))
		})

		It("Should not set an error when the CRD is not found", func(ctx context.Context) {
			// Create the simplest possible CRDCompatibilityRequirement
			requirement := &operatorv1alpha1.CRDCompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-requirement-",
				},
				Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
					CRDRef:             "tests.example.com",
					CompatibilityCRD:   toYAML(testCRDClean),
					CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
					CreatorDescription: "Test Creator",
				},
			}

			createTestObject(ctx, requirement, "test CRDCompatibilityRequirement")

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
			// Create a working copy of the CRD so we maintain a clean version
			// without runtime metadata
			testCRDWorking := testCRD.DeepCopy()

			By("Attempting to make an invalid modification by removing a field")
			expectedError := "admission webhook \"crdcompatibility.operator.openshift.io\" denied the request: CRD is not compatible with CRDCompatibilityRequirements: requirement " + requirement.Name + ": removed field : v1beta1.^.status"
			updateCRD := createOrUpdateCRD(ctx, testCRDWorking, func() {
				delete(testCRDWorking.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties, "status")
			})
			Eventually(updateCRD).Should(MatchError(expectedError))
		}

		testValidCRD := func(ctx context.Context, testCRD *apiextensionsv1.CustomResourceDefinition, createOrUpdateCRD func(context.Context, client.Object, func()) func() error) {
			// Create a working copy of the CRD so we maintain a clean version
			// without runtime metadata
			testCRDWorking := testCRD.DeepCopy()

			By("Attempting to make a valid modification by adding a field")
			updateCRD := createOrUpdateCRD(ctx, testCRDWorking, func() {
				testCRDWorking.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["foo"] = apiextensionsv1.JSONSchemaProps{
					Type: "string",
				}
			})
			Eventually(updateCRD).Should(Succeed(), "The test CRD should be modified")
		}

		Context("When modifying a CRD with a requirement", func() {
			var (
				testCRDClean *apiextensionsv1.CustomResourceDefinition
				requirement  *operatorv1alpha1.CRDCompatibilityRequirement
			)

			BeforeEach(func(ctx context.Context) {
				testCRDClean = generateTestCRD()

				// Create a copy of the CRD so we maintain a clean version
				// without runtime metadata
				testCRDWorking := testCRDClean.DeepCopy()

				createTestObject(ctx, testCRDWorking, "test CRD")

				requirement = baseRequirement(testCRDClean)
				createTestObject(ctx, requirement, "test CRDCompatibilityRequirement")
				waitForAdmitted(ctx, requirement)
			})

			It("Should not permit a CRD with requirements to be deleted", func(ctx context.Context) {
				By("Attempting to delete CRD " + testCRDClean.Name)
				Eventually(tryDelete(ctx, testCRDClean)).Should(MatchError(ContainSubstring(errCRDHasRequirements.Error())), "The test CRD should not be deleted")
			})

			updateCRD := func(ctx context.Context, obj client.Object, updateFn func()) func() error {
				return kWithCtx(ctx).Update(obj, updateFn)
			}

			It("Should not permit an invalid CRD modification", func(ctx context.Context) {
				testInvalidCRD(ctx, testCRDClean, requirement, updateCRD)
			})

			It("Should permit a valid CRD modification", func(ctx context.Context) {
				testValidCRD(ctx, testCRDClean, updateCRD)
			})
		})

		Context("When creating a CRD with a requirement", func() {
			var (
				testCRDWorking *apiextensionsv1.CustomResourceDefinition
				requirement    *operatorv1alpha1.CRDCompatibilityRequirement
			)

			BeforeEach(func(ctx context.Context) {
				testCRDWorking = generateTestCRD()

				// We need to register this before the requirement is created so
				// it will be deleted after the requirement is deleted
				deferCleanupTestObject(testCRDWorking, "test CRD")

				requirement = baseRequirement(testCRDWorking)
				createTestObject(ctx, requirement, "test CRDCompatibilityRequirement")
				waitForAdmitted(ctx, requirement)
			})

			createCRD := func(ctx context.Context, obj client.Object, updateFn func()) func() error {
				return func() error {
					By("Creating test CRD " + obj.GetName())

					updateFn()

					return cl.Create(ctx, obj)
				}
			}

			It("Should not permit an invalid CRD to be created", func(ctx context.Context) {
				testInvalidCRD(ctx, testCRDWorking, requirement, createCRD)
			})

			It("Should permit a valid CRD to be created", func(ctx context.Context) {
				testValidCRD(ctx, testCRDWorking, createCRD)
			})
		})
	})
})

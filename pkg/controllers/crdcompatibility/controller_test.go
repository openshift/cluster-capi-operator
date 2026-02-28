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
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

func createRequirementWithCleanup(ctx context.Context, requirement *apiextensionsv1alpha1.CompatibilityRequirement) {
	createTestObject(ctx, requirement, "CompatibilityRequirement")

	// The reconciler added a finalizer which we need to remove manually because
	// we're not running the controller
	DeferCleanup(func(ctx context.Context) {
		By("Removing finalizer from " + requirement.Name)
		Eventually(kWithCtx(ctx).Update(requirement, func() {
			requirement.SetFinalizers(nil)
		})).Should(SatisfyAny(Succeed(), test.BeK8SNotFound())) // If a test case deletes the object, this will return a not found error.
	})
}

var _ = Describe("CRDCompatibilityReconciler Controller Setup", func() {
	var (
		reconciler *CompatibilityRequirementReconciler
		testCRD    *apiextensionsv1.CustomResourceDefinition
	)

	BeforeEach(func(ctx context.Context) {
		reconciler, _ = InitManager(ctx)

		// We aren't going to start the manager, so the cached client won't work
		reconciler.client = cl

		testCRD = test.GenerateTestCRD()

		// Create a working copy of the CRD so we maintain a clean version
		// with no runtime metadata
		testCRDWorking := testCRD.DeepCopy()
		createTestObject(ctx, testCRDWorking, "CRD")
	})

	Context("When starting the controller with existing state", func() {
		var (
			admittedRequirements    []*apiextensionsv1alpha1.CompatibilityRequirement
			nonAdmittedRequirements []*apiextensionsv1alpha1.CompatibilityRequirement
		)

		BeforeEach(func(ctx context.Context) {
			By("Creating 2 admitted and 2 non-admitted requirements")
			admittedRequirements = []*apiextensionsv1alpha1.CompatibilityRequirement{
				test.GenerateTestCompatibilityRequirement(testCRD),
				test.GenerateTestCompatibilityRequirement(testCRD),
			}
			nonAdmittedRequirements = []*apiextensionsv1alpha1.CompatibilityRequirement{
				test.GenerateTestCompatibilityRequirement(testCRD),
				test.GenerateTestCompatibilityRequirement(testCRD),
			}
			for _, requirement := range nonAdmittedRequirements {
				requirement.Spec.CompatibilitySchema.CustomResourceDefinition.Data = "invalid"
			}

			for _, requirement := range slices.Concat(admittedRequirements, nonAdmittedRequirements) {
				createRequirementWithCleanup(ctx, requirement)
			}

			// Reconcile the admitted requirements to write their status
			By("Reconciling admitted requirements to write their status")

			for _, requirement := range admittedRequirements {
				Eventually(func() (*apiextensionsv1alpha1.CompatibilityRequirement, error) {
					if _, err := reconciler.Reconcile(ctx, ctrl.Request{
						NamespacedName: types.NamespacedName{
							Name: requirement.Name,
						},
					}); err != nil {
						return nil, err
					}

					if err := cl.Get(ctx, types.NamespacedName{Name: requirement.Name}, requirement); err != nil {
						return nil, err
					}

					return requirement, nil
				}).Should(HaveField("Status.Conditions", test.HaveCondition("Admitted").WithStatus(metav1.ConditionTrue)))
			}

			for _, requirement := range nonAdmittedRequirements {
				Eventually(func() (*apiextensionsv1alpha1.CompatibilityRequirement, error) {
					if _, err := reconciler.Reconcile(ctx, ctrl.Request{
						NamespacedName: types.NamespacedName{
							Name: requirement.Name,
						},
					}); err != nil && !util.IsTerminalWithReasonError(err) {
						return nil, err
					}

					if err := cl.Get(ctx, types.NamespacedName{Name: requirement.Name}, requirement); err != nil {
						return nil, err
					}

					return requirement, nil
				}).Should(HaveField("Status.Conditions", test.HaveCondition("Admitted").WithStatus(metav1.ConditionFalse)))
			}
		})

		It("should not adjust the transition timestamp of conditions that have not changed", func(ctx context.Context) {
			for _, requirement := range admittedRequirements {
				var originalTransitionTime metav1.Time
				for _, condition := range requirement.Status.Conditions {
					if condition.Type == apiextensionsv1alpha1.CompatibilityRequirementAdmitted {
						originalTransitionTime = condition.LastTransitionTime
						break
					}
				}
				Expect(originalTransitionTime).NotTo(BeZero())

				Eventually(func() (*apiextensionsv1alpha1.CompatibilityRequirement, error) {
					if _, err := reconciler.Reconcile(ctx, ctrl.Request{
						NamespacedName: types.NamespacedName{
							Name: requirement.Name,
						},
					}); err != nil {
						return nil, err
					}

					if err := cl.Get(ctx, types.NamespacedName{Name: requirement.Name}, requirement); err != nil {
						return nil, err
					}

					return requirement, nil
				}).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
					HaveField("Type", BeEquivalentTo(apiextensionsv1alpha1.CompatibilityRequirementAdmitted)),
					HaveField("LastTransitionTime", BeEquivalentTo(originalTransitionTime)),
				))))
			}
		}, defaultNodeTimeout)

		It("should remove the objects finalizer when the requirement is deleted", func(ctx context.Context) {
			for _, requirement := range admittedRequirements {
				Expect(kWithCtx(ctx).Object(requirement)()).To(HaveField("ObjectMeta.Finalizers", Not(BeEmpty())))

				Expect(cl.Delete(ctx, requirement)).To(Succeed())

				Eventually(func() error {
					if _, err := reconciler.Reconcile(ctx, ctrl.Request{
						NamespacedName: types.NamespacedName{
							Name: requirement.Name,
						},
					}); err != nil {
						return err
					}

					return cl.Get(ctx, types.NamespacedName{Name: requirement.Name}, requirement)
				}).Should(test.BeK8SNotFound())
			}
		}, defaultNodeTimeout)
	})
})

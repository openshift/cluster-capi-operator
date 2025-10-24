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
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

func createRequirementWithCleanup(ctx context.Context, requirement *operatorv1alpha1.CRDCompatibilityRequirement) {
	createTestObject(ctx, requirement, "CRDCompatibilityRequirement")

	// The reconciler added a finalizer which we need to remove manually because
	// we're not running the controller
	DeferCleanup(func(ctx context.Context) {
		By("Removing finalizer from " + requirement.Name)
		Eventually(kWithCtx(ctx).Update(requirement, func() {
			requirement.SetFinalizers(nil)
		})).Should(Succeed())
	})
}

var _ = Describe("CRDCompatibilityReconciler Controller Setup", func() {
	var (
		reconciler *CRDCompatibilityReconciler
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
			admittedRequirements    []*operatorv1alpha1.CRDCompatibilityRequirement
			nonAdmittedRequirements []*operatorv1alpha1.CRDCompatibilityRequirement
		)

		BeforeEach(func(ctx context.Context) {
			By("Creating 2 admitted and 2 non-admitted requirements")
			admittedRequirements = []*operatorv1alpha1.CRDCompatibilityRequirement{
				test.GenerateTestCRDCompatibilityRequirement(testCRD),
				test.GenerateTestCRDCompatibilityRequirement(testCRD),
			}
			nonAdmittedRequirements = []*operatorv1alpha1.CRDCompatibilityRequirement{
				test.GenerateTestCRDCompatibilityRequirement(testCRD),
				test.GenerateTestCRDCompatibilityRequirement(testCRD),
			}

			for _, requirement := range slices.Concat(admittedRequirements, nonAdmittedRequirements) {
				createRequirementWithCleanup(ctx, requirement)
			}

			// Reconcile the admitted requirements to write their status
			By("Reconciling admitted requirements to write their status")

			for _, requirement := range admittedRequirements {
				Eventually(func() (*operatorv1alpha1.CRDCompatibilityRequirement, error) {
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
				}).Should(test.HaveCondition("Admitted", metav1.ConditionTrue))
			}
		})
	})
})

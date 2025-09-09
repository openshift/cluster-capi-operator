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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func createRequirementWithCleanup(ctx context.Context, requirement *operatorv1alpha1.CRDCompatibilityRequirement) {
	createTestObject(ctx, requirement, "test CRDCompatibilityRequirement")

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
		mgrClient  client.Client
		testCRD    *apiextensionsv1.CustomResourceDefinition
	)

	BeforeEach(func(ctx context.Context) {
		reconciler, _ = InitManager(ctx)

		// We aren't going to start the manager, so the cached client won't work
		mgrClient = reconciler.client
		reconciler.client = cl

		testCRD = generateTestCRD()

		// Create a working copy of the CRD so we maintain a clean version
		// with no runtime metadata
		testCRDWorking := testCRD.DeepCopy()
		createTestObject(ctx, testCRDWorking, "test CRD")
	})

	Context("When starting the controller with existing state", func() {
		var (
			admittedRequirements    []*operatorv1alpha1.CRDCompatibilityRequirement
			nonAdmittedRequirements []*operatorv1alpha1.CRDCompatibilityRequirement
		)

		BeforeEach(func(ctx context.Context) {
			By("Creating 2 admitted and 2 non-admitted requirements")
			admittedRequirements = []*operatorv1alpha1.CRDCompatibilityRequirement{
				baseRequirement(testCRD),
				baseRequirement(testCRD),
			}
			nonAdmittedRequirements = []*operatorv1alpha1.CRDCompatibilityRequirement{
				baseRequirement(testCRD),
				baseRequirement(testCRD),
			}

			for _, requirement := range slices.Concat(admittedRequirements, nonAdmittedRequirements) {
				createRequirementWithCleanup(ctx, requirement)
			}

			// Reconcile the admitted requirements to write their status
			By("Reconciling admitted requirements to write their status")

			// We're simulating a run of a previous incarnation of the controller.
			// Set the synced flag so the controller doesn't register the
			// reconciliation of the admitted requirements.
			reconciler.synced = true
			defer func() {
				reconciler.synced = false
			}()

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

		It("should return an error if the context is cancelled while waiting for requirements to be synced", func(ctx context.Context) {
			ctx, cancel := context.WithDeadline(ctx, time.Now().Add(100*time.Millisecond))
			defer cancel()

			// We use the manager client because it will only return errors
			// (because the manager is not running so the caches are not
			// synced). This ensures the sync loop can never complete
			// successfully, which avoids a race in the test.
			reconciler.client = mgrClient
			Expect(reconciler.WaitForSynced(ctx)).To(MatchError(ContainSubstring("context deadline exceeded")))
		}, NodeTimeout(5*time.Second))

		It("should be synced when all admitted requirements have been reconciled", func(ctx context.Context) {
			ctx = ctrl.LoggerInto(ctx, GinkgoLogr)

			errChan := make(chan error)
			go func() {
				errChan <- reconciler.WaitForSynced(ctx)
			}()

			for _, requirement := range admittedRequirements {
				Eventually(func() error {
					_, err := reconciler.Reconcile(ctx, ctrl.Request{
						NamespacedName: types.NamespacedName{
							Name: requirement.Name,
						},
					})
					return err
				}).Should(Succeed())
			}

			Expect(<-errChan).To(Succeed())
		}, NodeTimeout(5*time.Second))

		It("should not be synced when only non-admitted requirements have been reconciled", func(ctx context.Context) {
			ctx = ctrl.LoggerInto(ctx, GinkgoLogr)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			errChan := make(chan error)
			go func() {
				errChan <- reconciler.WaitForSynced(ctx)
			}()

			for _, requirement := range nonAdmittedRequirements {
				Eventually(func() error {
					_, err := reconciler.Reconcile(ctx, ctrl.Request{
						NamespacedName: types.NamespacedName{
							Name: requirement.Name,
						},
					})
					return err
				}).Should(Succeed())
			}

			// Give the sync routine a chance to exit prematurely, which is the
			// purpose of this test. If it exits prematurely we would see a null
			// on the error channel. We expect to see that it exited because we
			// cancelled it after giving it the opportunity to exit normally.
			time.Sleep(100 * time.Millisecond)
			cancel()

			Expect(<-errChan).To(MatchError(ContainSubstring("context canceled")))
			Expect(reconciler.IsSynced()).To(BeFalse())
		}, NodeTimeout(5*time.Second))
	})
})

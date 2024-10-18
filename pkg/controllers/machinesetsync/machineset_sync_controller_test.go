/*
Copyright 2024 Red Hat, Inc.

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

package machinesetsync

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("MachineSetSync Reconciler", func() {
	var mgrCancel context.CancelFunc
	var mgrDone chan struct{}
	var mgr manager.Manager
	var reconciler *MachineSetSyncReconciler

	var namespace *corev1.Namespace
	var namespaceName string

	var machineSetBuilder machinev1resourcebuilder.MachineSetBuilder
	var machineset *machinev1beta1.MachineSet

	startManager := func(mgr *manager.Manager) (context.CancelFunc, chan struct{}) {
		mgrCtx, mgrCancel := context.WithCancel(context.Background())
		mgrDone := make(chan struct{})

		go func() {
			defer GinkgoRecover()
			defer close(mgrDone)

			Expect((*mgr).Start(mgrCtx)).To(Succeed())
		}()

		return mgrCancel, mgrDone
	}

	stopManager := func() {
		mgrCancel()
		// Wait for the mgrDone to be closed, which will happen once the mgr has stopped
		<-mgrDone
	}

	BeforeEach(func() {
		By("Setting up a namespace for the test")
		namespace = corev1resourcebuilder.Namespace().WithGenerateName("machineset-sync-controller-").Build()
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		namespaceName = namespace.GetName()

		By("Setting up the machineset builder")
		machineSetBuilder = machinev1resourcebuilder.MachineSet().
			WithNamespace(namespaceName).
			WithGenerateName("foo-").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec())

		By("Setting up a manager and controller")
		var err error
		mgr, err = ctrl.NewManager(cfg, ctrl.Options{
			Scheme: testScheme,
		})
		Expect(err).ToNot(HaveOccurred(), "Manager should be able to be created")

		reconciler = &MachineSetSyncReconciler{
			Client:   mgr.GetClient(),
			Platform: configv1.AWSPlatformType,
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed(), "Reconciler should be able to setup with manager")
	})

	AfterEach(func() {
		Expect(test.CleanupAndWait(ctx, k8sClient, machineset)).To(Succeed())
	})

	JustBeforeEach(func() {
		By("Starting the manager")
		mgrCancel, mgrDone = startManager(&mgr)
	})

	JustAfterEach(func() {
		By("Stopping the manager")
		stopManager()
	})

	Context("when the MAPI machine set has MachineAuthority set to Cluster API", func() {
		JustBeforeEach(func() {
			By("Creating the CAPI and MAPI MachineSets")
			mapiMachineSet = mapiMachineSetBuilder.Build()
			capiMachineSet = capiMachineSetBuilder.Build()

			Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

		})

		// For now only happy path
		It("should update the synchronized condition", func() {

			By("Setting the AuthoritativeAPI to ClusterAPI")
			Eventually(k.UpdateStatus(mapiMachineSet, func() {
				mapiMachineSet.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityClusterAPI
			})).Should(Succeed())

			Eventually(k.Object(mapiMachineSet), timeout).Should(
				HaveField("Status.Conditions", ContainElement(
					SatisfyAll(
						HaveField("Type", Equal(consts.SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
					))),
			)
		})

	})
})

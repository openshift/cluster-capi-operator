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

package machinesync

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("MachineSync Reconciler (AWS)", func() {
	var mgrCancel context.CancelFunc
	var mgrDone chan struct{}
	var mgr manager.Manager
	var reconciler *MachineSyncReconciler

	var namespace *corev1.Namespace
	var namespaceName string

	var machineBuilder machinev1resourcebuilder.MachineBuilder
	var machine *machinev1beta1.Machine

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
		namespace = corev1resourcebuilder.Namespace().WithGenerateName("machine-sync-controller-").Build()
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		namespaceName = namespace.GetName()

		By("Setting up the machine builder")
		machineBuilder = machinev1resourcebuilder.Machine().
			WithNamespace(namespaceName).
			WithGenerateName("foo").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec())

		By("Setting up a manager and controller")
		var err error
		mgr, err = ctrl.NewManager(cfg, ctrl.Options{
			Scheme: testScheme,
			Controller: config.Controller{
				SkipNameValidation: ptr.To(true),
			},
		})
		Expect(err).ToNot(HaveOccurred(), "Manager should be able to be created")

		reconciler = &MachineSyncReconciler{
			Client:   mgr.GetClient(),
			Platform: configv1.AWSPlatformType,
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed(), "Reconciler should be able to setup with manager")
	})

	AfterEach(func() {
		Expect(test.CleanupAndWait(ctx, k8sClient, machine)).To(Succeed())
	})

	JustBeforeEach(func() {
		By("Starting the manager")
		mgrCancel, mgrDone = startManager(&mgr)
	})

	JustAfterEach(func() {
		By("Stopping the manager")
		stopManager()
	})

	It("should reconcile without erroring", func() {
		machine = machineBuilder.Build()

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: namespaceName,
				Name:      machine.Name,
			},
		})
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("MachineSync Reconciler (OpenStack)", func() {
	var mgrCancel context.CancelFunc
	var mgrDone chan struct{}
	var mgr manager.Manager
	var reconciler *MachineSyncReconciler

	var namespace *corev1.Namespace
	var namespaceName string

	var machineBuilder machinev1resourcebuilder.MachineBuilder
	var machine *machinev1beta1.Machine

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
		namespace = corev1resourcebuilder.Namespace().WithGenerateName("machine-sync-controller-").Build()
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		namespaceName = namespace.GetName()

		By("Setting up the machine builder")
		machineBuilder = machinev1resourcebuilder.Machine().
			WithNamespace(namespaceName).
			WithGenerateName("foo").
			WithProviderSpecBuilder(machinev1resourcebuilder.OpenStackProviderSpec())

		By("Setting up a manager and controller")
		var err error
		mgr, err = ctrl.NewManager(cfg, ctrl.Options{
			Scheme: testScheme,
			Controller: config.Controller{
				SkipNameValidation: ptr.To(true),
			},
		})
		Expect(err).ToNot(HaveOccurred(), "Manager should be able to be created")

		reconciler = &MachineSyncReconciler{
			Client:   mgr.GetClient(),
			Platform: configv1.OpenStackPlatformType,
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed(), "Reconciler should be able to setup with manager")
	})

	AfterEach(func() {
		Expect(test.CleanupAndWait(ctx, k8sClient, machine)).To(Succeed())
	})

	JustBeforeEach(func() {
		By("Starting the manager")
		mgrCancel, mgrDone = startManager(&mgr)
	})

	JustAfterEach(func() {
		By("Stopping the manager")
		stopManager()
	})

	It("should reconcile without erroring", func() {
		machine = machineBuilder.Build()

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: namespaceName,
				Name:      machine.Name,
			},
		})
		Expect(err).ToNot(HaveOccurred())
	})
})

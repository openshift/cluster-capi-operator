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
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

var _ = Describe("MachineSync Controller", func() {
	var mgrCancel context.CancelFunc
	var mgrDone chan struct{}
	var mgr manager.Manager
	var reconciler *MachineSyncController

	var syncControllerNamespace *corev1.Namespace
	var capiNamespace *corev1.Namespace
	var mapiNamespace *corev1.Namespace

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
		syncControllerNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("machineset-sync-controller-").Build()
		Expect(k8sClient.Create(ctx, syncControllerNamespace)).To(Succeed(), "sync controller namespace should be able to be created")

		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Expect(k8sClient.Create(ctx, mapiNamespace)).To(Succeed(), "mapi namespace should be able to be created")

		capiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-cluster-api-").Build()
		Expect(k8sClient.Create(ctx, capiNamespace)).To(Succeed(), "capi namespace should be able to be created")

		By("Creating the cluster-api ClusterOperator")
		capiClusterOperator := &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster-api",
			},
		}
		Expect(k8sClient.Create(ctx, capiClusterOperator)).To(Succeed(), "should be able to create the 'cluster-api' ClusterOperator object")

		By("Setting up the machine builder")
		machineBuilder = machinev1resourcebuilder.Machine().
			WithNamespace(mapiNamespace.Name).
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

		reconciler = &MachineSyncController{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client:           mgr.GetClient(),
				ManagedNamespace: capiNamespace.Name,
			},
			Client:   mgr.GetClient(),
			Platform: configv1.AWSPlatformType,
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed(), "Reconciler should be able to setup with manager")
	})

	AfterEach(func() {
		By("Cleaning up MAPI test resources")
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, mapiNamespace.GetName(),
			&machinev1beta1.Machine{},
			&machinev1beta1.MachineSet{},
			&configv1.ClusterOperator{},
		)

		By("Cleaning up CAPI test resources")
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, capiNamespace.GetName(),
			&capiv1beta1.Machine{},
			&capiv1beta1.MachineSet{},
			&capav1.AWSCluster{},
			&capav1.AWSMachineTemplate{},
		)
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
				Namespace: capiNamespace.Name,
				Name:      machine.Name,
			},
		})
		Expect(err).ToNot(HaveOccurred())
	})

	// TODO: once real tests for the machinesync will be here, create some for
	// MachineSyncControllerAvailableCondition, MachineSyncControllerDegradedCondition.
})

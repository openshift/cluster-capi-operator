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
	capiv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	capav1builder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"

	corev1 "k8s.io/api/core/v1"
	awscapiv1beta1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

var _ = Describe("With a running controller", func() {
	var mgrCancel context.CancelFunc
	var mgrDone chan struct{}
	var mgr manager.Manager
	var k komega.Komega
	var reconciler *MachineSetSyncReconciler

	var syncControllerNamespace *corev1.Namespace
	var CapiNamespace *corev1.Namespace
	var MapiNamespace *corev1.Namespace

	var mapiMachineSetBuilder machinev1resourcebuilder.MachineSetBuilder
	var mapiMachineSet *machinev1beta1.MachineSet

	var capiMachineSetBuilder capiv1resourcebuilder.MachineSetBuilder
	var capiMachineSet *capiv1beta1.MachineSet

	var capaMachineTemplateBuilder capav1builder.AWSMachineTemplateBuilder
	var capaMachineTemplate *awscapiv1beta1.AWSMachineTemplate

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
		Expect(k8sClient.Create(ctx, syncControllerNamespace)).To(Succeed())

		MapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Expect(k8sClient.Create(ctx, MapiNamespace)).To(Succeed())

		CapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-cluster-api-").Build()
		Expect(k8sClient.Create(ctx, CapiNamespace)).To(Succeed())

		By("Setting up the builders")
		mapiMachineSetBuilder = machinev1resourcebuilder.MachineSet().
			WithNamespace(MapiNamespace.GetName()).
			WithName("foo").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec())

			// We need to build and create the CAPA MachineTemplate in order to
			// reference it on the CAPI MachineSet
		capaMachineTemplateBuilder = capav1builder.AWSMachineTemplate().
			WithNamespace(CapiNamespace.GetName()).
			WithGenerateName("machine-template-")

		capaMachineTemplate = capaMachineTemplateBuilder.Build()
		Expect(k8sClient.Create(ctx, capaMachineTemplate)).To(Succeed())

		capiMachineTemplate := capiv1beta1.MachineTemplateSpec{
			Spec: capiv1beta1.MachineSpec{
				InfrastructureRef: corev1.ObjectReference{
					Kind:      capaMachineTemplate.Kind,
					Name:      capaMachineTemplate.GetName(),
					Namespace: capaMachineTemplate.GetNamespace(),
				},
			},
		}
		capiMachineSetBuilder = capiv1resourcebuilder.MachineSet().
			WithNamespace(CapiNamespace.GetName()).
			WithName("foo").
			WithTemplate(capiMachineTemplate)

		By("Setting up a manager and controller")
		var err error
		mgr, err = ctrl.NewManager(cfg, ctrl.Options{
			Scheme: testScheme,
		})
		Expect(err).ToNot(HaveOccurred(), "Manager should be able to be created")

		reconciler = &MachineSetSyncReconciler{
			Client:        mgr.GetClient(),
			Platform:      configv1.AWSPlatformType,
			CAPINamespace: CapiNamespace.GetName(),
			MAPINamespace: MapiNamespace.GetName(),
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed(),
			"Reconciler should be able to setup with manager")

		k = komega.New(k8sClient)
	})

	AfterEach(func() {
		Expect(test.CleanupAndWait(
			ctx, k8sClient, mapiMachineSet, capiMachineSet, capaMachineTemplate,
		)).To(Succeed())
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
			mapiMachineSet = mapiMachineSetBuilder.
				WithAuthoritativeAPIStatus(machinev1beta1.MachineAuthorityClusterAPI).
				Build()

			capiMachineSet = capiMachineSetBuilder.Build()

			Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())
		})

		// For now only happy path
		It("should update the synchronized condition", func() {
			Eventually(k.Object(mapiMachineSet)).Should(
				HaveField("Status.Conditions", ContainElement(
					SatisfyAll(
						HaveField("Type", Equal(consts.SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
					))),
			)
		})

	})
})

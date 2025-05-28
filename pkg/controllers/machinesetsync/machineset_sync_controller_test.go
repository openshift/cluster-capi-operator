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
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"

	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"

	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/machinesync"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var _ = Describe("With a running MachineSetSync controller", func() {
	var mgrCancel context.CancelFunc
	var mgrDone chan struct{}
	var mgr manager.Manager
	var k komega.Komega
	var reconciler *MachineSetSyncReconciler

	var syncControllerNamespace *corev1.Namespace
	var capiNamespace *corev1.Namespace
	var mapiNamespace *corev1.Namespace

	var mapiMachineSetBuilder machinev1resourcebuilder.MachineSetBuilder
	var mapiMachineSet *machinev1beta1.MachineSet

	var capiMachineSetBuilder capiv1resourcebuilder.MachineSetBuilder
	var capiMachineSet *capiv1beta1.MachineSet

	var capaMachineTemplateBuilder capav1builder.AWSMachineTemplateBuilder
	var capaMachineTemplate *capav1.AWSMachineTemplate

	var capaClusterBuilder capav1builder.AWSClusterBuilder

	var capiClusterBuilder capiv1resourcebuilder.ClusterBuilder
	var capiCluster *capiv1beta1.Cluster
	var capiClusterOwnerReference []metav1.OwnerReference

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
		By("Setting up a namespaces for the test")
		syncControllerNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("machineset-sync-controller-").Build()
		Expect(k8sClient.Create(ctx, syncControllerNamespace)).To(Succeed(), "sync controller namespace should be able to be created")

		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Expect(k8sClient.Create(ctx, mapiNamespace)).To(Succeed(), "mapi namespace should be able to be created")

		capiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-cluster-api-").Build()
		Expect(k8sClient.Create(ctx, capiNamespace)).To(Succeed(), "capi namespace should be able to be created")

		mapiMachineSetBuilder = machinev1resourcebuilder.MachineSet().
			WithNamespace(mapiNamespace.GetName()).
			WithName("foo").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil))

		infrastructureName := "cluster-foo"
		capaClusterBuilder = capav1builder.AWSCluster().
			WithNamespace(capiNamespace.GetName()).
			WithName(infrastructureName)
		Expect(k8sClient.Create(ctx, capaClusterBuilder.Build())).To(Succeed(), "capa cluster should be able to be created")

		capiClusterBuilder = capiv1resourcebuilder.Cluster().WithNamespace(capiNamespace.GetName()).WithName(infrastructureName)
		Expect(k8sClient.Create(ctx, capiClusterBuilder.Build())).To(Succeed(), "capi cluster should be able to be created")

		capiCluster = &capiv1beta1.Cluster{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: infrastructureName, Namespace: capiNamespace.GetName()}, capiCluster)).To(Succeed())
		capiClusterOwnerReference = []metav1.OwnerReference{{
			APIVersion:         capiv1beta1.GroupVersion.String(),
			Kind:               capiv1beta1.ClusterKind,
			Name:               capiCluster.GetName(),
			UID:                capiCluster.GetUID(),
			Controller:         ptr.To(false),
			BlockOwnerDeletion: ptr.To(true),
		}}

		// We need to build and create the CAPA MachineTemplate in order to
		// reference it on the CAPI MachineSet
		capaMachineTemplateBuilder = capav1builder.AWSMachineTemplate().
			WithNamespace(capiNamespace.GetName()).
			WithName("machine-template")

		capaMachineTemplate = capaMachineTemplateBuilder.Build()

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
			WithNamespace(capiNamespace.GetName()).
			WithName("foo").
			WithTemplate(capiMachineTemplate).
			WithClusterName(infrastructureName)

		By("Setting up a manager and controller")
		var err error
		mgr, err = ctrl.NewManager(cfg, ctrl.Options{
			Scheme: testScheme,
			Controller: config.Controller{
				SkipNameValidation: ptr.To(true),
			},
		})
		Expect(err).ToNot(HaveOccurred(), "Manager should be able to be created")

		reconciler = &MachineSetSyncReconciler{
			Client: mgr.GetClient(),
			Infra: configv1resourcebuilder.Infrastructure().
				AsAWS("cluster", "us-east-1").WithInfrastructureName(infrastructureName).Build(),
			Platform:      configv1.AWSPlatformType,
			CAPINamespace: capiNamespace.GetName(),
			MAPINamespace: mapiNamespace.GetName(),
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed(),
			"Reconciler should be able to setup with manager")

		k = komega.New(k8sClient)

		By("Starting the manager")
		mgrCancel, mgrDone = startManager(&mgr)
	})

	AfterEach(func() {
		By("Stopping the manager")

		stopManager()
		Eventually(mgrDone, timeout).Should(BeClosed())

		By("Cleaning up MAPI test resources")
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, mapiNamespace.GetName(),
			&machinev1beta1.Machine{},
			&machinev1beta1.MachineSet{},
		)

		testutils.CleanupResources(Default, ctx, cfg, k8sClient, capiNamespace.GetName(),
			&capiv1beta1.Machine{},
			&capiv1beta1.MachineSet{},
			&capav1.AWSCluster{},
			&capav1.AWSMachineTemplate{},
		)
	})

	Context("when all the CAPI infra resources exist", func() {
		BeforeEach(func() {
			By("Creating the CAPI infra machine template")
			Expect(k8sClient.Create(ctx, capaMachineTemplate)).To(Succeed(), "capa machine template should be able to be created")
		})

		Context("when the MAPI machine set has MachineAuthority set to Machine API", func() {
			BeforeEach(func() {
				By("Creating the MAPI machine set")
				mapiMachineSet = mapiMachineSetBuilder.Build()
				Expect(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				By("Setting the MAPI machine set AuthoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())
			})

			Context("when the CAPI machine set does not exist", func() {
				It("should create the CAPI machine set", func() {
					Eventually(k.Get(
						capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build(),
					)).Should(Succeed())
				})

				It("should create MachineSet and InfraMachineTemplate with CAPI Cluster OwnerReference", func() {
					capiMachineSet := capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capiMachineSet)).Should(Succeed())
					Expect(capiMachineSet.OwnerReferences).To(Equal(capiClusterOwnerReference))

					capaMachineTemplate := capav1builder.AWSMachineTemplate().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capaMachineTemplate)).Should(Succeed())
					Expect(capaMachineTemplate.OwnerReferences).To(Equal(capiClusterOwnerReference))
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized MAPI MachineSet to CAPI")),
							))),
					)
				})

				It("should set the sync finalizer on both the mapi and capi machine sets", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
					)

					capiMachineSet := capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capiMachineSet)).Should(Succeed())
					Eventually(k.Object(capiMachineSet), timeout).Should(
						HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
					)
				})

				Context("when the MAPI machine set has a non-zero deletion timestamp", func() {
					BeforeEach(func() {
						Expect(k8sClient.Delete(ctx, mapiMachineSet)).To(Succeed())
					})
					It("should not create the CAPI machine set", func() {
						Consistently(k.Get(
							capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build(),
						), timeout).Should(Not(Succeed()))
					})

					It("should delete the MAPI machine set", func() {
						Eventually(k.Get(mapiMachineSet)).ShouldNot(Succeed())
					})
				})
			})

			Context("when the CAPI machine set does exist", func() {
				BeforeEach(func() {
					capiMachineSet = capiMachineSetBuilder.Build()
					capiMachineSet.SetFinalizers([]string{capiv1beta1.MachineSetFinalizer})
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
				})

				It("should update MachineSet and InfraMachineTemplate with CAPI Cluster OwnerReference", func() {
					capiMachineSet := capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					capaMachineTemplate := capav1builder.AWSMachineTemplate().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()

					Eventually(k.Object(capiMachineSet), timeout).Should(
						HaveField("OwnerReferences", Equal(capiClusterOwnerReference)),
					)

					Eventually(k.Object(capaMachineTemplate), timeout).Should(
						HaveField("OwnerReferences", Equal(capiClusterOwnerReference)),
					)
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized MAPI MachineSet to CAPI")),
							))),
					)
				})

				Context("when the MAPI machine set has a non-zero deletion timestamp", func() {
					BeforeEach(func() {
						Eventually(k.Object(mapiMachineSet), timeout).Should(
							HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
						)
						Eventually(k.Object(capiMachineSet), timeout).Should(
							SatisfyAll(
								HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
								HaveField("ObjectMeta.Finalizers", ContainElement(capiv1beta1.MachineSetFinalizer)),
							),
						)
						Expect(k8sClient.Delete(ctx, mapiMachineSet)).To(Succeed())
					})
					// Expect to see the finalizers, so they're in place before
					//  we Expect logic that relies on them to work
					It("should delete the CAPI machine set", func() {
						Eventually(k.Get(capiMachineSet), timeout).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "eventually capiMachineSet should not be found")
						// We don't want to re-create the machineset just deleted
						Consistently(k.Get(capiMachineSet), timeout).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "the capiMachineSet should not be recreated")
					})

					It("should delete the MAPI machine set", func() {
						Eventually(k.Get(mapiMachineSet), timeout).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "eventually mapiMachineSet should not be found")
						// We don't want to re-create the machineset just deleted
						Consistently(k.Get(mapiMachineSet), timeout).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "the mapiMachineSet should not be recreated")
					})
				})

				Context("when the CAPI machine set has a non-zero deletion timestamp", func() {
					BeforeEach(func() {
						Eventually(k.Object(mapiMachineSet), timeout).Should(
							HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
						)
						Eventually(k.Object(capiMachineSet), timeout).Should(
							HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
						)
						Expect(k8sClient.Delete(ctx, capiMachineSet)).To(Succeed())
					})
					It("should delete the MAPI machine set", func() {
						Eventually(k.Get(mapiMachineSet), timeout).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "eventually mapiMachineSet should not be found")
					})
				})
			})
		})

		Context("when the MAPI machine set has MachineAuthority set to Cluster API", func() {
			BeforeEach(func() {
				By("Creating the MAPI machine set")
				mapiMachineSet = mapiMachineSetBuilder.Build()
				Expect(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				By("Setting the MAPI machine set AuthoritativeAPI to ClusterAPI")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityClusterAPI
				})).Should(Succeed())
			})

			Context("when the CAPI machine set exists and the spec differs (replica count)", func() {
				BeforeEach(func() {
					By("Creating the CAPI machine set with a differing spec")
					capiMachineSet = capiMachineSetBuilder.WithReplicas(int32(4)).Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
				})

				It("should set the sync finalizer on both the mapi and capi machine sets", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
					)

					capiMachineSet := capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capiMachineSet)).Should(Succeed())
					Eventually(k.Object(capiMachineSet), timeout).Should(
						HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
					)
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						SatisfyAll(
							HaveField("Status.Conditions", ContainElement(
								SatisfyAll(
									HaveField("Type", Equal(consts.SynchronizedCondition)),
									HaveField("Status", Equal(corev1.ConditionTrue)),
								))),
							HaveField("Status.SynchronizedGeneration", Equal(capiMachineSet.GetGeneration())),
						))
				})

				It("should sync the spec of the machine sets (updating the replica count)", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Spec.Replicas", Equal(ptr.To(int32(4)))),
					)
				})
			})

			Context("when the CAPI machine set exists and the object meta differs", func() {
				Context("where the field is meant to be copied", func() {
					BeforeEach(func() {
						By("Creating the MAPI machine set with differing object meta in relevant field")
						capiMachineSet = capiMachineSetBuilder.WithLabels(map[string]string{"foo": "bar"}).Build()
						Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
					})

					It("should update the synchronized condition on the MAPI machine set to True", func() {
						Eventually(k.Object(mapiMachineSet), timeout).Should(
							SatisfyAll(
								HaveField("Status.Conditions", ContainElement(
									SatisfyAll(
										HaveField("Type", Equal(consts.SynchronizedCondition)),
										HaveField("Status", Equal(corev1.ConditionTrue)),
									))),
								HaveField("Status.SynchronizedGeneration", Equal(capiMachineSet.GetGeneration())),
							))
					})

					It("should update the labels", func() {
						Eventually(k.Object(mapiMachineSet), timeout).Should(
							HaveField("Labels", Equal(map[string]string{"foo": "bar"})),
						)
					})

				})

				Context("where the field is not meant to be copied", func() {
					BeforeEach(func() {
						By("Creating the CAPI machine set with differing object meta in non relevant field")
						capiMachineSet = capiMachineSetBuilder.Build()
						capiMachineSet.Finalizers = []string{"foo", "bar"}
						Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
					})

					It("should update the synchronized condition on the MAPI machine set to True", func() {
						Eventually(k.Object(mapiMachineSet), timeout).Should(
							SatisfyAll(
								HaveField("Status.Conditions", ContainElement(
									SatisfyAll(
										HaveField("Type", Equal(consts.SynchronizedCondition)),
										HaveField("Status", Equal(corev1.ConditionTrue)),
									))),
								HaveField("Status.SynchronizedGeneration", Equal(capiMachineSet.GetGeneration())),
							))
					})

					It("should not populate the field", func() {
						Eventually(k.Object(mapiMachineSet), timeout).Should(
							HaveField("Finalizers", Not(ContainElements("foo", "bar"))),
						)
					})

				})
			})

			Context("when the CAPI machine set exists and the conversion fails", func() {
				BeforeEach(func() {
					By("Creating the CAPI machine set")
					capiMachineSet = capiMachineSetBuilder.WithOwnerReferences(
						[]metav1.OwnerReference{
							{APIVersion: "FooAPIVersion", Kind: "FooKind", Name: "FooName", UID: "123"},
						}).Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine set to False", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionFalse)),
								HaveField("Severity", Equal(machinev1beta1.ConditionSeverityError)),
								HaveField("Reason", Equal("FailedToConvertCAPIMachineSetToMAPI")),
							))),
					)

				})
			})

			Context("when the CAPI machine set exists and the conversion has warnings", func() {
				// The AWS conversion library currently does not throw any warnings.
				// When we have a conversion that does, this test should be filled out.
				// We could also mock the conversion interface.
			})

			Context("when the CAPI machine set does not exist", func() {
				It("should create the CAPI machine set", func() {
					Eventually(k.Get(
						capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()),
					).Should(Succeed())
				})

				It("should set the sync finalizer on both the mapi and capi machine sets", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
					)

					capiMachineSet := capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capiMachineSet)).Should(Succeed())
					Eventually(k.Object(capiMachineSet), timeout).Should(
						HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
					)
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized MAPI MachineSet to CAPI")),
							))),
					)
				})
			})

			Context("when the CAPI machine set has a non-zero deletion timestamp", func() {
				BeforeEach(func() {
					capiMachineSet = capiMachineSetBuilder.Build()
					capiMachineSet.SetFinalizers([]string{capiv1beta1.MachineSetFinalizer})
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

					// Expect to see the finalizers, so they're in place before
					//  we Expect logic that relies on them to work
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
					)
					Eventually(k.Object(capiMachineSet), timeout).Should(SatisfyAll(
						HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
						HaveField("ObjectMeta.Finalizers", ContainElement(capiv1beta1.MachineSetFinalizer)),
					),
					)
					Expect(k8sClient.Delete(ctx, capiMachineSet)).To(Succeed())
				})

				Context("when the CAPI finalizer is removed", func() {
					// Mock the CAPI machine set controller removing the
					// finalizer that goes once all machines have been deleted.
					BeforeEach(func() {
						Eventually(k.Update(capiMachineSet, func() {
							capiMachineSet.SetFinalizers([]string{machinesync.SyncFinalizer})
						})).Should(Succeed())
					})

					It("should delete the MAPI machine set", func() {
						Eventually(k.Get(mapiMachineSet), timeout).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "eventually mapiMachineSet should not be found")

						// We don't want to re-create the machineset just deleted
						Consistently(k.Get(mapiMachineSet), timeout).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "the mapiMachineSet should not be recreated")
					})

					It("should delete the CAPI machine set", func() {
						Eventually(k.Get(capiMachineSet), timeout).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "eventually capiMachineSet should not be found")
						// We don't want to re-create the machineset just deleted
						Consistently(k.Get(capiMachineSet), timeout).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "the capiMachineSet should not be recreated")
					})
				})
			})

			Context("when the non-authoritative MAPI machine set has a non-zero deletion timestamp", func() {
				BeforeEach(func() {
					capiMachineSet = capiMachineSetBuilder.Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
					)
					Eventually(k.Object(capiMachineSet), timeout).Should(
						HaveField("ObjectMeta.Finalizers", ContainElement(machinesync.SyncFinalizer)),
					)
					Expect(k8sClient.Delete(ctx, mapiMachineSet)).To(Succeed())
				})

				It("should remove the machinesync finalizer from the CAPI machine set", func() {
					Eventually(k.Object(capiMachineSet), timeout).Should(
						HaveField("ObjectMeta.Finalizers", Not(ContainElement(machinesync.SyncFinalizer))),
					)
				})

				It("should delete the MAPI machine set", func() {
					Eventually(k.Get(mapiMachineSet), timeout).Should(WithTransform(apierrors.IsNotFound, BeTrue()), "eventually mapiMachineSet should not be found")

				})

				It("should not delete the CAPI machine set", func() {
					Consistently(k.Object(capiMachineSet), timeout).Should(
						HaveField("ObjectMeta.DeletionTimestamp", BeNil()),
					)
				})
			})
		})

		Context("when the MAPI machine set has MachineAuthority set to Migrating", func() {
			BeforeEach(func() {
				By("Creating the CAPI and MAPI machine sets")
				// We want a difference, so if we try to reconcile either way we
				// will get a new resourceversion
				mapiMachineSet = mapiMachineSetBuilder.WithReplicas(6).Build()
				capiMachineSet = capiMachineSetBuilder.WithReplicas(9).Build()

				Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
				Expect(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				By("Setting the AuthoritativeAPI to Migrating")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityMigrating
				})).Should(Succeed())
			})

			It("should not make any changes to either machineset", func() {
				// We want to make sure that this is the original ResourceVersion
				// since we haven't fetched the resource since it was created.
				mapiResourceVersion := mapiMachineSet.GetResourceVersion()
				capiResourceVersion := capiMachineSet.GetResourceVersion()
				Consistently(k.Object(mapiMachineSet), timeout).Should(
					HaveField("ResourceVersion", Equal(mapiResourceVersion)),
				)
				Consistently(k.Object(capiMachineSet), timeout).Should(
					HaveField("ResourceVersion", Equal(capiResourceVersion)),
				)
			})
		})

		Context("when the MAPI machine set has MachineAuthority not set", func() {
			BeforeEach(func() {
				By("Creating the CAPI and MAPI MachineSets")
				mapiMachineSet = mapiMachineSetBuilder.Build()
				capiMachineSet = capiMachineSetBuilder.Build()

				Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
				Expect(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				By("Setting the AuthoritativeAPI to Migrating")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = ""
				})).Should(Succeed())
			})

			It("should not make any changes", func() {
				resourceVersion := mapiMachineSet.GetResourceVersion()
				Consistently(k.Object(mapiMachineSet), timeout).Should(
					HaveField("ResourceVersion", Equal(resourceVersion)),
				)
			})
		})

		Context("when the MAPI machine set does not exist and the CAPI machine set does", func() {
			BeforeEach(func() {
				By("Creating the CAPI machine set")
				capiMachineSet = capiMachineSetBuilder.Build()

				Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
			})

			It("should not make any changes to the CAPI machine set", func() {
				resourceVersion := capiMachineSet.GetResourceVersion()
				Consistently(k.Object(capiMachineSet), timeout).Should(
					HaveField("ResourceVersion", Equal(resourceVersion)),
				)
			})

			It("should not create a MAPI machine set", func() {
				Consistently(k.ObjectList(&machinev1beta1.MachineSetList{}), timeout).ShouldNot(HaveField("Items",
					ContainElement(HaveField("ObjectMeta.Name", Equal(capiMachineSet.GetName()))),
				))
			})
		})
	})

	Context("when the CAPI infra machine template resource does not exist", func() {
		Context("when the MAPI machine set has MachineAuthority set to Machine API", func() {
			BeforeEach(func() {
				By("Creating MAPI machine set")
				mapiMachineSet = mapiMachineSetBuilder.Build()

				Expect(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				By("Setting the AuthoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())
			})

			Context("when the CAPI machine set does not exist", func() {
				It("should create the CAPI machine set", func() {
					capiMachineSet = capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capiMachineSet)).Should(Succeed())
				})

				It("should create the CAPI infra machine template", func() {
					capiInfraMachineTemplate := capav1builder.AWSMachineTemplate().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capiInfraMachineTemplate)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized MAPI MachineSet to CAPI")),
							))),
					)
				})
			})

			Context("when the CAPI machine set does exist", func() {
				BeforeEach(func() {
					capiMachineSet = capiMachineSetBuilder.Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
				})

				It("should create the CAPI infra machine template", func() {
					capiInfraMachineTemplate := capav1builder.AWSMachineTemplate().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capiInfraMachineTemplate)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized MAPI MachineSet to CAPI")),
							))),
					)
				})
			})
		})

		Context("when the MAPI machine set has MachineAuthority set to Cluster API", func() {
			BeforeEach(func() {
				By("Creating MAPI machine set")
				mapiMachineSet = mapiMachineSetBuilder.Build()
				Expect(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				By("Setting the AuthoritativeAPI to ClusterAPI")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityClusterAPI
				})).Should(Succeed())
			})

			Context("when the CAPI machine set does exist", func() {
				BeforeEach(func() {
					capiMachineSet = capiMachineSetBuilder.Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine set to False", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionFalse)),
								HaveField("Severity", Equal(machinev1beta1.ConditionSeverityError)),
								HaveField("Reason", Equal("FailedToGetCAPIInfraResources")),
							))),
					)
				})
			})

			Context("when the CAPI machine set does not exist", func() {
				It("should create the CAPI machine set", func() {
					capiMachineSet = capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
				})

				It("should create the CAPI infra machine template", func() {
					capiInfraMachineTemplate := capav1builder.AWSMachineTemplate().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capiInfraMachineTemplate)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized MAPI MachineSet to CAPI")),
							))),
					)
				})
			})
		})
	})

})

var _ = Describe("applySynchronizedConditionWithPatch", func() {
	var mapiNamespace *corev1.Namespace
	var reconciler *MachineSetSyncReconciler
	var mapiMachineSet *machinev1beta1.MachineSet
	var k komega.Komega

	BeforeEach(func() {
		k = komega.New(k8sClient)

		By("Setting up a namespace for the test")
		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Expect(k8sClient.Create(ctx, mapiNamespace)).To(Succeed(), "mapi namespace should be able to be created")

		By("Setting up the reconciler")
		reconciler = &MachineSetSyncReconciler{
			Client: k8sClient,
		}

		By("Create the MAPI Machine")
		mapiMachineSetBuilder := machinev1resourcebuilder.MachineSet().
			WithName("test-machineset").
			WithNamespace(mapiNamespace.Name)

		mapiMachineSet = mapiMachineSetBuilder.Build()
		mapiMachineSet.Spec.AuthoritativeAPI = machinev1beta1.MachineAuthorityMachineAPI
		Expect(k8sClient.Create(ctx, mapiMachineSet))

		By("Set the initial status of the MAPI Machine")
		Eventually(k.UpdateStatus(mapiMachineSet, func() {
			mapiMachineSet.Status.SynchronizedGeneration = int64(22)
			mapiMachineSet.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityMachineAPI
		})).Should(Succeed())

		By("Get the MAPI Machine from the API Server")
		mapiMachineSet = mapiMachineSetBuilder.Build()
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachineSet), mapiMachineSet)).Should(Succeed())

		// Artificially set the Generation to a made up number
		// as that can't be written directly to the API Server as it is read-only.
		mapiMachineSet.Generation = int64(23)
	})

	AfterEach(func() {
		By("Cleaning up MAPI test resources")
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, mapiNamespace.GetName(),
			&machinev1beta1.Machine{},
			&machinev1beta1.MachineSet{},
		)
	})

	Context("when condition status is False", func() {
		BeforeEach(func() {
			err := reconciler.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionFalse, "ErrorReason", "Error message", nil)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should add a Synchronized condition with status False and severity Error", func() {
			Eventually(k.Object(mapiMachineSet), timeout).Should(
				HaveField("Status.Conditions", ContainElement(
					SatisfyAll(
						HaveField("Type", Equal(consts.SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionFalse)),
						HaveField("Reason", Equal("ErrorReason")),
						HaveField("Message", Equal("Error message")),
						HaveField("Severity", Equal(machinev1beta1.ConditionSeverityError)),
					))),
			)
		})

		It("should keep SynchronizedGeneration unchanged", func() {
			Eventually(k.Object(mapiMachineSet), timeout).Should(
				HaveField("Status.SynchronizedGeneration", Equal(int64(22))),
			)
		})
	})

	Context("when condition status is Unknown", func() {
		BeforeEach(func() {
			err := reconciler.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionUnknown, "", "", nil)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should add a Synchronized condition with status Unknown and severity Info", func() {
			Eventually(k.Object(mapiMachineSet), timeout).Should(
				HaveField("Status.Conditions", ContainElement(
					SatisfyAll(
						HaveField("Type", Equal(consts.SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionUnknown)),
						HaveField("Reason", Equal("")),
						HaveField("Message", Equal("")),
						HaveField("Severity", Equal(machinev1beta1.ConditionSeverityInfo)),
					))),
			)
		})

		It("should keep SynchronizedGeneration unchanged", func() {
			Eventually(k.Object(mapiMachineSet), timeout).Should(
				HaveField("Status.SynchronizedGeneration", Equal(int64(22))),
			)
		})
	})

	Context("when condition status is True", func() {
		BeforeEach(func() {
			err := reconciler.applySynchronizedConditionWithPatch(ctx, mapiMachineSet, corev1.ConditionTrue, consts.ReasonResourceSynchronized, messageSuccessfullySynchronizedMAPItoCAPI, &mapiMachineSet.Generation)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should add a Synchronized condition with status True", func() {
			Eventually(k.Object(mapiMachineSet), timeout).Should(
				HaveField("Status.Conditions", ContainElement(
					SatisfyAll(
						HaveField("Type", Equal(consts.SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
						HaveField("Reason", Equal(consts.ReasonResourceSynchronized)),
						HaveField("Message", Equal("Successfully synchronized MAPI MachineSet to CAPI")),
						HaveField("Severity", Equal(machinev1beta1.ConditionSeverityNone)),
					))),
			)
		})

		It("should update status SynchronizedGeneration to the current Generation", func() {
			Eventually(k.Object(mapiMachineSet), timeout).Should(
				HaveField("Status.SynchronizedGeneration", Equal(int64(23))),
			)
		})
	})

})

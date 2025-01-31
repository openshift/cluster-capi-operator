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
	capov1builder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta1"
	capav1builder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"

	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"

	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var _ = Describe("With a running MachineSetSync controller (AWS)", func() {
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

		mapiMachineSetBuilder = machinev1resourcebuilder.MachineSet().
			WithNamespace(mapiNamespace.GetName()).
			WithName("foo").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil))

		infrastructureName := "cluster-foo"
		capaClusterBuilder = capav1builder.AWSCluster().
			WithNamespace(capiNamespace.GetName()).
			WithName(infrastructureName)
		Expect(k8sClient.Create(ctx, capaClusterBuilder.Build())).To(Succeed(), "capa cluster should be able to be created")

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

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
							))),
					)
				})
			})

			Context("when the CAPI machine set does exist", func() {
				BeforeEach(func() {
					capiMachineSet = capiMachineSetBuilder.Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
							))),
					)
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
							HaveField("Finalizers", BeEmpty()),
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

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
							))),
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
				It("should create the CAPI machine set and CAPI infra machine template", func() {
					capiMachineSet = capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

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
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
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
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
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
				It("should create the CAPI machine set and CAPI infra machine template", func() {
					capiMachineSet = capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

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
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
							))),
					)
				})
			})
		})
	})
})

var _ = Describe("With a running MachineSetSync controller (OpenStack)", func() {
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

	var capoMachineTemplateBuilder capov1builder.OpenStackMachineTemplateBuilder
	var capoMachineTemplate *capov1.OpenStackMachineTemplate

	var capoClusterBuilder capov1builder.OpenStackClusterBuilder

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

		mapiMachineSetBuilder = machinev1resourcebuilder.MachineSet().
			WithNamespace(mapiNamespace.GetName()).
			WithName("foo").
			WithProviderSpecBuilder(machinev1resourcebuilder.OpenStackProviderSpec())

		infrastructureName := "cluster-foo"
		capoClusterBuilder = capov1builder.OpenStackCluster().
			WithNamespace(capiNamespace.GetName()).
			WithName(infrastructureName)
		Expect(k8sClient.Create(ctx, capoClusterBuilder.Build())).To(Succeed(), "capo cluster should be able to be created")

		// We need to build and create the CAPO MachineTemplate in order to
		// reference it on the CAPI MachineSet
		capoMachineTemplateBuilder = capov1builder.OpenStackMachineTemplate().
			WithNamespace(capiNamespace.GetName()).
			WithName("machine-template").
			WithImage(capov1.ImageParam{Filter: &capov1.ImageFilter{Name: ptr.To("image-foo")}})

		capoMachineTemplate = capoMachineTemplateBuilder.Build()

		capiMachineTemplate := capiv1beta1.MachineTemplateSpec{
			Spec: capiv1beta1.MachineSpec{
				InfrastructureRef: corev1.ObjectReference{
					Kind:      capoMachineTemplate.Kind,
					Name:      capoMachineTemplate.GetName(),
					Namespace: capoMachineTemplate.GetNamespace(),
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
				AsOpenStack("cluster").WithInfrastructureName(infrastructureName).Build(),
			Platform:      configv1.OpenStackPlatformType,
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
			&capov1.OpenStackCluster{},
			&capov1.OpenStackMachineTemplate{},
		)
	})

	Context("when all the CAPI infra resources exist", func() {
		BeforeEach(func() {
			By("Creating the CAPI infra machine template")
			Expect(k8sClient.Create(ctx, capoMachineTemplate)).To(Succeed(), "capo machine template should be able to be created")
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

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
							))),
					)
				})
			})

			Context("when the CAPI machine set does exist", func() {
				BeforeEach(func() {
					capiMachineSet = capiMachineSetBuilder.Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
							))),
					)
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
							HaveField("Finalizers", BeEmpty()),
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
				// FIXME: The above is not true for OpenStack
			})

			Context("when the CAPI machine set does not exist", func() {
				It("should create the CAPI machine set", func() {
					Eventually(k.Get(
						capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()),
					).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
							))),
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
				It("should create the CAPI machine set and CAPI infra machine template", func() {
					capiMachineSet = capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

					capiInfraMachineTemplate := capov1builder.OpenStackMachineTemplate().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capiInfraMachineTemplate)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
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
					capiInfraMachineTemplate := capov1builder.OpenStackMachineTemplate().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capiInfraMachineTemplate)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
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
				It("should create the CAPI machine set and CAPI infra machine template", func() {
					capiMachineSet = capiv1resourcebuilder.MachineSet().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

					capiInfraMachineTemplate := capov1builder.OpenStackMachineTemplate().WithName(mapiMachineSet.Name).WithNamespace(capiNamespace.Name).Build()
					Eventually(k.Get(capiInfraMachineTemplate)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine set to True", func() {
					Eventually(k.Object(mapiMachineSet), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized CAPI MachineSet to MAPI")),
							))),
					)
				})
			})
		})
	})
})

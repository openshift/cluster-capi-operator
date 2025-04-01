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
	capiv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	capav1builder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capav1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var _ = Describe("With a running MachineSync Reconciler", func() {
	var mgrCancel context.CancelFunc
	var mgrDone chan struct{}
	var mgr manager.Manager
	var k komega.Komega
	var reconciler *MachineSyncReconciler

	var syncControllerNamespace *corev1.Namespace
	var capiNamespace *corev1.Namespace
	var mapiNamespace *corev1.Namespace

	var mapiMachineSetBuilder machinev1resourcebuilder.MachineSetBuilder

	var capiMachineSet *capiv1beta1.MachineSet
	var capiMachineSetBuilder capiv1resourcebuilder.MachineSetBuilder

	var mapiMachineBuilder machinev1resourcebuilder.MachineBuilder
	var mapiMachine *machinev1beta1.Machine

	var capiMachineBuilder capiv1resourcebuilder.MachineBuilder
	var capiMachine *capiv1beta1.Machine

	var capaMachineBuilder capav1builder.AWSMachineBuilder
	var capaMachine *capav1.AWSMachine

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
		By("Setting up a namespaces for the test")
		syncControllerNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("machine-sync-controller-").Build()
		Expect(k8sClient.Create(ctx, syncControllerNamespace)).To(Succeed(), "sync controller namespace should be able to be created")

		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Expect(k8sClient.Create(ctx, mapiNamespace)).To(Succeed(), "mapi namespace should be able to be created")

		capiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-cluster-api-").Build()
		Expect(k8sClient.Create(ctx, capiNamespace)).To(Succeed(), "capi namespace should be able to be created")

		infrastructureName := "cluster-foo"
		capaClusterBuilder = capav1builder.AWSCluster().
			WithNamespace(capiNamespace.GetName()).
			WithName(infrastructureName)
		Expect(k8sClient.Create(ctx, capaClusterBuilder.Build())).To(Succeed(), "capa cluster should be able to be created")

		// Create the CAPI Cluster to have valid owner reference to it
		capiClusterBuilder := capiv1resourcebuilder.Cluster().WithNamespace(capiNamespace.GetName()).WithName(infrastructureName)
		Expect(k8sClient.Create(ctx, capiClusterBuilder.Build())).To(Succeed(), "capi cluster should be able to be created")

		// We need to build and create the CAPA Machine in order to
		// reference it on the CAPI Machine
		capaMachineBuilder = capav1builder.AWSMachine().
			WithNamespace(capiNamespace.GetName()).
			WithName("machine-template")

		mapiMachineSetBuilder = machinev1resourcebuilder.MachineSet().
			WithNamespace(mapiNamespace.GetName()).
			WithName("foo-machineset").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil))

		// We need to build and create the CAPA MachineTemplate in order to
		// reference it on the CAPI MachineSet
		capaMachineTemplateBuilder := capav1builder.AWSMachineTemplate().
			WithNamespace(capiNamespace.GetName()).
			WithName("machine-template")

		capaMachineTemplate := capaMachineTemplateBuilder.Build()

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
			WithName("foo-machineset").
			WithTemplate(capiMachineTemplate).
			WithClusterName(infrastructureName)

		capaMachine = capaMachineBuilder.Build()

		capaMachineRef := corev1.ObjectReference{
			Kind:      capaMachine.Kind,
			Name:      capaMachine.GetName(),
			Namespace: capaMachine.GetNamespace(),
		}

		mapiMachineBuilder = machinev1resourcebuilder.Machine().
			WithNamespace(mapiNamespace.GetName()).
			WithGenerateName("foo").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil))

		capiMachineBuilder = capiv1resourcebuilder.Machine().
			WithNamespace(capiNamespace.GetName()).
			WithName("foo").
			WithInfrastructureRef(capaMachineRef).
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

		reconciler = &MachineSyncReconciler{
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
			By("Creating the CAPI infra machine")
			Expect(k8sClient.Create(ctx, capaMachine)).To(Succeed(), "capa machine should be able to be created")
		})

		Context("when the MAPI machine has MachineAuthority set to Machine API", func() {
			BeforeEach(func() {
				By("Creating the MAPI machine")
				mapiMachine = mapiMachineBuilder.Build()
				Expect(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				By("Setting the MAPI machine AuthoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())
			})

			Context("when the CAPI machine does not exist", func() {
				It("should create the CAPI machine", func() {
					Eventually(k.Get(
						capiv1resourcebuilder.Machine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build(),
					)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine to True", func() {
					Eventually(k.Object(mapiMachine), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized MAPI Machine to CAPI")),
							))),
					)
				})
			})

			Context("when the CAPI machine does exist", func() {
				BeforeEach(func() {
					capiMachine = capiMachineBuilder.Build()
					Expect(k8sClient.Create(ctx, capiMachine)).Should(Succeed())
				})

				It("should update the synchronized condition on the MAPI machine to True", func() {
					Eventually(k.Object(mapiMachine), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized MAPI Machine to CAPI")),
							))),
					)
				})
			})

			Context("when the MAPI machine providerSpec gets updated", func() {
				BeforeEach(func() {
					By("Updating the MAPI machine providerSpec")
					modifiedMAPIMachineBuilder := machinev1resourcebuilder.Machine().
						WithNamespace(mapiNamespace.GetName()).
						WithName(mapiMachine.Name).
						WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil).WithInstanceType("m6i.2xlarge")).Build()

					mapiMachineCopy := mapiMachine.DeepCopy()
					mapiMachineCopy.Spec.ProviderSpec = modifiedMAPIMachineBuilder.Spec.ProviderSpec

					Expect(k8sClient.Update(ctx, mapiMachineCopy)).Should(Succeed())
				})

				It("should recreate the CAPI infra machine", func() {
					capaMachineBuilder = capav1builder.AWSMachine().
						WithNamespace(capiNamespace.GetName()).
						WithName(mapiMachine.Name)

					Eventually(k.Object(capaMachineBuilder.Build()), timeout).Should(
						HaveField("Spec.InstanceType", Equal("m6i.2xlarge")),
					)
				})

				It("should update the synchronized condition on the MAPI machine to True", func() {
					Eventually(k.Object(mapiMachine), timeout).Should(
						HaveField("Status.Conditions", ContainElement(
							SatisfyAll(
								HaveField("Type", Equal(consts.SynchronizedCondition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
								HaveField("Reason", Equal("ResourceSynchronized")),
								HaveField("Message", Equal("Successfully synchronized MAPI Machine to CAPI")),
							))),
					)
				})
			})
		})

		Context("when the MAPI machine has MachineAuthority set to Cluster API", func() {
			BeforeEach(func() {

				By("Creating the MAPI machine")
				mapiMachine = mapiMachineBuilder.WithName("test-machine").Build()
				Expect(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				By("Creating the CAPI Machine")
				capiMachine = capiMachineBuilder.WithName("test-machine").Build()
				Expect(k8sClient.Create(ctx, capiMachine)).Should(Succeed())

				By("Setting the MAPI machine AuthoritativeAPI to Cluster API")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityClusterAPI
				})).Should(Succeed())

			})

			Context("when a MAPI counterpart exists", func() {
				Context("when the CAPI Provider Machine gets updated", func() {
					BeforeEach(func() {
						By("Updating the CAPI provider machine (CAPA Machine)")
						modifiedCapaMachine := capaMachineBuilder.WithInstanceType("m7i.4xlarge").Build()
						modifiedCapaMachine.ResourceVersion = capaMachine.GetResourceVersion()
						Expect(k8sClient.Update(ctx, modifiedCapaMachine)).Should(Succeed())
					})

					It("should update the MAPI provider spec", func() {
						Eventually(k.Object(mapiMachine), timeout).Should(
							WithTransform(awsProviderSpecFromMachine,
								HaveField("InstanceType", Equal("m7i.4xlarge")),
							))
					})

					It("should update the synchronized condition on the MAPI machine to True", func() {
						Eventually(k.Object(mapiMachine), timeout).Should(
							HaveField("Status.Conditions", ContainElement(
								SatisfyAll(
									HaveField("Type", Equal(consts.SynchronizedCondition)),
									HaveField("Status", Equal(corev1.ConditionTrue)),
									HaveField("Reason", Equal("ResourceSynchronized")),
									HaveField("Message", Equal("Successfully synchronized CAPI Machine to MAPI")),
								))),
						)
					})
				})
			})
		})

		Context("when the MAPI machine has MachineAuthority set to Machine API and has CPMS owner reference", func() {
			BeforeEach(func() {
				fakeCPMSOwnerReference := metav1.OwnerReference{
					APIVersion:         machinev1beta1.GroupVersion.String(),
					Kind:               "ControlPlaneMachineSet",
					Name:               "cluster",
					UID:                "cpms-uid",
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				}

				By("Creating the MAPI machine")
				mapiMachine = mapiMachineBuilder.WithOwnerReferences([]metav1.OwnerReference{fakeCPMSOwnerReference}).Build()
				Expect(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				By("Setting the MAPI machine AuthoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())
			})

			It("should not create the CAPI machine", func() {
				Consistently(k.Get(
					capiv1resourcebuilder.Machine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build(),
				)).Should(Not(Succeed()))
			})

			It("should update the synchronized condition on the MAPI machine to False", func() {
				Eventually(k.Object(mapiMachine), timeout).Should(
					HaveField("Status.Conditions", ContainElement(
						SatisfyAll(
							HaveField("Type", Equal(consts.SynchronizedCondition)),
							HaveField("Status", Equal(corev1.ConditionFalse)),
							HaveField("Reason", Equal("FailedToConvertMAPIMachineToCAPI")),
							HaveField("Message", Equal("conversion of control plane machines owned by control plane machine set is currently not supported")),
						))),
				)
			})

		})

		Context("when the MAPI machine has MachineAuthority set to Migrating", func() {
			BeforeEach(func() {
				By("Creating the CAPI and MAPI machines")
				// We want a difference, so if we try to reconcile either way we
				// will get a new resourceversion
				mapiMachine = mapiMachineBuilder.Build()
				capiMachine = capiMachineBuilder.Build()

				Expect(k8sClient.Create(ctx, capiMachine)).Should(Succeed())
				Expect(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				By("Setting the AuthoritativeAPI to Migrating")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityMigrating
				})).Should(Succeed())
			})

			It("should not make any changes to either machine", func() {
				// We want to make sure that this is the original ResourceVersion
				// since we haven't fetched the resource since it was created.
				mapiResourceVersion := mapiMachine.GetResourceVersion()
				capiResourceVersion := capiMachine.GetResourceVersion()
				Consistently(k.Object(mapiMachine), timeout).Should(
					HaveField("ResourceVersion", Equal(mapiResourceVersion)),
				)
				Consistently(k.Object(capiMachine), timeout).Should(
					HaveField("ResourceVersion", Equal(capiResourceVersion)),
				)
			})
		})

		Context("when the MAPI machine has MachineAuthority not set", func() {
			BeforeEach(func() {
				By("Creating the CAPI and MAPI Machines")
				mapiMachine = mapiMachineBuilder.Build()
				capiMachine = capiMachineBuilder.Build()

				Expect(k8sClient.Create(ctx, capiMachine)).Should(Succeed())
				Expect(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				By("Setting the AuthoritativeAPI to Migrating")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.AuthoritativeAPI = ""
				})).Should(Succeed())
			})

			It("should not make any changes", func() {
				resourceVersion := mapiMachine.GetResourceVersion()
				Consistently(k.Object(mapiMachine), timeout).Should(
					HaveField("ResourceVersion", Equal(resourceVersion)),
				)
			})
		})

		Context("when the MAPI machine does not exist and the CAPI machine does", func() {
			Context("and there is no CAPI machineSet owning the machine", func() {
				BeforeEach(func() {
					capiMachine = capiMachineBuilder.Build()
					Expect(k8sClient.Create(ctx, capiMachine)).Should(Succeed())
				})

				It("should not make any changes to the CAPI machine", func() {
					resourceVersion := capiMachine.GetResourceVersion()
					Consistently(k.Object(capiMachine), timeout).Should(
						HaveField("ResourceVersion", Equal(resourceVersion)),
					)
				})

				It("should not create a MAPI machine", func() {
					Consistently(k.ObjectList(&machinev1beta1.MachineList{}), timeout).ShouldNot(HaveField("Items",
						ContainElement(HaveField("ObjectMeta.Name", Equal(capiMachine.GetName()))),
					))
				})
			})

			Context("And there is a CAPI Machineset owning the machine", func() {
				var ownerReferencesToCapiMachineSet []metav1.OwnerReference
				BeforeEach(func() {
					By("Creating the CAPI machineset")
					capiMachineSet = capiMachineSetBuilder.Build()
					Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

					ownerReferencesToCapiMachineSet = []metav1.OwnerReference{{
						APIVersion:         capiv1beta1.GroupVersion.String(),
						Kind:               machineSetKind,
						Name:               capiMachineSet.Name,
						UID:                capiMachineSet.UID,
						Controller:         ptr.To(true),
						BlockOwnerDeletion: ptr.To(true),
					}}

					By("Creating the CAPI machine")
					capiMachine = capiMachineBuilder.WithOwnerReferences(ownerReferencesToCapiMachineSet).Build()
					Expect(k8sClient.Create(ctx, capiMachine)).Should(Succeed())
				})

				Context("with no MAPI counterpart", func() {
					It("should not make any changes to the CAPI machine", func() {
						resourceVersion := capiMachine.GetResourceVersion()
						Consistently(k.Object(capiMachine), timeout).Should(
							HaveField("ResourceVersion", Equal(resourceVersion)),
						)
					})

					It("should not create a MAPI machine", func() {
						Consistently(k.ObjectList(&machinev1beta1.MachineList{}), timeout).ShouldNot(HaveField("Items",
							ContainElement(HaveField("ObjectMeta.Name", Equal(capiMachine.GetName()))),
						))
					})
				})

				Context("with a MAPI counterpart", func() {
					BeforeEach(func() {
						mapiMachineSet := mapiMachineSetBuilder.Build()

						Expect(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())
					})

					It("should not make any changes to the CAPI machine", func() {
						resourceVersion := capiMachine.GetResourceVersion()
						Consistently(k.Object(capiMachine), timeout).Should(
							HaveField("ResourceVersion", Equal(resourceVersion)),
						)
					})

					It("should create a MAPI machine", func() {
						Eventually(k.ObjectList(&machinev1beta1.MachineList{}), timeout).Should(HaveField("Items",
							ContainElement(HaveField("ObjectMeta.Name", Equal(capiMachine.GetName()))),
						))

						mapiMachine = machinev1resourcebuilder.Machine().WithName(capiMachine.Name).WithNamespace(mapiNamespace.Name).Build()
						Eventually(k.Object(mapiMachine), timeout).Should(HaveField("ObjectMeta.OwnerReferences", ContainElement(
							SatisfyAll(
								HaveField("APIVersion", Equal(machinev1beta1.GroupVersion.String())),
								HaveField("Kind", Equal(machineSetKind)),
								HaveField("Name", Equal(capiMachineSet.Name)),
								HaveField("Controller", Equal(ptr.To(true))),
								HaveField("BlockOwnerDeletion", Equal(ptr.To(true))),
							),
						)))

					})

				})

			})

		})
	})

	Context("when the CAPI infra machine resource does not exist", func() {
		Context("when the MAPI machine is owned by a machineset", func() {
			var ownerReferencesToMapiMachineSet []metav1.OwnerReference

			BeforeEach(func() {
				By("Creating the MAPI machineset")
				mapiMachineSet := mapiMachineSetBuilder.Build()
				Expect(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				ownerReferencesToMapiMachineSet = []metav1.OwnerReference{{
					APIVersion:         machinev1beta1.GroupVersion.String(),
					Kind:               machineSetKind,
					Name:               mapiMachineSet.Name,
					UID:                mapiMachineSet.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				}}

				capiMachineSet := capiMachineSetBuilder.Build()
				Expect(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

			})
			Context("when the MAPI machine has MachineAuthority set to Machine API", func() {
				BeforeEach(func() {
					By("Creating MAPI machine")
					mapiMachine = mapiMachineBuilder.WithOwnerReferences(ownerReferencesToMapiMachineSet).Build()

					Expect(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

					By("Setting the AuthoritativeAPI to MachineAPI")
					Eventually(k.UpdateStatus(mapiMachine, func() {
						mapiMachine.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityMachineAPI
					})).Should(Succeed(), "should have succeeded updating the AuthoritativeAPI")
				})

				Context("when the CAPI machine does not exist", func() {
					It("should create the CAPI machine", func() {
						capiMachine = capiv1resourcebuilder.Machine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Get(capiMachine)).Should(Succeed(), "should have succeeded getting a CAPI Machine")
					})

					It("should have CAPI MachineSet OwnerReference", func() {
						capiMachine = capiv1resourcebuilder.Machine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Object(capiMachine), timeout).Should(HaveField("ObjectMeta.OwnerReferences", ContainElement(
							SatisfyAll(
								HaveField("Kind", Equal(machineSetKind)),
								HaveField("APIVersion", Equal(capiv1beta1.GroupVersion.String())),
							),
						)))
					})

					It("should create the CAPI infra machine", func() {
						capiInfraMachine := capav1builder.AWSMachine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Get(capiInfraMachine)).Should(Succeed(), "should have succeeded getting a CAPI Infra Machine")
					})

					It("should have Machine as an OwnerReference on the InfraMachine", func() {
						capiMachine = capiv1resourcebuilder.Machine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Get(capiMachine)).Should(Succeed(), "should have succeeded getting a CAPI Machine")

						capiInfraMachine := capav1builder.AWSMachine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Get(capiInfraMachine)).Should(Succeed(), "should have succeeded getting a CAPI Infra Machine")
						ownerReferencesOnMachine := []metav1.OwnerReference{{
							APIVersion:         capiv1beta1.GroupVersion.String(),
							Kind:               machineKind,
							Name:               capiMachine.Name,
							UID:                capiMachine.UID,
							Controller:         ptr.To(true),
							BlockOwnerDeletion: ptr.To(true),
						}}

						Expect(capiInfraMachine.OwnerReferences).To(Equal(ownerReferencesOnMachine))
					})

					It("should update the synchronized condition on the MAPI machine to True", func() {
						Eventually(k.Object(mapiMachine), timeout).Should(
							HaveField("Status.Conditions", ContainElement(
								SatisfyAll(
									HaveField("Type", Equal(consts.SynchronizedCondition)),
									HaveField("Status", Equal(corev1.ConditionTrue)),
									HaveField("Reason", Equal("ResourceSynchronized")),
									HaveField("Message", Equal("Successfully synchronized MAPI Machine to CAPI")),
								))),
						)
					})
				})

				Context("when the CAPI machine does exist", func() {
					BeforeEach(func() {
						capiMachine = capiMachineBuilder.Build()
						Expect(k8sClient.Create(ctx, capiMachine)).Should(Succeed())
					})

					It("should create the CAPI infra machine", func() {
						capiInfraMachine := capav1builder.AWSMachine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Get(capiInfraMachine)).Should(Succeed())
					})

					It("should update the synchronized condition on the MAPI machine to True", func() {
						Eventually(k.Object(mapiMachine), timeout).Should(
							HaveField("Status.Conditions", ContainElement(
								SatisfyAll(
									HaveField("Type", Equal(consts.SynchronizedCondition)),
									HaveField("Status", Equal(corev1.ConditionTrue)),
									HaveField("Reason", Equal("ResourceSynchronized")),
									HaveField("Message", Equal("Successfully synchronized MAPI Machine to CAPI")),
								))),
						)
					})
				})
			})

			Context("when the MAPI machine has MachineAuthority set to Cluster API", func() {
				BeforeEach(func() {
					By("Creating MAPI machine")
					mapiMachine = mapiMachineBuilder.WithName("test-machine").Build()

					Expect(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

					By("Setting the AuthoritativeAPI to Cluster API")
					Eventually(k.UpdateStatus(mapiMachine, func() {
						mapiMachine.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityClusterAPI
					})).Should(Succeed(), "should have succeeded updating the AuthoritativeAPI")
				})

				Context("when the CAPI machine exists", func() {
					BeforeEach(func() {
						capiMachine = capiMachineBuilder.WithName("test-machine").Build()
						Expect(k8sClient.Create(ctx, capiMachine)).Should(Succeed())
					})

					Context("and the InfraMachine does not exist", func() {
						It("should update the synchronized condition on the MAPI machine to False", func() {
							Eventually(k.Object(mapiMachine), timeout).Should(
								HaveField("Status.Conditions", ContainElement(
									SatisfyAll(
										HaveField("Type", Equal(consts.SynchronizedCondition)),
										HaveField("Status", Equal(corev1.ConditionFalse)),
										HaveField("Reason", Equal("FailedToGetCAPIInfraResources")),
										HaveField("Message", ContainSubstring("failed to get Cluster API infrastructure machine")),
									))),
							)
						})
					})
				})

				Context("when the CAPI machine does not exist", func() {
					It("should update the synchronized condition on the MAPI machine to False", func() {
						Eventually(k.Object(mapiMachine), timeout).Should(
							HaveField("Status.Conditions", ContainElement(
								SatisfyAll(
									HaveField("Type", Equal(consts.SynchronizedCondition)),
									HaveField("Status", Equal(corev1.ConditionFalse)),
									HaveField("Reason", Equal("CAPIMachineNotFound")),
									HaveField("Message", Equal("Cluster API machine not found")),
								))),
						)
					})
				})

			})
		})
	})
})

var _ = Describe("applySynchronizedConditionWithPatch", func() {
	var mapiNamespace *corev1.Namespace
	var reconciler *MachineSyncReconciler
	var mapiMachine *machinev1beta1.Machine
	var k komega.Komega

	BeforeEach(func() {
		k = komega.New(k8sClient)

		By("Setting up a namespace for the test")
		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Expect(k8sClient.Create(ctx, mapiNamespace)).To(Succeed(), "mapi namespace should be able to be created")

		By("Setting up the reconciler")
		reconciler = &MachineSyncReconciler{
			Client: k8sClient,
		}

		By("Create the MAPI Machine")
		mapiMachineBuilder := machinev1resourcebuilder.Machine().
			WithName("test-machine").
			WithNamespace(mapiNamespace.Name)

		mapiMachine = mapiMachineBuilder.Build()
		mapiMachine.Spec.AuthoritativeAPI = machinev1beta1.MachineAuthorityMachineAPI
		Expect(k8sClient.Create(ctx, mapiMachine))

		By("Set the initial status of the MAPI Machine")
		Eventually(k.UpdateStatus(mapiMachine, func() {
			mapiMachine.Status.SynchronizedGeneration = int64(22)
			mapiMachine.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityMachineAPI
		})).Should(Succeed())

		By("Get the MAPI Machine from the API Server")
		mapiMachine = mapiMachineBuilder.Build()
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachine), mapiMachine)).Should(Succeed())

		// Artificially set the Generation to a made up number
		// as that can't be written directly to the API Server as it is read-only.
		mapiMachine.Generation = int64(23)
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
			err := reconciler.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, "ErrorReason", "Error message", nil)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should add a Synchronized condition with status False and severity Error", func() {
			Eventually(k.Object(mapiMachine), timeout).Should(
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
			Eventually(k.Object(mapiMachine), timeout).Should(
				HaveField("Status.SynchronizedGeneration", Equal(int64(22))),
			)
		})
	})

	Context("when condition status is Unknown", func() {
		BeforeEach(func() {
			err := reconciler.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionUnknown, reasonProgressingToCreateCAPIInfraMachine, progressingToSynchronizeMAPItoCAPI, nil)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should add a Synchronized condition with status Unknown and severity Info", func() {
			Eventually(k.Object(mapiMachine), timeout).Should(
				HaveField("Status.Conditions", ContainElement(
					SatisfyAll(
						HaveField("Type", Equal(consts.SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionUnknown)),
						HaveField("Reason", Equal("ProgressingToCreateCAPIInfraMachine")),
						HaveField("Message", Equal("Progressing to synchronize MAPI Machine to CAPI")),
						HaveField("Severity", Equal(machinev1beta1.ConditionSeverityInfo)),
					))),
			)
		})

		It("should keep SynchronizedGeneration unchanged", func() {
			Eventually(k.Object(mapiMachine), timeout).Should(
				HaveField("Status.SynchronizedGeneration", Equal(int64(22))),
			)
		})
	})

	Context("when condition status is True", func() {
		BeforeEach(func() {
			err := reconciler.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionTrue, consts.ReasonResourceSynchronized, messageSuccessfullySynchronizedMAPItoCAPI, &mapiMachine.Generation)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should add a Synchronized condition with status True", func() {
			Eventually(k.Object(mapiMachine), timeout).Should(
				HaveField("Status.Conditions", ContainElement(
					SatisfyAll(
						HaveField("Type", Equal(consts.SynchronizedCondition)),
						HaveField("Status", Equal(corev1.ConditionTrue)),
						HaveField("Reason", Equal(consts.ReasonResourceSynchronized)),
						HaveField("Message", Equal("Successfully synchronized MAPI Machine to CAPI")),
						HaveField("Severity", Equal(machinev1beta1.ConditionSeverityNone)),
					))),
			)
		})

		It("should update status SynchronizedGeneration to the current Generation", func() {
			Eventually(k.Object(mapiMachine), timeout).Should(
				HaveField("Status.SynchronizedGeneration", Equal(int64(23))),
			)
		})
	})

})

// awsProviderSpecFromMachine wraps AWSProviderSpecFromRawExtension for use with WithTransform.
func awsProviderSpecFromMachine(mapiMachine *machinev1beta1.Machine) (machinev1beta1.AWSMachineProviderConfig, error) {
	if mapiMachine == nil {
		return machinev1beta1.AWSMachineProviderConfig{}, nil
	}

	return mapi2capi.AWSProviderSpecFromRawExtension(mapiMachine.Spec.ProviderSpec.Value)
}

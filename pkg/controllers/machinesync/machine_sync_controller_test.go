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
	"errors"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	clusterv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	awsv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	admissiontestutils "github.com/openshift/cluster-capi-operator/pkg/admissionpolicy/testutils"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/conversion/mapi2capi"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
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

	var capiMachineSet *clusterv1.MachineSet
	var capiMachineSetBuilder clusterv1resourcebuilder.MachineSetBuilder

	var mapiMachineBuilder machinev1resourcebuilder.MachineBuilder
	var mapiMachine *mapiv1beta1.Machine

	var capiMachineBuilder clusterv1resourcebuilder.MachineBuilder
	var capiMachine *clusterv1.Machine

	var capaMachineBuilder awsv1resourcebuilder.AWSMachineBuilder
	var capaMachine *awsv1.AWSMachine

	var capaClusterBuilder awsv1resourcebuilder.AWSClusterBuilder

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
		Eventually(k8sClient.Create(ctx, syncControllerNamespace)).Should(Succeed(), "sync controller namespace should be able to be created")

		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Eventually(k8sClient.Create(ctx, mapiNamespace)).Should(Succeed(), "mapi namespace should be able to be created")

		capiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-cluster-api-").Build()
		Eventually(k8sClient.Create(ctx, capiNamespace)).Should(Succeed(), "capi namespace should be able to be created")

		infrastructureName := "cluster-foo"
		capaClusterBuilder = awsv1resourcebuilder.AWSCluster().
			WithNamespace(capiNamespace.GetName()).
			WithName(infrastructureName)
		Eventually(k8sClient.Create(ctx, capaClusterBuilder.Build())).Should(Succeed(), "capa cluster should be able to be created")

		// Create the CAPI Cluster to have valid owner reference to it
		capiClusterBuilder := clusterv1resourcebuilder.Cluster().WithNamespace(capiNamespace.GetName()).WithName(infrastructureName)
		Eventually(k8sClient.Create(ctx, capiClusterBuilder.Build())).Should(Succeed(), "capi cluster should be able to be created")

		// We need to build and create the CAPA Machine in order to
		// reference it on the CAPI Machine
		capaMachineBuilder = awsv1resourcebuilder.AWSMachine().
			WithNamespace(capiNamespace.GetName()).
			WithName("foo")

		mapiMachineSetBuilder = machinev1resourcebuilder.MachineSet().
			WithNamespace(mapiNamespace.GetName()).
			WithName("foo-machineset").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil))

		// We need to build and create the CAPA MachineTemplate in order to
		// reference it on the CAPI MachineSet
		capaMachineTemplateBuilder := awsv1resourcebuilder.AWSMachineTemplate().
			WithNamespace(capiNamespace.GetName()).
			WithName("foo")

		capaMachineTemplate := capaMachineTemplateBuilder.Build()

		capiMachineTemplate := clusterv1.MachineTemplateSpec{
			Spec: clusterv1.MachineSpec{
				InfrastructureRef: corev1.ObjectReference{
					Kind:      capaMachineTemplate.Kind,
					Name:      capaMachineTemplate.GetName(),
					Namespace: capaMachineTemplate.GetNamespace(),
				},
			},
		}

		capiMachineSetBuilder = clusterv1resourcebuilder.MachineSet().
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
			WithName("foo").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil))

		capiMachineBuilder = clusterv1resourcebuilder.Machine().
			WithNamespace(capiNamespace.GetName()).
			WithName("foo").
			WithInfrastructureRef(capaMachineRef).
			WithClusterName(infrastructureName)

		By("Setting up a manager and controller")
		// Adds new user to the api server so that the controller
		// can be a different user to the one we use to manipulate test resources
		var err error
		var controllerCfg *rest.Config

		controllerCfg, err = testEnv.ControlPlane.APIServer.SecureServing.AddUser(envtest.User{
			Name:   "system:serviceaccount:openshift-cluster-api:cluster-capi-operator",
			Groups: []string{"system:masters", "system:authenticated"},
		}, cfg)
		Expect(err).ToNot(HaveOccurred(), "User be able to be created")

		mgr, err = ctrl.NewManager(controllerCfg, ctrl.Options{
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
			&mapiv1beta1.Machine{},
			&mapiv1beta1.MachineSet{},
		)

		testutils.CleanupResources(Default, ctx, cfg, k8sClient, capiNamespace.GetName(),
			&clusterv1.Machine{},
			&clusterv1.MachineSet{},
			&awsv1.AWSCluster{},
			&awsv1.AWSMachineTemplate{},
		)
	})

	Context("when all the CAPI infra resources exist", func() {
		BeforeEach(func() {
			By("Creating the CAPI infra machine")
			Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed(), "capa machine should be able to be created")
		})

		Context("when the MAPI machine has MachineAuthority set to Machine API", func() {
			BeforeEach(func() {
				By("Creating the MAPI machine")
				mapiMachine = mapiMachineBuilder.Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed(), "mapi machine should be able to be created")

				By("Setting the MAPI machine AuthoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())
			})

			Context("when the CAPI machine does not exist", func() {
				It("should create a paused CAPI machine", func() {
					Eventually(k.Get(
						clusterv1resourcebuilder.Machine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build(),
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
					By("Creating the CAPI machine")
					capiMachine = capiMachineBuilder.Build()
					Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed(), "capi machine should be able to be created")
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

					Eventually(k.Update(mapiMachine, func() {
						mapiMachine.Spec.ProviderSpec = modifiedMAPIMachineBuilder.Spec.ProviderSpec
					})).Should(Succeed(), "mapi machine providerSpec should be able to be updated")
				})

				It("should recreate the CAPI infra machine", func() {
					capaMachineBuilder = awsv1resourcebuilder.AWSMachine().
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
				mapiMachine = mapiMachineBuilder.Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed(), "mapi machine should be able to be created")

				By("Creating the CAPI Machine")
				capiMachine = capiMachineBuilder.Build()
				Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed(), "capi machine should be able to be created")

				By("Setting the MAPI machine AuthoritativeAPI to Cluster API")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
				})).Should(Succeed())

			})

			Context("when a MAPI counterpart exists", func() {
				Context("when the CAPI Infra Machine gets updated", func() {
					BeforeEach(func() {
						By("Updating the CAPI Infra Machine (CAPA Machine)")
						modifiedCapaMachine := capaMachineBuilder.WithInstanceType("m7i.4xlarge").Build()
						modifiedCapaMachine.ResourceVersion = capaMachine.GetResourceVersion()
						Eventually(k8sClient.Update(ctx, modifiedCapaMachine)).Should(Succeed(), "capa machine should be able to be updated")
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

		Context("when the MAPI machine has status.authoritativeAPI set to MachineAPI and has CPMS owner reference", func() {
			BeforeEach(func() {
				fakeCPMSOwnerReference := metav1.OwnerReference{
					APIVersion:         mapiv1beta1.GroupVersion.String(),
					Kind:               "ControlPlaneMachineSet",
					Name:               "cluster",
					UID:                "cpms-uid",
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				}

				By("Creating the MAPI machine")
				mapiMachine = mapiMachineBuilder.WithOwnerReferences([]metav1.OwnerReference{fakeCPMSOwnerReference}).Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed(), "mapi machine should be able to be created")

				By("Setting the MAPI machine status.authoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())
			})

			It("should not create the CAPI machine", func() {
				Consistently(k.Get(
					clusterv1resourcebuilder.Machine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build(),
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

		Context("when the MAPI machine has status.authoritativeAPI set to MachineAPI and has no owner references", func() {
			BeforeEach(func() {
				By("Creating the MAPI machine")
				mapiMachine = mapiMachineBuilder.Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed(), "mapi machine should be able to be created")

				By("Setting the MAPI machine status.authoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())
			})

			It("should successfully create the CAPI machine", func() {
				Eventually(k.Get(
					clusterv1resourcebuilder.Machine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build(),
				)).Should(Succeed())
			})

			It("should successfully create the CAPA machine", func() {
				Eventually(k.Get(
					awsv1resourcebuilder.AWSMachine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build(),
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

		Context("when the MAPI machine has status.authoritativeAPI set to Migrating", func() {
			BeforeEach(func() {
				By("Creating the CAPI and MAPI machines")
				// We want a difference, so if we try to reconcile either way we
				// will get a new resourceversion
				mapiMachine = mapiMachineBuilder.Build()
				capiMachine = capiMachineBuilder.Build()

				Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed(), "capi machine should be able to be created")
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed(), "mapi machine should be able to be created")

				By("Setting the status.authoritativeAPI to Migrating")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMigrating
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

		Context("when the MAPI machine has status.authoritativeAPI not set", func() {
			BeforeEach(func() {
				By("Creating the CAPI and MAPI Machines")
				mapiMachine = mapiMachineBuilder.Build()
				capiMachine = capiMachineBuilder.Build()

				Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed(), "capi machine should be able to be created")
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed(), "mapi machine should be able to be created")

				By("Setting the status.authoritativeAPI to Migrating")
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
					By("Creating the CAPI machine")
					capiMachine = capiMachineBuilder.Build()
					Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed(), "capi machine should be able to be created")

					By("Updating the CAPA machine adding the CAPI machine as an owner")
					Eventually(k.Update(capaMachine, func() {
						capaMachine.OwnerReferences = append(capaMachine.OwnerReferences, metav1.OwnerReference{
							Kind:               machineKind,
							APIVersion:         clusterv1.GroupVersion.String(),
							Name:               capiMachine.Name,
							UID:                capiMachine.UID,
							BlockOwnerDeletion: ptr.To(true),
							Controller:         ptr.To(false),
						})
					})).Should(Succeed(), "capa machine should be able to be updated")
				})

				It("should not make any changes to the CAPI machine", func() {
					resourceVersion := capiMachine.GetResourceVersion()
					Consistently(k.Object(capiMachine), timeout).Should(
						HaveField("ResourceVersion", Equal(resourceVersion)),
					)
				})

				It("should not create a MAPI machine", func() {
					Consistently(k.ObjectList(&mapiv1beta1.MachineList{}), timeout).ShouldNot(HaveField("Items",
						ContainElement(HaveField("ObjectMeta.Name", Equal(capiMachine.GetName()))),
					))
				})

				Context("when MAPI machine with the same name and status.authoritativeAPI set to ClusterAPI is created", func() {
					BeforeEach(func() {
						mapiMachine = mapiMachineBuilder.WithName(capiMachine.Name).WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).Build()
						Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed(), "mapi machine should be able to be created")

						By("Setting the status.authoritativeAPI to Cluster API")
						Eventually(k.UpdateStatus(mapiMachine, func() {
							mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
						})).Should(Succeed())
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

			Context("And there is a CAPI Machineset owning the machine", func() {
				var ownerReferencesToCapiMachineSet []metav1.OwnerReference
				BeforeEach(func() {
					By("Creating the CAPI machineset")
					capiMachineSet = capiMachineSetBuilder.Build()
					Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed(), "capi machine set should be able to be created")

					ownerReferencesToCapiMachineSet = []metav1.OwnerReference{{
						APIVersion:         clusterv1.GroupVersion.String(),
						Kind:               machineSetKind,
						Name:               capiMachineSet.Name,
						UID:                capiMachineSet.UID,
						Controller:         ptr.To(true),
						BlockOwnerDeletion: ptr.To(true),
					}}

					By("Creating the CAPI machine")
					capiMachine = capiMachineBuilder.WithOwnerReferences(ownerReferencesToCapiMachineSet).Build()
					Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed(), "capi machine should be able to be created")
				})

				Context("with no MAPI counterpart", func() {
					It("should not make any changes to the CAPI machine", func() {
						resourceVersion := capiMachine.GetResourceVersion()
						Consistently(k.Object(capiMachine), timeout).Should(
							HaveField("ResourceVersion", Equal(resourceVersion)),
						)
					})

					It("should not create a MAPI machine", func() {
						Consistently(k.ObjectList(&mapiv1beta1.MachineList{}), timeout).ShouldNot(HaveField("Items",
							ContainElement(HaveField("ObjectMeta.Name", Equal(capiMachine.GetName()))),
						))
					})
				})

				Context("with a MAPI counterpart", func() {
					BeforeEach(func() {
						mapiMachineSet := mapiMachineSetBuilder.Build()

						Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed(), "mapi machine set should be able to be created")
					})

					// We now set finalizers regardless, so this does not work any more.

					// It("should not make any changes to the CAPI machine", func() {
					// 	resourceVersion := capiMachine.GetResourceVersion()
					// 	Consistently(k.Object(capiMachine), timeout).Should(
					// 		HaveField("ResourceVersion", Equal(resourceVersion)),
					// 	)
					// })

					It("should create a MAPI machine", func() {
						Eventually(k.ObjectList(&mapiv1beta1.MachineList{}), timeout).Should(HaveField("Items",
							ContainElement(HaveField("ObjectMeta.Name", Equal(capiMachine.GetName()))),
						))

						mapiMachine = machinev1resourcebuilder.Machine().WithName(capiMachine.Name).WithNamespace(mapiNamespace.Name).Build()
						Eventually(k.Object(mapiMachine), timeout).Should(HaveField("ObjectMeta.OwnerReferences", ContainElement(
							SatisfyAll(
								HaveField("APIVersion", Equal(mapiv1beta1.GroupVersion.String())),
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
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed(), "mapi machine set should be able to be created")

				ownerReferencesToMapiMachineSet = []metav1.OwnerReference{{
					APIVersion:         mapiv1beta1.GroupVersion.String(),
					Kind:               machineSetKind,
					Name:               mapiMachineSet.Name,
					UID:                mapiMachineSet.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				}}

				capiMachineSet := capiMachineSetBuilder.Build()
				Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed(), "capi machine set should be able to be created")

			})
			Context("when the MAPI machine has MachineAuthority set to Machine API", func() {
				BeforeEach(func() {
					By("Creating MAPI machine")
					mapiMachine = mapiMachineBuilder.WithOwnerReferences(ownerReferencesToMapiMachineSet).Build()

					Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed(), "mapi machine should be able to be created")

					By("Setting the AuthoritativeAPI to MachineAPI")
					Eventually(k.UpdateStatus(mapiMachine, func() {
						mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
					})).Should(Succeed(), "should have succeeded updating the AuthoritativeAPI")
				})

				Context("when the CAPI machine does not exist", func() {
					It("should create the CAPI machine", func() {
						capiMachine = clusterv1resourcebuilder.Machine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Get(capiMachine), timeout).Should(Succeed(), "should have succeeded getting a CAPI Machine")
					})

					It("should have CAPI MachineSet OwnerReference", func() {
						capiMachine = clusterv1resourcebuilder.Machine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Object(capiMachine), timeout).Should(HaveField("ObjectMeta.OwnerReferences", ContainElement(
							SatisfyAll(
								HaveField("Kind", Equal(machineSetKind)),
								HaveField("APIVersion", Equal(clusterv1.GroupVersion.String())),
							),
						)))
					})

					It("should create the CAPI infra machine", func() {
						capiInfraMachine := awsv1resourcebuilder.AWSMachine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Get(capiInfraMachine), timeout).Should(Succeed(), "should have succeeded getting a CAPI Infra Machine")
					})

					It("should have Machine as an OwnerReference on the InfraMachine", func() {
						capiMachine = clusterv1resourcebuilder.Machine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Get(capiMachine), timeout).Should(Succeed(), "should have succeeded getting a CAPI Machine")

						capiInfraMachine := awsv1resourcebuilder.AWSMachine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Get(capiInfraMachine), timeout).Should(Succeed(), "should have succeeded getting a CAPI Infra Machine")
						ownerReferencesOnMachine := []metav1.OwnerReference{{
							APIVersion:         clusterv1.GroupVersion.String(),
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
						Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed(), "capi machine should be able to be created")
					})

					It("should create the CAPI infra machine", func() {
						capiInfraMachine := awsv1resourcebuilder.AWSMachine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Get(capiInfraMachine), timeout).Should(Succeed())
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

					Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed(), "mapi machine should be able to be created")

					By("Setting the AuthoritativeAPI to Cluster API")
					Eventually(k.UpdateStatus(mapiMachine, func() {
						mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
					})).Should(Succeed(), "should have succeeded updating the AuthoritativeAPI")
				})

				Context("when the CAPI machine exists", func() {
					BeforeEach(func() {
						capiMachine = capiMachineBuilder.WithName("test-machine").Build()
						Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed(), "capi machine should be able to be created")
					})

					Context("and the InfraMachine does not exist", func() {
						It("should update the synchronized condition on the MAPI machine to False", func() {
							// TODO: Revert this to a useful error message.
							// We changed how fetchCAPIInfraResources behaves, so now we fail at a later point in the code.
							// This still fails, the behaviour is the same - it's just a less useful error for an end user.
							// We need to revisit fetchCAPIInfraResources.
							Eventually(k.Object(mapiMachine), timeout).Should(
								HaveField("Status.Conditions", ContainElement(
									SatisfyAll(
										HaveField("Type", Equal(consts.SynchronizedCondition)),
										HaveField("Status", Equal(corev1.ConditionFalse)),
										HaveField("Reason", Equal("FailedToConvertCAPIMachineToMAPI")),
										HaveField("Message", ContainSubstring("unexpected InfraMachine type, expected AWSMachine, got <nil>")),
									))),
							)
						})
					})
				})

				Context("when the CAPI machine does not exist", func() {
					It("should create the CAPI machine", func() {
						capiMachine := capiMachineBuilder.WithName("test-machine").Build()
						Eventually(k.Get(capiMachine), timeout).Should(Succeed())
						Eventually(k.Object(capiMachine), timeout).ShouldNot(
							HaveField("ObjectMeta.Annotations", ContainElement(
								HaveKeyWithValue(clusterv1.PausedAnnotation, ""),
							)))
					})

					It("should create the CAPI Infra machine", func() {
						capiInfraMachine := awsv1resourcebuilder.AWSMachine().WithName(mapiMachine.Name).WithNamespace(capiNamespace.Name).Build()
						Eventually(k.Get(capiInfraMachine), timeout).Should(Succeed())
						Eventually(k.Object(capiInfraMachine), timeout).ShouldNot(
							HaveField("ObjectMeta.Annotations", ContainElement(
								HaveKeyWithValue(clusterv1.PausedAnnotation, ""),
							)))
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
	})

	Context("validating admission policy tests", func() {
		var (
			policyBinding *admissionregistrationv1.ValidatingAdmissionPolicyBinding
			machineVap    *admissionregistrationv1.ValidatingAdmissionPolicy
		)

		BeforeEach(func() {
			By("Loading the transport config maps")
			transportConfigMaps := admissiontestutils.LoadTransportConfigMaps()

			By("Applying the objects found in clusterAPICustomAdmissionPolicies")
			for _, obj := range transportConfigMaps[admissiontestutils.ClusterAPICustomAdmissionPolicies] {
				// Deep‑copy so the loop variable isn’t mutated by the client
				newObj, ok := obj.DeepCopyObject().(client.Object)
				Expect(ok).To(BeTrue())

				Eventually(func() error {
					err := k8sClient.Create(ctx, newObj)
					if err != nil && !apierrors.IsAlreadyExists(err) {
						return err
					}

					return nil
				}, timeout).Should(Succeed())
			}

			By("Creating the CAPI infra machine")
			Eventually(k8sClient.Create(ctx, capaMachine), timeout).Should(Succeed(), "capa machine should be able to be created")

			By("Creating the MAPI machine")
			mapiMachine = mapiMachineBuilder.WithName("test-machine").WithLabels(map[string]string{
				"machine.openshift.io/cluster-api-cluster":      "ci-op-gs2k97d6-c9e33-2smph",
				"machine.openshift.io/cluster-api-machine-role": "worker",
				"machine.openshift.io/cluster-api-machine-type": "worker",
				"machine.openshift.io/cluster-api-machineset":   "ci-op-gs2k97d6-c9e33-2smph-worker-us-west-2b",
				"machine.openshift.io/instance-type":            "m6a.xlarge",
			}).WithAnnotations(map[string]string{
				"machine.openshift.io/instance-state": "running",
			}).Build()
			Eventually(k8sClient.Create(ctx, mapiMachine), timeout).Should(Succeed())

			By("Creating the CAPI Machine")
			capiMachine = capiMachineBuilder.WithName("test-machine").WithLabels(map[string]string{
				"machine.openshift.io/cluster-api-cluster":      "ci-op-gs2k97d6-c9e33-2smph",
				"machine.openshift.io/cluster-api-machine-role": "worker",
				"machine.openshift.io/cluster-api-machine-type": "worker",
				"machine.openshift.io/cluster-api-machineset":   "ci-op-gs2k97d6-c9e33-2smph-worker-us-west-2b",
				"machine.openshift.io/instance-type":            "m6a.xlarge",
				"cluster.x-k8s.io/cluster-name":                 "ci-op-gs2k97d6-c9e33-2smph",
				"cluster.x-k8s.io/set-name":                     "ci-op-gs2k97d6-c9e33-2smph-worker-us-west-2b",
				"node-role.kubernetes.io/worker":                "",
			}).WithAnnotations(map[string]string{
				"machine.openshift.io/instance-state": "running",
			}).Build()
			Eventually(k8sClient.Create(ctx, capiMachine), timeout).Should(Succeed())

			Eventually(k.Get(capiMachine)).Should(Succeed())
			Eventually(k.Get(mapiMachine)).Should(Succeed())

		})

		AfterEach(func() {
			// Cleanup all VAPs and bindings
			testutils.CleanupResources(Default, ctx, cfg, k8sClient, "",
				&admissionregistrationv1.ValidatingAdmissionPolicy{},
				&admissionregistrationv1.ValidatingAdmissionPolicyBinding{},
			)
		})

		Context("machine api vap tests", func() {
			BeforeEach(func() {
				By("Waiting for VAP to be ready")
				machineVap = &admissionregistrationv1.ValidatingAdmissionPolicy{}
				Eventually(k8sClient.Get(ctx, client.ObjectKey{Name: "machine-api-machine-vap"}, machineVap), timeout).Should(Succeed())
				Eventually(k.Update(machineVap, func() {
					machineVap.Spec.Validations = append(machineVap.Spec.Validations, admissionregistrationv1.Validation{
						Expression: "!(variables.newLabels[?\"test-sentinel\"].orValue(\"\") == \"fubar\")",
						Message:    "policy in place",
					})
				})).Should(Succeed())

				Eventually(k.Object(machineVap), timeout).Should(
					HaveField("Status.ObservedGeneration", BeNumerically(">=", 2)),
				)

				By("Updating the VAP binding")
				policyBinding = &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
				Eventually(k8sClient.Get(ctx, client.ObjectKey{
					Name: "machine-api-machine-vap"}, policyBinding), timeout).Should(Succeed())

				Eventually(k.Update(policyBinding, func() {
					// We want to have our paramref reference the CAPI namespace,
					// since we `GenerateName` it is not static
					policyBinding.Spec.ParamRef.Namespace = capiNamespace.GetName()
					// We need to update the namespace in our namespaceSelector,
					// since also use `GenerateName` here
					policyBinding.Spec.MatchResources.NamespaceSelector.MatchLabels = map[string]string{
						"kubernetes.io/metadata.name": mapiNamespace.GetName(),
					}
				}), timeout).Should(Succeed())

				// Wait until the binding shows the patched values
				Eventually(k.Object(policyBinding), timeout).Should(
					SatisfyAll(
						HaveField("Spec.ParamRef.Namespace",
							Equal(capiNamespace.GetName())),

						HaveField("Spec.MatchResources.NamespaceSelector.MatchLabels",
							HaveKeyWithValue("kubernetes.io/metadata.name",
								mapiNamespace.GetName())),
					),
				)

				By("Creating a throwaway MAPI machine")
				testMachine := mapiMachineBuilder.WithGenerateName("test-machine").Build()
				Eventually(k8sClient.Create(ctx, testMachine), timeout).Should(Succeed())

				By("Setting the throwaway MAPI machine AuthoritativeAPI to Cluster API")
				Eventually(k.UpdateStatus(testMachine, func() {
					testMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
				})).Should(Succeed())

				Eventually(k.Object(testMachine), timeout).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))

				Eventually(k.Update(testMachine, func() {
					testMachine.ObjectMeta.Labels = map[string]string{"test-sentinel": "fubar"}
				}), timeout).Should(MatchError(ContainSubstring("policy in place")))
			})
			Context("with status.AuthoritativeAPI: Machine API", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine AuthoritativeAPI to Machine API")
					Eventually(k.UpdateStatus(mapiMachine, func() {
						mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
					})).Should(Succeed())

					Eventually(k.Object(mapiMachine), timeout).Should(
						HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)))
				})

				It("updating the spec should be allowed", func() {
					Eventually(k.Update(mapiMachine, func() {
						mapiMachine.Spec.ObjectMeta.Labels = map[string]string{"foo": "bar"}
					}), timeout).Should(Succeed(), "expected success when updating the spec")
				})

			})

			Context("with status.AuthoritativeAPI: ClusterAPI", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine AuthoritativeAPI to Cluster API")
					Eventually(k.UpdateStatus(mapiMachine, func() {
						mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
					})).Should(Succeed())

					Eventually(k.Object(mapiMachine), timeout).Should(
						HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))
				})

				It("updating the spec (outside of authoritative api) should be prevented", func() {
					Eventually(k.Update(mapiMachine, func() {
						mapiMachine.Spec.ObjectMeta.Labels = map[string]string{"foo": "bar"}
					}), timeout).Should(MatchError(ContainSubstring("You may only modify spec.authoritativeAPI")))
				})

				It("updating the spec.authoritativeAPI should be allowed", func() {
					Eventually(k.Update(mapiMachine, func() {
						mapiMachine.Spec.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
					}), timeout).Should(Succeed(), "expected success when updating spec.authoritativeAPI")
				})

				Context("when trying to update metadata.labels", func() {
					It("rejects modification of the protected machine.openshift.io label", func() {
						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Labels["machine.openshift.io/instance-type"] = "m5.large"
						}), timeout).Should(MatchError(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* or kubernetes.io/* label")))
					})

					It("rejects deletion of the protected machine.openshift.io label", func() {
						Eventually(k.Update(mapiMachine, func() {
							delete(mapiMachine.Labels, "machine.openshift.io/instance-type")
						}), timeout).Should(MatchError(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* or kubernetes.io/* label")))
					})

					It("rejects setting of the protected machine.openshift.io label to the empty string ''", func() {
						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Labels["machine.openshift.io/instance-type"] = ""
						}), timeout).Should(MatchError(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* or kubernetes.io/* label")))
					})

					It("rejects adding a new machine.openshift.io label", func() {
						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Labels["machine.openshift.io/foo"] = "bar"
						}), timeout).Should(MatchError(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* or kubernetes.io/* label")))
					})

					It("rejects adding a new machine.openshift.io label with an empty string value", func() {
						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Labels["machine.openshift.io/foo"] = ""
						}), timeout).Should(MatchError(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* or kubernetes.io/* label")))
					})

					It("allows modification of a non-protected label", func() {
						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Labels["test"] = "val"
						}), timeout).Should(Succeed(), "expected success when modifying unrelated labels")
					})
				})

				Context("when trying to update metadata.Annotations", func() {
					It("rejects modification of a protected machine.openshift.io annotation", func() {
						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Annotations["machine.openshift.io/instance-state"] = "stopped"
						}), timeout).Should(MatchError(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* annotation")))
					})

					It("rejects deletion of a protected machine.openshift.io annotation", func() {
						Eventually(k.Update(mapiMachine, func() {
							delete(mapiMachine.Annotations, "machine.openshift.io/instance-state")
						}), timeout).Should(MatchError(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* annotation")))
					})

					It("rejects modification of a protected machine.openshift.io annotation to the empty string ''", func() {
						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Annotations["machine.openshift.io/instance-state"] = ""
						}), timeout).Should(MatchError(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* annotation")))
					})

					It("rejects adding a new protected machine.openshift.io annotation", func() {
						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Annotations["machine.openshift.io/foo"] = "bar"
						}), timeout).Should(MatchError(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* annotation")))
					})

					It("rejects adding a new protected machine.openshift.io annotation with an empty string value", func() {
						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Annotations["machine.openshift.io/foo"] = ""
						}), timeout).Should(MatchError(ContainSubstring("Cannot add, modify or delete any machine.openshift.io/* annotation")))
					})

					It("allows modification of a non-protected annotation", func() {
						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Annotations["bar"] = "baz"
						}), timeout).Should(Succeed(), "expected success when modifying unrelated annotations")
					})
				})

				Context("when trying to update Cluster API owned metadata.labels", func() {
					It("allows changing a metadata label to match the param machine", func() {
						Eventually(k.Object(capiMachine), timeout).Should(
							HaveField("Labels", HaveKeyWithValue("cluster.x-k8s.io/cluster-name", "ci-op-gs2k97d6-c9e33-2smph")))

						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Labels["cluster.x-k8s.io/cluster-name"] = "ci-op-gs2k97d6-c9e33-2smph"
						}), timeout).Should(Succeed(), "expected success when updating label to match CAPI machine")
					})

					It("rejects changing a label to differ from the param machine", func() {
						Eventually(k.Update(mapiMachine, func() {
							mapiMachine.Labels["cluster.x-k8s.io/cluster-name"] = "foo"
						}), timeout).Should(MatchError(ContainSubstring("Cannot modify a Cluster API controlled label except to match the Cluster API mirrored machine")))
					})
				})

				It("rejects updating spec.authoritativeAPI alongside other spec fields", func() {
					Eventually(k.Update(mapiMachine, func() {
						mapiMachine.Spec.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
						mapiMachine.Spec.ObjectMeta.Labels = map[string]string{"foo": "bar"}
					}), timeout).Should(MatchError(ContainSubstring("You may only modify spec.authoritativeAPI")))

				})

			})
		})

		Context("Prevent setting of CAPI fields that are not supported by MAPI", func() {
			BeforeEach(func() {
				By("Waiting for VAP to be ready")
				machineVap = &admissionregistrationv1.ValidatingAdmissionPolicy{}
				Eventually(k8sClient.Get(ctx, client.ObjectKey{Name: "openshift-cluster-api-prevent-setting-of-capi-fields-unsupported-by-mapi"}, machineVap), timeout).Should(Succeed())
				Eventually(k.Update(machineVap, func() {
					machineVap.Spec.Validations = append(machineVap.Spec.Validations, admissionregistrationv1.Validation{
						Expression: "!(has(object.metadata.labels) && \"test-sentinel\" in object.metadata.labels)",
						Message:    "policy in place",
					})
				})).Should(Succeed())

				Eventually(k.Object(machineVap), timeout).Should(
					HaveField("Status.ObservedGeneration", BeNumerically(">=", 2)),
				)

				By("Updating the VAP binding")
				policyBinding = &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
				Eventually(k8sClient.Get(ctx, client.ObjectKey{
					Name: "openshift-cluster-api-prevent-setting-of-capi-fields-unsupported-by-mapi"}, policyBinding), timeout).Should(Succeed())

				Eventually(k.Update(policyBinding, func() {
					// We need to update the namespace in our namespaceSelector,
					// since also use `GenerateName` here
					policyBinding.Spec.MatchResources.NamespaceSelector.MatchLabels = map[string]string{
						"kubernetes.io/metadata.name": capiNamespace.GetName(),
					}
				}), timeout).Should(Succeed())

				// Wait until the binding shows the patched values
				Eventually(k.Object(policyBinding), timeout).Should(
					SatisfyAll(
						HaveField("Spec.MatchResources.NamespaceSelector.MatchLabels",
							HaveKeyWithValue("kubernetes.io/metadata.name",
								capiNamespace.GetName())),
					),
				)

				checkVAPMachine := clusterv1resourcebuilder.Machine().WithName("vap-checking-machine").WithNamespace(capiNamespace.Name).Build()
				Eventually(k8sClient.Create(ctx, checkVAPMachine)).Should(Succeed(), "check vap machine should be able to be created")

				// Continually try to update the capiMachine to a forbidden field until the VAP blocks it
				Eventually(k.Update(checkVAPMachine, func() {
					checkVAPMachine.ObjectMeta.Labels = map[string]string{"test-sentinel": "fubar"}
				}), timeout).Should(MatchError(ContainSubstring("policy in place")))
			})

			It("updating the spec.Version should not be allowed", func() {
				Eventually(k.Update(capiMachine, func() {
					testVersion := "1"
					capiMachine.Spec.Version = &testVersion
				}), timeout).Should(MatchError(ContainSubstring(".version is a forbidden field")))
			})

			It("updating the spec.readinessGates on machines should not be allowed", func() {
				Eventually(k.Update(capiMachine, func() {
					capiMachine.Spec.ReadinessGates = []clusterv1.MachineReadinessGate{{ConditionType: "foo"}}
				}), timeout).Should(MatchError(ContainSubstring(".readinessGates is a forbidden field")))
			})
		})

		Context("Prevent creation of MAPI machine if authoritative API is not CAPI", func() {
			BeforeEach(func() {
				By("Waiting for VAP to be ready")
				machineVap = &admissionregistrationv1.ValidatingAdmissionPolicy{}
				Eventually(k8sClient.Get(ctx, client.ObjectKey{Name: "openshift-only-create-mapi-machine-if-authoritative-api-capi"}, machineVap), timeout).Should(Succeed())
				resourceRules := machineVap.Spec.MatchConstraints.ResourceRules
				Expect(resourceRules).To(HaveLen(1))
				resourceRules[0].Operations = append(resourceRules[0].Operations, admissionregistrationv1.Update)
				Eventually(k.Update(machineVap, func() {
					machineVap.Spec.Validations = append(machineVap.Spec.Validations, admissionregistrationv1.Validation{
						Expression: "!(has(object.metadata.labels) && \"test-sentinel\" in object.metadata.labels)",
						Message:    "policy in place",
					})
					// Updating the VAP so that it functions on "UPDATE" as well as "CREATE" only in this test suite to make it easier to test the functionality
					machineVap.Spec.MatchConstraints.ResourceRules = resourceRules

				})).Should(Succeed())

				Eventually(k.Object(machineVap), timeout).Should(
					HaveField("Status.ObservedGeneration", BeNumerically(">=", 2)),
				)

				By("Updating the VAP binding")
				policyBinding = &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
				Eventually(k8sClient.Get(ctx, client.ObjectKey{
					Name: "openshift-only-create-mapi-machine-if-authoritative-api-capi"}, policyBinding), timeout).Should(Succeed())

				Eventually(k.Update(policyBinding, func() {
					// We want to have our paramref reference the CAPI namespace,
					// since we `GenerateName` it is not static
					policyBinding.Spec.ParamRef.Namespace = capiNamespace.GetName()
					// We need to update the namespace in our namespaceSelector,
					// since also use `GenerateName` here
					policyBinding.Spec.MatchResources.NamespaceSelector.MatchLabels = map[string]string{
						"kubernetes.io/metadata.name": mapiNamespace.GetName(),
					}
				}), timeout).Should(Succeed())

				// Wait until the binding shows the patched values
				Eventually(k.Object(policyBinding), timeout).Should(
					SatisfyAll(
						HaveField("Spec.MatchResources.NamespaceSelector.MatchLabels",
							HaveKeyWithValue("kubernetes.io/metadata.name",
								mapiNamespace.GetName())),
					),
				)

				By("Creating a throwaway MAPI machine")
				sentinelMachine := mapiMachineBuilder.WithName("sentinel-machine").WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).Build()
				Eventually(k8sClient.Create(ctx, sentinelMachine), timeout).Should(Succeed())

				capiSentinelMachine := clusterv1resourcebuilder.Machine().WithName("sentinel-machine").WithNamespace(capiNamespace.Name).Build()
				Expect(k8sClient.Create(ctx, capiSentinelMachine)).To(Succeed())

				Eventually(k.Get(capiSentinelMachine)).Should(Succeed())

				Eventually(k.Update(sentinelMachine, func() {
					sentinelMachine.ObjectMeta.Labels = map[string]string{"test-sentinel": "fubar"}
				}), timeout).Should(MatchError(ContainSubstring("policy in place")))
			})

			// The Authoritative API defaults to MachineAPI so we can't test if it's unset.
			It("Doesn't allow creation of a MAPI machine with authoritative API MachineAPI and the same name", func() {
				By("Create the Capi Machine")
				newCapiMachine := clusterv1resourcebuilder.Machine().WithName("validation-machine").WithNamespace(capiNamespace.Name).Build()
				Expect(k8sClient.Create(ctx, newCapiMachine)).To(Succeed())

				By("Create the Mapi Machine")
				newMapiMachine := mapiMachineBuilder.WithName("validation-machine").WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).Build()
				Eventually(k8sClient.Create(ctx, newMapiMachine), timeout).Should(MatchError(ContainSubstring("with authoritativeAPI=MachineAPI because a Cluster API Machine with the same name already exists.")))
			})

			It("Does allow creation of a MAPI machine with authoritative API Cluster and the same name", func() {
				By("Create the Capi Machine")
				newCapiMachine := clusterv1resourcebuilder.Machine().WithName("validation-machine").WithNamespace(capiNamespace.Name).Build()
				Expect(k8sClient.Create(ctx, newCapiMachine)).To(Succeed())

				By("Create the Mapi Machine")
				newMapiMachine := mapiMachineBuilder.WithName("validation-machine").WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).Build()
				Eventually(k8sClient.Create(ctx, newMapiMachine), timeout).Should(Succeed())
			})

		})

		Context("Prevent updates to MAPI machine if migrating would be unpredictable", func() {
			BeforeEach(func() {
				By("Waiting for VAP to be ready")
				machineVap = &admissionregistrationv1.ValidatingAdmissionPolicy{}
				Eventually(k8sClient.Get(ctx, client.ObjectKey{Name: "openshift-prevent-migration-when-machine-updating"}, machineVap), timeout).Should(Succeed())
				Eventually(k.Update(machineVap, func() {
					machineVap.Spec.Validations = append(machineVap.Spec.Validations, admissionregistrationv1.Validation{
						Expression: "!(has(object.metadata.labels) && \"test-sentinel\" in object.metadata.labels)",
						Message:    "policy in place",
					})
				})).Should(Succeed())

				Eventually(k.Object(machineVap), timeout).Should(
					HaveField("Status.ObservedGeneration", BeNumerically(">=", 2)),
				)

				By("Updating the VAP binding")
				policyBinding = &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
				Eventually(k8sClient.Get(ctx, client.ObjectKey{
					Name: "openshift-prevent-migration-when-machine-updating"}, policyBinding), timeout).Should(Succeed())

				Eventually(k.Update(policyBinding, func() {
					policyBinding.Spec.MatchResources.NamespaceSelector.MatchLabels = map[string]string{
						"kubernetes.io/metadata.name": mapiNamespace.GetName(),
					}
				}), timeout).Should(Succeed())

				// Wait until the binding shows the patched values
				Eventually(k.Object(policyBinding), timeout).Should(
					SatisfyAll(
						HaveField("Spec.MatchResources.NamespaceSelector.MatchLabels",
							HaveKeyWithValue("kubernetes.io/metadata.name",
								mapiNamespace.GetName())),
					),
				)

				By("Creating a throwaway MAPI machine")
				sentinelMachine := mapiMachineBuilder.WithName("sentinel-machine").WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).Build()
				Eventually(k8sClient.Create(ctx, sentinelMachine), timeout).Should(Succeed())

				capiSentinelMachine := clusterv1resourcebuilder.Machine().WithName("sentinel-machine").WithNamespace(capiNamespace.Name).Build()
				Expect(k8sClient.Create(ctx, capiSentinelMachine)).To(Succeed())

				Eventually(k.Get(capiSentinelMachine)).Should(Succeed())

				Eventually(k.Update(sentinelMachine, func() {
					sentinelMachine.ObjectMeta.Labels = map[string]string{"test-sentinel": "fubar"}
				}), timeout).Should(MatchError(ContainSubstring("policy in place")))
			})

			It("denies updating the AuthoritativeAPI when the machine is in Provisioning", func() {
				By("Updating the MAPI machine phase to be provisioning")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					provisioningPhase := mapiv1beta1.PhaseProvisioning
					mapiMachine.Status.Phase = &provisioningPhase
				})).Should(Succeed())

				By("Attempting to update the authoritativeAPI should be blocked")
				Eventually(k.Update(mapiMachine, func() {
					mapiMachine.Spec.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
				}), timeout).Should(MatchError(ContainSubstring("Cannot update .spec.authoritativeAPI when machine is in Provisioning phase")))
			})

			It("denies updating the AuthoritativeAPI when the machine has a non-zero deletion timestamp", func() {
				By("Adding a finalizer to prevent actual deletion")
				Eventually(k.Update(mapiMachine, func() {
					mapiMachine.Finalizers = append(mapiMachine.Finalizers, "test-finalizer")
				})).Should(Succeed())

				By("Deleting the MAPI machine to set deletion timestamp")
				Eventually(k8sClient.Delete(ctx, mapiMachine)).Should(Succeed())

				By("Waiting for deletion timestamp to be set")
				Eventually(k.Object(mapiMachine)).Should(SatisfyAll(
					HaveField("DeletionTimestamp", Not(BeNil())),
				))

				By("Attempting to update the authoritativeAPI should be blocked")
				Eventually(k.Update(mapiMachine, func() {
					mapiMachine.Spec.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
				}), timeout).Should(MatchError(ContainSubstring("Cannot update .spec.authoritativeAPI when machine has a non-zero deletion timestamp")))
			})

			It("allows updating the AuthoritativeAPI when the machine is in Running phase", func() {
				By("Updating the MAPI machine phase to be running")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					runningPhase := mapiv1beta1.PhaseRunning
					mapiMachine.Status.Phase = &runningPhase
				})).Should(Succeed())

				By("Attempting to update the authoritativeAPI should succeed")
				Eventually(k.Update(mapiMachine, func() {
					mapiMachine.Spec.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
				}), timeout).Should(Succeed())
			})

			It("allows updating labels when the machine is in Provisioning phase but not changing AuthoritativeAPI", func() {
				By("Updating the MAPI machine phase to be provisioning")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					provisioningPhase := mapiv1beta1.PhaseProvisioning
					mapiMachine.Status.Phase = &provisioningPhase
				})).Should(Succeed())

				By("Attempting to update labels should succeed")
				Eventually(k.Update(mapiMachine, func() {
					mapiMachine.ObjectMeta.Labels = map[string]string{"test-label": "fubar"}
				}), timeout).Should(Succeed())
			})

		})

		FContext("Updates to MAPI machine warns user if the Synchronized condition is set to false", func() {
			BeforeEach(func() {
				By("Waiting for VAP to be ready")
				machineVap = &admissionregistrationv1.ValidatingAdmissionPolicy{}
				Eventually(k8sClient.Get(ctx, client.ObjectKey{Name: "openshift-provide-warning-when-not-synchronized"}, machineVap), timeout).Should(Succeed())
				Eventually(k.Update(machineVap, func() {
					machineVap.Spec.Validations = append(machineVap.Spec.Validations, admissionregistrationv1.Validation{
						Expression: "!(has(object.metadata.labels) && \"test-sentinel\" in object.metadata.labels)",
						Message:    "policy in place",
					})
				})).Should(Succeed())

				Eventually(k.Object(machineVap), timeout).Should(
					HaveField("Status.ObservedGeneration", BeNumerically(">=", 2)),
				)

				By("Updating the VAP binding")
				policyBinding = &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
				Eventually(k8sClient.Get(ctx, client.ObjectKey{
					Name: "openshift-provide-warning-when-not-synchronized"}, policyBinding), timeout).Should(Succeed())

				Eventually(k.Update(policyBinding, func() {
					// We need to update the namespace in our namespaceSelector,
					// since also use `GenerateName` here
					policyBinding.Spec.MatchResources.NamespaceSelector.MatchLabels = map[string]string{
						"kubernetes.io/metadata.name": mapiNamespace.GetName(),
					}
				}), timeout).Should(Succeed())

				// Wait until the binding shows the patched values
				Eventually(k.Object(policyBinding), timeout).Should(
					SatisfyAll(
						HaveField("Spec.MatchResources.NamespaceSelector.MatchLabels",
							HaveKeyWithValue("kubernetes.io/metadata.name",
								mapiNamespace.GetName())),
					),
				)

				By("Creating a throwaway MAPI machine")
				sentinelMachine := mapiMachineBuilder.WithName("sentinel-machine").WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).Build()
				Eventually(k8sClient.Create(ctx, sentinelMachine), timeout).Should(Succeed())

				capiSentinelMachine := clusterv1resourcebuilder.Machine().WithName("sentinel-machine").WithNamespace(capiNamespace.Name).Build()
				Expect(k8sClient.Create(ctx, capiSentinelMachine)).To(Succeed())

				Eventually(k.Get(capiSentinelMachine)).Should(Succeed())

				Eventually(k.Update(sentinelMachine, func() {
					sentinelMachine.ObjectMeta.Labels = map[string]string{"test-sentinel": "fubar"}
				}), timeout).Should(Succeed())
			})

			It("warns the user when the machine is still synchronzing", func() {
				By("Setting the Synchronized condition to False")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.Conditions = []mapiv1beta1.Condition{
						{
							Type:               consts.SynchronizedCondition,
							Status:             corev1.ConditionFalse,
							Reason:             "ErrorReason",
							Message:            "Error message",
							LastTransitionTime: metav1.Now(),
						},
					}
				})).Should(Succeed())

				By("Attempting to update the authoritativeAPI should be blocked")
				Eventually(k.Update(mapiMachine, func() {
					mapiMachine.Spec.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
				}), timeout).Should(Succeed()) // This should succeed but show a warning?
			})

		})

	})
})

var _ = Describe("applySynchronizedConditionWithPatch", func() {
	var mapiNamespace *corev1.Namespace
	var reconciler *MachineSyncReconciler
	var mapiMachine *mapiv1beta1.Machine
	var k komega.Komega

	BeforeEach(func() {
		k = komega.New(k8sClient)

		By("Setting up a namespace for the test")
		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Eventually(k8sClient.Create(ctx, mapiNamespace)).Should(Succeed(), "mapi namespace should be able to be created")

		By("Setting up the reconciler")
		reconciler = &MachineSyncReconciler{
			Client: k8sClient,
		}

		By("Create the MAPI Machine")
		mapiMachineBuilder := machinev1resourcebuilder.Machine().
			WithName("test-machine").
			WithNamespace(mapiNamespace.Name)

		mapiMachine = mapiMachineBuilder.Build()
		mapiMachine.Spec.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
		Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed(), "mapi machine should be able to be created")

		By("Set the initial status of the MAPI Machine")
		Eventually(k.UpdateStatus(mapiMachine, func() {
			mapiMachine.Status.SynchronizedGeneration = int64(22)
			mapiMachine.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
		})).Should(Succeed())

		By("Get the MAPI Machine from the API Server")
		mapiMachine = mapiMachineBuilder.Build()
		Eventually(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachine), mapiMachine)).Should(Succeed(), "mapi machine should be able to be fetched")

		// Artificially set the Generation to a made up number
		// as that can't be written directly to the API Server as it is read-only.
		mapiMachine.Generation = int64(23)
	})

	AfterEach(func() {
		By("Cleaning up MAPI test resources")
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, mapiNamespace.GetName(),
			&mapiv1beta1.Machine{},
			&mapiv1beta1.MachineSet{},
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
						HaveField("Severity", Equal(mapiv1beta1.ConditionSeverityError)),
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
						HaveField("Severity", Equal(mapiv1beta1.ConditionSeverityInfo)),
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
						HaveField("Severity", Equal(mapiv1beta1.ConditionSeverityNone)),
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
func awsProviderSpecFromMachine(mapiMachine *mapiv1beta1.Machine) (mapiv1beta1.AWSMachineProviderConfig, error) {
	if mapiMachine == nil {
		return mapiv1beta1.AWSMachineProviderConfig{}, nil
	}

	return mapi2capi.AWSProviderSpecFromRawExtension(mapiMachine.Spec.ProviderSpec.Value)
}

var _ = Describe("Unsupported AWS fields validating admission policy", Ordered, func() {
	var (
		namespace *corev1.Namespace
		k         komega.Komega
	)

	expectVAPError := func(err error, msg string) {
		var statusErr *apierrors.StatusError
		ExpectWithOffset(1, errors.As(err, &statusErr)).To(BeTrue())
		ExpectWithOffset(1, statusErr.Status().Code).To(Equal(int32(http.StatusUnprocessableEntity)))
		ExpectWithOffset(1, statusErr.Error()).To(ContainSubstring(msg))
	}

	BeforeAll(func() {
		k = komega.New(k8sClient)

		By("Loading the transport config maps")
		transportConfigMaps := admissiontestutils.LoadTransportConfigMaps()

		By("creating a namespace for the test")
		namespace = corev1resourcebuilder.Namespace().WithGenerateName("unsupported-aws-fields-").Build()
		Eventually(k8sClient.Create(ctx, namespace)).Should(Succeed(), "namespace should be able to be created")

		By("Applying the objects found in clusterAPIAWSAdmissionPolicies for the test namespace")
		for _, obj := range transportConfigMaps[admissiontestutils.ClusterAPIAWSAdmissionPolicies] {
			newObj, ok := obj.DeepCopyObject().(client.Object)
			Expect(ok).To(BeTrue())

			// Update the "openshift-cluster-api" namespace to the test namespace
			if binding, ok := newObj.(*admissionregistrationv1.ValidatingAdmissionPolicyBinding); ok {
				if binding.Spec.MatchResources != nil && binding.Spec.MatchResources.NamespaceSelector != nil {
					for i, expr := range binding.Spec.MatchResources.NamespaceSelector.MatchExpressions {
						if expr.Key == "kubernetes.io/metadata.name" && expr.Values[i] == capiNamespace {
							binding.Spec.MatchResources.NamespaceSelector.MatchExpressions[i].Values = []string{namespace.Name}
						}
					}
				}
			}

			Expect(k8sClient.Create(ctx, newObj)).To(Succeed())
		}

		checkVAPMachine := awsv1resourcebuilder.AWSMachine().WithName("check-vap-machine").WithNamespace(namespace.Name).Build()
		Eventually(k8sClient.Create(ctx, checkVAPMachine)).Should(Succeed(), "check vap machine should be able to be created")

		// Continually try to update the AWSMachine to a forbidden field until the VAP blocks it
		Eventually(k.Update(checkVAPMachine, func() {
			checkVAPMachine.Spec.ImageLookupFormat = "forbidden-format"
		})).Should(MatchError(ContainSubstring("spec.imageLookupFormat is a forbidden field")))
	})

	AfterAll(func() {
		// Cleanup all VAPs
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, "",
			&admissionregistrationv1.ValidatingAdmissionPolicy{},
			&admissionregistrationv1.ValidatingAdmissionPolicyBinding{},
		)

		By("deleting the namespace")
		Eventually(k8sClient.Delete(ctx, namespace)).Should(Succeed(), "namespace should be able to be deleted")

	})

	Context("AWSMachine validation", func() {
		const (
			testImageLookupFormat = "ami-format"
			testImageLookupOrg    = "123456789012"
			testImageLookupBaseOS = "linux"
			testSecretPrefix      = "my-secret"
			testSecurityGroupID   = "sg-123"
			testNetworkInterface  = "eni-12345678"
			testVaultBackend      = "vault"
		)

		type testCase struct {
			modifier      func(*awsv1.AWSMachine)
			expectedError string
		}

		DescribeTable("should validate AWSMachine creation",
			func(tc testCase) {
				awsMachine := awsv1resourcebuilder.AWSMachine().WithGenerateName("test-aws-machine").WithNamespace(namespace.Name).Build()

				if tc.modifier != nil {
					tc.modifier(awsMachine)
				}

				err := k8sClient.Create(ctx, awsMachine)

				if tc.expectedError != "" {
					Expect(err).To(HaveOccurred())
					expectVAPError(err, tc.expectedError)
				} else {
					Expect(err).ToNot(HaveOccurred())
				}
			},
			Entry("without forbidden fields", testCase{
				modifier:      nil,
				expectedError: "",
			}),
			Entry("with a forbidden field (ami.eksLookupType)", testCase{
				modifier: func(m *awsv1.AWSMachine) {
					m.Spec.AMI.EKSOptimizedLookupType = ptr.To(awsv1.AmazonLinux)
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.ami.eksLookupType is a forbidden field",
			}),
			Entry("with a forbidden field (imageLookupFormat)", testCase{
				modifier: func(m *awsv1.AWSMachine) {
					m.Spec.ImageLookupFormat = testImageLookupFormat
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.imageLookupFormat is a forbidden field",
			}),
			Entry("with a forbidden field (imageLookupOrg)", testCase{
				modifier: func(m *awsv1.AWSMachine) {
					m.Spec.ImageLookupOrg = testImageLookupOrg
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.imageLookupOrg is a forbidden field",
			}),
			Entry("with a forbidden field (imageLookupBaseOS)", testCase{
				modifier: func(m *awsv1.AWSMachine) {
					m.Spec.ImageLookupBaseOS = testImageLookupBaseOS
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.imageLookupBaseOS is a forbidden field",
			}),
			Entry("with a forbidden field (networkInterfaces)", testCase{
				modifier: func(m *awsv1.AWSMachine) {
					m.Spec.NetworkInterfaces = []string{testNetworkInterface}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.networkInterfaces is a forbidden field",
			}),
			Entry("with a forbidden field (uncompressedUserData)", testCase{
				modifier: func(m *awsv1.AWSMachine) {
					m.Spec.UncompressedUserData = ptr.To(true)
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.uncompressedUserData is a forbidden field",
			}),
			Entry("with a forbidden field (cloudInit)", testCase{
				modifier: func(m *awsv1.AWSMachine) {
					m.Spec.CloudInit = awsv1.CloudInit{SecretCount: 1}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.cloudInit is a forbidden field",
			}),
			Entry("with a forbidden field (privateDNSName)", testCase{
				modifier: func(m *awsv1.AWSMachine) {
					m.Spec.PrivateDNSName = &awsv1.PrivateDNSName{}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.privateDnsName is a forbidden field",
			}),
			Entry("with a forbidden field (ignition.proxy)", testCase{
				modifier: func(m *awsv1.AWSMachine) {
					m.Spec.Ignition = &awsv1.Ignition{Proxy: &awsv1.IgnitionProxy{}}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.ignition.proxy is a forbidden field",
			}),
			Entry("with a forbidden field (ignition.tls)", testCase{
				modifier: func(m *awsv1.AWSMachine) {
					m.Spec.Ignition = &awsv1.Ignition{TLS: &awsv1.IgnitionTLS{}}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.ignition.tls is a forbidden field",
			}),
			Entry("with a forbidden field (securityGroupOverrides)", testCase{
				modifier: func(m *awsv1.AWSMachine) {
					m.Spec.SecurityGroupOverrides = map[awsv1.SecurityGroupRole]string{"bastion": testSecurityGroupID}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.securityGroupOverrides is a forbidden field",
			}),
		)

		It("should block updates that add multiple forbidden fields to AWSMachine", func() {
			awsMachine := awsv1resourcebuilder.AWSMachine().WithGenerateName("test-aws-machine").WithNamespace(namespace.Name).Build()
			Expect(k8sClient.Create(ctx, awsMachine)).To(Succeed())

			// Add multiple forbidden fields in one update
			awsMachine.Spec.ImageLookupFormat = testImageLookupFormat
			awsMachine.Spec.NetworkInterfaces = []string{testNetworkInterface}
			awsMachine.Spec.UncompressedUserData = ptr.To(true)

			err := k8sClient.Update(ctx, awsMachine)
			Expect(err).To(HaveOccurred())
			// Should catch the first forbidden field (validation stops at first error)
			expectVAPError(err, "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.imageLookupFormat is a forbidden field")
		})

		It("should not enforce the VAP on other namespaces", func() {
			otherNamespace := corev1resourcebuilder.Namespace().WithGenerateName("other-namespace").Build()
			Expect(k8sClient.Create(ctx, otherNamespace)).To(Succeed())

			awsMachine := awsv1resourcebuilder.AWSMachine().WithGenerateName("test-aws-machine").WithNamespace(otherNamespace.Name).Build()
			awsMachine.Spec.ImageLookupFormat = testImageLookupFormat
			err := k8sClient.Create(ctx, awsMachine)
			Expect(err).ToNot(HaveOccurred())
		})

	})

	Context("AWSMachineTemplate validation", func() {
		const (
			testImageLookupFormat = "ami-format"
			testImageLookupOrg    = "123456789012"
			testImageLookupBaseOS = "linux"
			testSecretPrefix      = "my-secret"
			testSecurityGroupID   = "sg-123"
			testNetworkInterface  = "eni-12345678"
			testVaultBackend      = "vault"
		)

		type testCase struct {
			modifier      func(*awsv1.AWSMachineTemplate)
			expectedError string
		}

		DescribeTable("should validate AWSMachineTemplate creation",
			func(tc testCase) {
				awsMachineTemplate := awsv1resourcebuilder.AWSMachineTemplate().WithGenerateName("test-aws-machine-template").WithNamespace(namespace.Name).Build()

				if tc.modifier != nil {
					tc.modifier(awsMachineTemplate)
				}

				err := k8sClient.Create(ctx, awsMachineTemplate)

				if tc.expectedError != "" {
					Expect(err).To(HaveOccurred())
					expectVAPError(err, tc.expectedError)
				} else {
					Expect(err).ToNot(HaveOccurred())
				}
			},
			Entry("without forbidden fields", testCase{
				modifier:      nil,
				expectedError: "",
			}),
			Entry("with a forbidden field (ami.eksLookupType)", testCase{
				modifier: func(mt *awsv1.AWSMachineTemplate) {
					mt.Spec.Template.Spec.AMI.EKSOptimizedLookupType = ptr.To(awsv1.AmazonLinux)
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.ami.eksLookupType is a forbidden field",
			}),
			Entry("with a forbidden field (imageLookupFormat)", testCase{
				modifier: func(mt *awsv1.AWSMachineTemplate) {
					mt.Spec.Template.Spec.ImageLookupFormat = testImageLookupFormat
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.imageLookupFormat is a forbidden field",
			}),
			Entry("with a forbidden field (imageLookupOrg)", testCase{
				modifier: func(mt *awsv1.AWSMachineTemplate) {
					mt.Spec.Template.Spec.ImageLookupOrg = testImageLookupOrg
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.imageLookupOrg is a forbidden field",
			}),
			Entry("with a forbidden field (imageLookupBaseOS)", testCase{
				modifier: func(mt *awsv1.AWSMachineTemplate) {
					mt.Spec.Template.Spec.ImageLookupBaseOS = testImageLookupBaseOS
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.imageLookupBaseOS is a forbidden field",
			}),
			Entry("with a forbidden field (networkInterfaces)", testCase{
				modifier: func(mt *awsv1.AWSMachineTemplate) {
					mt.Spec.Template.Spec.NetworkInterfaces = []string{testNetworkInterface}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.networkInterfaces is a forbidden field",
			}),
			Entry("with a forbidden field (uncompressedUserData)", testCase{
				modifier: func(mt *awsv1.AWSMachineTemplate) {
					mt.Spec.Template.Spec.UncompressedUserData = ptr.To(true)
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.uncompressedUserData is a forbidden field",
			}),
			Entry("with a forbidden field (cloudInit)", testCase{
				modifier: func(mt *awsv1.AWSMachineTemplate) {
					mt.Spec.Template.Spec.CloudInit = awsv1.CloudInit{SecretCount: 1}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.cloudInit is a forbidden field",
			}),
			Entry("with a forbidden field (privateDNSName)", testCase{
				modifier: func(mt *awsv1.AWSMachineTemplate) {
					mt.Spec.Template.Spec.PrivateDNSName = &awsv1.PrivateDNSName{}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.privateDnsName is a forbidden field",
			}),
			Entry("with a forbidden field (ignition.proxy)", testCase{
				modifier: func(mt *awsv1.AWSMachineTemplate) {
					mt.Spec.Template.Spec.Ignition = &awsv1.Ignition{Proxy: &awsv1.IgnitionProxy{}}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.ignition.proxy is a forbidden field",
			}),
			Entry("with a forbidden field (ignition.tls)", testCase{
				modifier: func(mt *awsv1.AWSMachineTemplate) {
					mt.Spec.Template.Spec.Ignition = &awsv1.Ignition{TLS: &awsv1.IgnitionTLS{}}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.ignition.tls is a forbidden field",
			}),
			Entry("with a forbidden field (securityGroupOverrides)", testCase{
				modifier: func(mt *awsv1.AWSMachineTemplate) {
					mt.Spec.Template.Spec.SecurityGroupOverrides = map[awsv1.SecurityGroupRole]string{"bastion": testSecurityGroupID}
				},
				expectedError: "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.securityGroupOverrides is a forbidden field",
			}),
		)

		It("should not enforce the VAP on other namespaces", func() {
			otherNamespace := corev1resourcebuilder.Namespace().WithGenerateName("other-namespace").Build()
			Eventually(k8sClient.Create(ctx, otherNamespace)).Should(Succeed(), "other namespace should be able to be created")

			awsMachineTemplate := awsv1resourcebuilder.AWSMachineTemplate().WithGenerateName("test-aws-machine-template").WithNamespace(otherNamespace.Name).Build()
			awsMachineTemplate.Spec.Template.Spec.ImageLookupBaseOS = testImageLookupBaseOS
			Eventually(k8sClient.Create(ctx, awsMachineTemplate)).Should(Succeed(), "aws machine template should be able to be created")
		})

		It("should block updates that add multiple forbidden fields", func() {
			awsMachineTemplate := awsv1resourcebuilder.AWSMachineTemplate().WithGenerateName("test-aws-machine-template").WithNamespace(namespace.Name).Build()
			Eventually(k8sClient.Create(ctx, awsMachineTemplate)).Should(Succeed(), "aws machine template should be able to be created")

			// Add multiple forbidden fields in one update
			awsMachineTemplate.Spec.Template.Spec.ImageLookupFormat = testImageLookupFormat
			awsMachineTemplate.Spec.Template.Spec.NetworkInterfaces = []string{testNetworkInterface}
			awsMachineTemplate.Spec.Template.Spec.UncompressedUserData = ptr.To(true)

			err := k8sClient.Update(ctx, awsMachineTemplate)
			Expect(err).To(HaveOccurred())
			// Should catch the first forbidden field (validation stops at first error)
			expectVAPError(err, "ValidatingAdmissionPolicy 'openshift-cluster-api-unsupported-aws-spec-fields' with binding 'openshift-cluster-api-unsupported-aws-spec-fields' denied request: spec.template.spec.imageLookupFormat is a forbidden field")
		})

	})
})

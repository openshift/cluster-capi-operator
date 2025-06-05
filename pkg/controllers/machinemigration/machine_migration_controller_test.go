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

package machinemigration

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	capiv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta1"
	capav1builder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("With a running MachineMigration controller", func() {
	var (
		k          komega.Komega
		reconciler *MachineMigrationReconciler

		migrationControllerNamespace *corev1.Namespace
		capiNamespace                *corev1.Namespace
		mapiNamespace                *corev1.Namespace

		mapiMachineBuilder machinev1resourcebuilder.MachineBuilder
		mapiMachine        *machinev1beta1.Machine
		capiMachineBuilder capiv1resourcebuilder.MachineBuilder
		capiMachine        *clusterv1.Machine
		capaMachine        *awsv1.AWSMachine
		capaMachineBuilder capav1builder.AWSMachineBuilder
		capaClusterBuilder capav1builder.AWSClusterBuilder
		capiClusterBuilder capiv1resourcebuilder.ClusterBuilder
		capiCluster        *clusterv1.Cluster
	)

	BeforeEach(func() {
		By("Setting up namespaces for the test")
		migrationControllerNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("machine-migration-controller-").Build()
		Expect(k8sClient.Create(ctx, migrationControllerNamespace)).To(Succeed(), "migration controller namespace should be able to be created")

		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Expect(k8sClient.Create(ctx, mapiNamespace)).To(Succeed(), "MAPI namespace should be able to be created")

		capiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-cluster-api-").Build()
		Expect(k8sClient.Create(ctx, capiNamespace)).To(Succeed(), "CAPI namespace should be able to be created")

		mapiMachineBuilder = machinev1resourcebuilder.Machine().
			WithNamespace(mapiNamespace.GetName()).
			WithName("foo").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil))

		infrastructureName := "cluster-foo"
		capaClusterBuilder = capav1builder.AWSCluster().
			WithNamespace(capiNamespace.GetName()).
			WithName(infrastructureName)
		Expect(k8sClient.Create(ctx, capaClusterBuilder.Build())).To(Succeed(), "CAPA cluster should be able to be created")

		capiClusterBuilder = capiv1resourcebuilder.Cluster().
			WithNamespace(capiNamespace.GetName()).
			WithName(infrastructureName)
		Expect(k8sClient.Create(ctx, capiClusterBuilder.Build())).To(Succeed(), "CAPI cluster should be able to be created")

		capiCluster = &clusterv1.Cluster{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: infrastructureName, Namespace: capiNamespace.GetName()}, capiCluster)).To(Succeed())

		capaMachineBuilder = capav1builder.AWSMachine().
			WithNamespace(capiNamespace.GetName()).
			WithName("machine-template")

		capaMachine = capaMachineBuilder.Build()

		capaMachineRef := corev1.ObjectReference{
			APIVersion: capaMachine.APIVersion,
			Kind:       capaMachine.Kind,
			Name:       capaMachine.GetName(),
			Namespace:  capaMachine.GetNamespace(),
		}

		capiMachineBuilder = capiv1resourcebuilder.Machine().
			WithNamespace(capiNamespace.GetName()).
			WithInfrastructureRef(capaMachineRef).
			WithName("foo").
			WithClusterName(infrastructureName)

		reconciler = &MachineMigrationReconciler{
			Client:        k8sClient,
			Scheme:        testEnv.Scheme,
			CAPINamespace: capiNamespace.GetName(),
			MAPINamespace: mapiNamespace.GetName(),
		}

		k = komega.New(k8sClient)
	})

	AfterEach(func() {
		By("Cleaning up MAPI test resources")
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, mapiNamespace.GetName(),
			&machinev1beta1.Machine{},
			&machinev1beta1.MachineSet{},
			&configv1.Infrastructure{},
		)
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, capiNamespace.GetName(),
			&clusterv1.Machine{},
			&clusterv1.MachineSet{},
			&awsv1.AWSCluster{},
			&awsv1.AWSMachineTemplate{},
			&awsv1.AWSMachine{},
		)
	})

	Describe("Reconcile", func() {
		var req reconcile.Request

		Context("when no migration is requested (status equals spec)", func() {
			BeforeEach(func() {
				By("Setting the MAPI machine spec AuthoritativeAPI to MachineAPI")
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(machinev1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				By("Setting the MAPI machine status AuthoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					mapiMachine.Status.AuthoritativeAPI = machinev1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())

				req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
			})

			It("should do nothing", func() {
				initialMAPIMachineRV := mapiMachine.ResourceVersion
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred(), "reconciler should not have errored")
				Eventually(k.Object(mapiMachine)).Should(HaveField("ObjectMeta.ResourceVersion", Equal(initialMAPIMachineRV)), "should not have modified the machine")
			})
		})

		Context("when status.AuthoritativeAPI is empty (first observation)", func() {
			BeforeEach(func() {
				By("Setting the MAPI machine spec AuthoritativeAPI to MachineAPI")
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(machinev1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				By("Leaving the MAPI machine status AuthoritativeAPI empty")

				req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
			})

			It("should patch the status to match spec and requeue", func() {
				By("Running one reconciliation")
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred(), "reconciler should not have errored")
				updatedM := &machinev1beta1.Machine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachine), updatedM)).To(Succeed())
				Expect(updatedM.Status.AuthoritativeAPI).To(Equal(updatedM.Spec.AuthoritativeAPI))
			})
		})

		Context("when the Synchronized condition is not True", func() {
			BeforeEach(func() {
				By("Setting the MAPI machine spec AuthoritativeAPI to ClusterAPI")
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(machinev1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				By("Setting the MAPI machine status AuthoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					updatedMAPIMachine := mapiMachineBuilder.
						WithAuthoritativeAPIStatus(machinev1beta1.MachineAuthorityMachineAPI).
						WithConditions([]machinev1beta1.Condition{{
							Type:               consts.SynchronizedCondition,
							LastTransitionTime: metav1.Now(),
							Status:             corev1.ConditionFalse}}).
						Build()
					mapiMachine.Status.AuthoritativeAPI = updatedMAPIMachine.Status.AuthoritativeAPI
					mapiMachine.Status.Conditions = updatedMAPIMachine.Status.Conditions
				})).Should(Succeed())

				req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
			})

			It("should do nothing", func() {
				initialMAPIMachineRV := mapiMachine.ResourceVersion
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred(), "reconciler should not have errored")
				Eventually(k.Object(mapiMachine)).Should(HaveField("ObjectMeta.ResourceVersion", Equal(initialMAPIMachineRV)), "should not have modified the machine")
			})
		})

		Context("when a migration request is first detected", func() {
			BeforeEach(func() {
				By("Setting the MAPI machine spec AuthoritativeAPI to ClusterAPI")
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(machinev1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				By("Setting the MAPI machine status AuthoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachine, func() {
					updatedMAPIMachine := mapiMachineBuilder.
						WithAuthoritativeAPIStatus(machinev1beta1.MachineAuthorityMachineAPI).
						WithConditions([]machinev1beta1.Condition{{
							Type:               consts.SynchronizedCondition,
							LastTransitionTime: metav1.Now(),
							Status:             corev1.ConditionTrue}}).
						Build()
					mapiMachine.Status.AuthoritativeAPI = updatedMAPIMachine.Status.AuthoritativeAPI
					mapiMachine.Status.Conditions = updatedMAPIMachine.Status.Conditions
				})).Should(Succeed())

				req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
			})

			It("should acknowledge the migration by updating status to 'Migrating' and requeuing", func() {
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				updatedM := &machinev1beta1.Machine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachine), updatedM)).To(Succeed())
				Expect(updatedM.Status.AuthoritativeAPI).To(Equal(machinev1beta1.MachineAuthorityMigrating))
			})
		})

		Context("when the resource migration has been acknowledged (resource status migrating)", func() {
			Context("when migrating from MachineAPI to ClusterAPI", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine spec AuthoritativeAPI to ClusterAPI")
					mapiMachine = mapiMachineBuilder.
						WithAuthoritativeAPI(machinev1beta1.MachineAuthorityClusterAPI).
						Build()
					Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

					By("Setting the MAPI machine status AuthoritativeAPI to 'Migrating'")
					Eventually(k.UpdateStatus(mapiMachine, func() {
						updatedMAPIMachine := mapiMachineBuilder.
							WithAuthoritativeAPIStatus(machinev1beta1.MachineAuthorityMigrating).
							WithConditions([]machinev1beta1.Condition{{
								Type:               consts.SynchronizedCondition,
								LastTransitionTime: metav1.Now(),
								Status:             corev1.ConditionTrue}}).
							Build()
						mapiMachine.Status.AuthoritativeAPI = updatedMAPIMachine.Status.AuthoritativeAPI
						mapiMachine.Status.Conditions = updatedMAPIMachine.Status.Conditions
					})).Should(Succeed())

					req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
				})

				It("should request pausing for the old authoritative resource (MAPI) and stay in Migrating status", func() {
					_, err := reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())

					updatedM := &machinev1beta1.Machine{}
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachine), updatedM)).To(Succeed())
					// To check for requesting pausing on the the MAPI resource it is sufficient
					// see that the spec.AuthoritativeAPI field is set to ClusterAPI,
					// which is already done anyway on the requester side.
					Expect(updatedM.Spec.AuthoritativeAPI).To(Equal(machinev1beta1.MachineAuthorityClusterAPI))
					Expect(updatedM.Status.AuthoritativeAPI).To(Equal(machinev1beta1.MachineAuthorityMigrating))
				})
			})
			Context("when migrating from ClusterAPI to MachineAPI", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine spec AuthoritativeAPI to MachineAPI")
					mapiMachine = mapiMachineBuilder.WithAuthoritativeAPI(machinev1beta1.MachineAuthorityMachineAPI).Build()
					Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

					By("Creating a mirror CAPI machine")
					capiMachine = capiMachineBuilder.Build()
					Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed())
					capaMachine = capaMachineBuilder.Build()
					Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed())

					By("Setting the MAPI machine status AuthoritativeAPI to 'Migrating'")
					Eventually(k.UpdateStatus(mapiMachine, func() {
						updatedMAPIMachine := mapiMachineBuilder.
							WithAuthoritativeAPIStatus(machinev1beta1.MachineAuthorityMigrating).
							WithConditions([]machinev1beta1.Condition{{
								Type:               consts.SynchronizedCondition,
								LastTransitionTime: metav1.Now(),
								Status:             corev1.ConditionTrue}}).
							Build()
						mapiMachine.Status.AuthoritativeAPI = updatedMAPIMachine.Status.AuthoritativeAPI
						mapiMachine.Status.Conditions = updatedMAPIMachine.Status.Conditions
					})).Should(Succeed())

					req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
				})

				It("should request pausing for the old authoritative resource (CAPI) and stay in Migrating status", func() {
					_, err := reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())

					updatedM := &machinev1beta1.Machine{}
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachine), updatedM)).To(Succeed())
					Expect(updatedM.Status.AuthoritativeAPI).To(Equal(machinev1beta1.MachineAuthorityMigrating))

					updatedCAPIM := &clusterv1.Machine{}
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(capiMachine), updatedCAPIM)).To(Succeed())
					Expect(updatedCAPIM.Annotations).To(HaveKeyWithValue(clusterv1.PausedAnnotation, ""))

					updatedCAPIInfraMachine := &awsv1.AWSMachine{}
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(capaMachine), updatedCAPIInfraMachine)).To(Succeed())
					Expect(updatedCAPIInfraMachine.Annotations).To(HaveKeyWithValue(clusterv1.PausedAnnotation, ""))
				})
			})
		})

		Context("when the old authoritative resource pausing has been requested", func() {
			Context("when migrating from MachineAPI to ClusterAPI", func() {
				Context("when status is not paused for the old authoritative resource (MAPI)", func() {
					BeforeEach(func() {
						By("Setting the MAPI machine spec AuthoritativeAPI to ClusterAPI")
						mapiMachine = mapiMachineBuilder.
							// Set desired authoritative API in spec to ClusterAPI.
							// To check for requesting pausing on the the MAPI resource it is sufficient
							// see that the spec.AuthoritativeAPI field is set to ClusterAPI.
							WithAuthoritativeAPI(machinev1beta1.MachineAuthorityClusterAPI).
							Build()
						Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

						By("Setting the MAPI machine status AuthoritativeAPI to 'Migrating'")
						Eventually(k.UpdateStatus(mapiMachine, func() {
							updatedMAPIMachine := mapiMachineBuilder.
								WithAuthoritativeAPIStatus(machinev1beta1.MachineAuthorityMigrating).
								WithConditions([]machinev1beta1.Condition{
									{
										Type:               consts.SynchronizedCondition,
										LastTransitionTime: metav1.Now(),
										Status:             corev1.ConditionTrue,
									},
									{
										Type:               "Paused",
										LastTransitionTime: metav1.Now(),
										Status:             corev1.ConditionTrue,
									},
								}).
								Build()
							mapiMachine.Status.AuthoritativeAPI = updatedMAPIMachine.Status.AuthoritativeAPI
							mapiMachine.Status.Conditions = updatedMAPIMachine.Status.Conditions
						})).Should(Succeed())

						req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
					})

					It("should set old authoritative API (MAPI) status to paused and requeue", func() {
						_, err := reconciler.Reconcile(ctx, req)
						Expect(err).NotTo(HaveOccurred())

						Eventually(komega.Object(mapiMachine)).Should(
							HaveField("Status.Conditions", SatisfyAll(
								Not(BeEmpty()),
								ContainElement(SatisfyAll(
									HaveField("Type", BeEquivalentTo("Paused")),
									HaveField("Status", Equal(corev1.ConditionTrue)),
								)),
							)),
						)
					})
				})
			})
			Context("when migrating from ClusterAPI to MachineAPI", func() {
				Context("when status is not paused for the old authoritative resource (CAPI)", func() {
					BeforeEach(func() {
						By("Setting the MAPI machine spec AuthoritativeAPI to MachineAPI")
						mapiMachine = mapiMachineBuilder.
							WithAuthoritativeAPI(machinev1beta1.MachineAuthorityMachineAPI).
							Build()
						Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

						By("Creating a mirror CAPI machine")
						capiMachine = capiMachineBuilder.Build()
						Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed())
						capaMachine = capaMachineBuilder.Build()
						Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed())

						By("Setting the MAPI machine status AuthoritativeAPI to 'Migrating'")
						Eventually(k.UpdateStatus(mapiMachine, func() {
							updatedMAPIMachine := mapiMachineBuilder.
								WithAuthoritativeAPIStatus(machinev1beta1.MachineAuthorityMigrating).
								WithConditions([]machinev1beta1.Condition{{
									Type:               consts.SynchronizedCondition,
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionTrue}}).
								Build()
							mapiMachine.Status.AuthoritativeAPI = updatedMAPIMachine.Status.AuthoritativeAPI
							mapiMachine.Status.Conditions = updatedMAPIMachine.Status.Conditions
						})).Should(Succeed())

						By("Setting the CAPI machine status condition to 'Paused'")
						Eventually(k.UpdateStatus(capiMachine, func() {
							updatedCAPIMachine := capiMachineBuilder.Build()
							updatedCAPIMachine.Status.V1Beta2 = &clusterv1.MachineV1Beta2Status{
								Conditions: []metav1.Condition{{
									Type:               clusterv1.PausedV1Beta2Condition,
									Status:             metav1.ConditionTrue,
									LastTransitionTime: metav1.Now(),
								}},
							}
							capiMachine.Status = updatedCAPIMachine.Status
						})).Should(Succeed())

						By("Setting the CAPI infra machine status condition to 'Paused'")
						Eventually(k.UpdateStatus(capaMachine, func() {
							updatedCAPIInfraMachine := capaMachineBuilder.Build()
							updatedCAPIInfraMachine.Status.Conditions = clusterv1.Conditions{
								{
									Type:               clusterv1.PausedV1Beta2Condition,
									Status:             corev1.ConditionTrue,
									LastTransitionTime: metav1.Now(),
								},
							}
							capaMachine.Status = updatedCAPIInfraMachine.Status
						})).Should(Succeed())

						req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
					})

					It("should set old authoritative API (CAPI) status to paused and requeue", func() {
						_, err := reconciler.Reconcile(ctx, req)
						Expect(err).NotTo(HaveOccurred())

						Eventually(komega.Object(capiMachine)).Should(
							HaveField("Status.V1Beta2.Conditions", SatisfyAll(
								Not(BeEmpty()),
								ContainElement(SatisfyAll(
									HaveField("Type", Equal(clusterv1.PausedV1Beta2Condition)),
									HaveField("Status", Equal(metav1.ConditionTrue)),
								)),
							)),
						)
						Eventually(komega.Object(capaMachine)).Should(
							HaveField("Status.Conditions", SatisfyAll(
								Not(BeEmpty()),
								ContainElement(SatisfyAll(
									HaveField("Type", BeEquivalentTo(clusterv1.PausedV1Beta2Condition)),
									HaveField("Status", Equal(corev1.ConditionTrue)),
								)),
							)),
						)
					})
				})
			})
		})

		Context("when the old authoritative resource has been paused", func() {
			Context("when migrating from MachineAPI to ClusterAPI", func() {
				Context("when status synchronizedGeneration is not matching the old authoritativeAPI generation (MAPI)", func() {
					BeforeEach(func() {
						By("Setting the MAPI machine spec AuthoritativeAPI to ClusterAPI")
						mapiMachine = mapiMachineBuilder.
							// Set desired authoritative API in spec to ClusterAPI.
							// To check for requesting pausing on the the MAPI resource it is sufficient
							// see that the spec.AuthoritativeAPI field is set to ClusterAPI.
							WithAuthoritativeAPI(machinev1beta1.MachineAuthorityClusterAPI).
							Build()
						Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

						By("Setting the MAPI machine status AuthoritativeAPI to 'Migrating'")
						Eventually(k.UpdateStatus(mapiMachine, func() {
							updatedMAPIMachine := mapiMachineBuilder.
								WithAuthoritativeAPIStatus(machinev1beta1.MachineAuthorityMigrating).
								WithConditions([]machinev1beta1.Condition{{
									Type:               consts.SynchronizedCondition,
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionTrue}}).
								Build()
							mapiMachine.Status.AuthoritativeAPI = updatedMAPIMachine.Status.AuthoritativeAPI
							mapiMachine.Status.SynchronizedGeneration = 9999 // Do not match .metadata.generation field.
							mapiMachine.Status.Conditions = updatedMAPIMachine.Status.Conditions
						})).Should(Succeed())

						req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
					})

					It("should do nothing", func() {
						initialMAPIMachineRV := mapiMachine.ResourceVersion
						_, err := reconciler.Reconcile(ctx, req)
						Expect(err).NotTo(HaveOccurred(), "reconciler should not have errored")
						Eventually(k.Object(mapiMachine)).Should(HaveField("ObjectMeta.ResourceVersion", Equal(initialMAPIMachineRV)), "should not have modified the machine")
					})
				})
			})
			Context("when migrating from ClusterAPI to MachineAPI", func() {
				Context("when status synchronizedGeneration is not matching the old authoritativeAPI generation (CAPI)", func() {
					BeforeEach(func() {
						By("Setting the MAPI machine spec AuthoritativeAPI to MachineAPI")
						mapiMachine = mapiMachineBuilder.
							WithAuthoritativeAPI(machinev1beta1.MachineAuthorityMachineAPI).
							Build()
						Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

						By("Creating a mirror CAPI machine")
						capiMachine = capiMachineBuilder.Build()
						Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed())
						capaMachine = capaMachineBuilder.Build()
						Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed())

						By("Setting the MAPI machine status AuthoritativeAPI to 'Migrating'")
						Eventually(k.UpdateStatus(mapiMachine, func() {
							updatedMAPIMachine := mapiMachineBuilder.
								WithAuthoritativeAPIStatus(machinev1beta1.MachineAuthorityMigrating).
								WithConditions([]machinev1beta1.Condition{{
									Type:               consts.SynchronizedCondition,
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionTrue}}).
								Build()
							mapiMachine.Status.AuthoritativeAPI = updatedMAPIMachine.Status.AuthoritativeAPI
							mapiMachine.Status.SynchronizedGeneration = 9999 // Do not match .metadata.generation field.
							mapiMachine.Status.Conditions = updatedMAPIMachine.Status.Conditions
						})).Should(Succeed())

						req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
					})

					It("should do nothing", func() {
						initialMAPIMachineRV := mapiMachine.ResourceVersion
						_, err := reconciler.Reconcile(ctx, req)
						Expect(err).NotTo(HaveOccurred(), "reconciler should not have errored")
						Eventually(k.Object(mapiMachine)).Should(HaveField("ObjectMeta.ResourceVersion", Equal(initialMAPIMachineRV)), "should not have modified the machine")
					})
				})
			})
		})

		Context("when all the prerequisites for switching the authoritative API are satisfied", func() {
			Context("when migrating from MachineAPI to ClusterAPI", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine spec AuthoritativeAPI to ClusterAPI")
					mapiMachine = mapiMachineBuilder.
						// Set desired authoritative API in spec to ClusterAPI.
						// To check for requesting pausing on the the MAPI resource it is sufficient
						// see that the spec.AuthoritativeAPI field is set to ClusterAPI.
						WithAuthoritativeAPI(machinev1beta1.MachineAuthorityClusterAPI).
						Build()
					Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

					By("Setting the MAPI machine status AuthoritativeAPI to 'Migrating'")
					Eventually(k.UpdateStatus(mapiMachine, func() {
						updatedMAPIMachine := mapiMachineBuilder.
							WithAuthoritativeAPIStatus(machinev1beta1.MachineAuthorityMigrating).
							WithConditions([]machinev1beta1.Condition{
								{
									Type:               consts.SynchronizedCondition,
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionTrue,
								},
								{
									Type:               "Paused",
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionTrue,
								},
							}).
							Build()
						mapiMachine.Status.AuthoritativeAPI = updatedMAPIMachine.Status.AuthoritativeAPI
						mapiMachine.Status.SynchronizedGeneration = mapiMachine.Generation // Match the MAPI .metadata.generation field.
						mapiMachine.Status.Conditions = updatedMAPIMachine.Status.Conditions
					})).Should(Succeed())

					By("Creating a mirror CAPI machine")
					capiMachine = capiMachineBuilder.
						WithAnnotations(map[string]string{
							clusterv1.PausedAnnotation: "",
						}).
						Build()
					capiMachine.Finalizers = append(capiMachine.Finalizers, clusterv1.MachineFinalizer)
					Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed())

					capaMachine = capaMachineBuilder.
						WithAnnotations(map[string]string{
							clusterv1.PausedAnnotation: "",
						}).
						Build()
					Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed())

					By("Setting the CAPI machine status condition to 'Paused'")
					Eventually(k.UpdateStatus(capiMachine, func() {
						updatedCAPIMachine := capiMachineBuilder.Build()
						updatedCAPIMachine.Status.V1Beta2 = &clusterv1.MachineV1Beta2Status{
							Conditions: []metav1.Condition{{
								Type:               clusterv1.PausedV1Beta2Condition,
								Status:             metav1.ConditionTrue,
								LastTransitionTime: metav1.Now(),
							}},
						}
						capiMachine.Status = updatedCAPIMachine.Status
					})).Should(Succeed())

					req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
				})

				It("should set the new to-be authoritative resource (CAPI) to actually be authoritative and unpause it", func() {
					result, err := reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())
					Expect(result.Requeue).To(BeFalse())

					Eventually(komega.Object(mapiMachine)).Should(SatisfyAll(
						HaveField("Status.AuthoritativeAPI", Equal(machinev1beta1.MachineAuthorityClusterAPI)),
						HaveField("Status.SynchronizedGeneration", BeZero()),
					))

					Eventually(komega.Object(capiMachine)).ShouldNot(
						HaveField("ObjectMeta.Annotations", ContainElement(HaveKeyWithValue(clusterv1.PausedAnnotation, ""))))
				})
			})
			Context("when migrating from ClusterAPI to MachineAPI", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine spec AuthoritativeAPI to MachineAPI")
					mapiMachine = mapiMachineBuilder.
						WithAuthoritativeAPI(machinev1beta1.MachineAuthorityMachineAPI).
						Build()
					Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

					By("Creating a mirror CAPI machine")
					capiMachine = capiMachineBuilder.
						WithAnnotations(map[string]string{
							clusterv1.PausedAnnotation: "",
						}).
						Build()
					Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed())
					capaMachine = capaMachineBuilder.
						WithAnnotations(map[string]string{
							clusterv1.PausedAnnotation: "",
						}).
						Build()
					Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed())

					By("Setting the MAPI machine status AuthoritativeAPI to 'Migrating'")
					Eventually(k.UpdateStatus(mapiMachine, func() {
						updatedMAPIMachine := mapiMachineBuilder.
							WithAuthoritativeAPIStatus(machinev1beta1.MachineAuthorityMigrating).
							WithConditions([]machinev1beta1.Condition{
								{
									Type:               consts.SynchronizedCondition,
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionTrue,
								},
								{
									Type:               "Paused",
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionTrue,
								},
							}).
							Build()
						mapiMachine.Status.AuthoritativeAPI = updatedMAPIMachine.Status.AuthoritativeAPI
						mapiMachine.Status.SynchronizedGeneration = capiMachine.Generation // Match the CAPI .metadata.generation field.
						mapiMachine.Status.Conditions = updatedMAPIMachine.Status.Conditions
					})).Should(Succeed())

					By("Setting the CAPI machine status condition to 'Paused'")
					Eventually(k.UpdateStatus(capiMachine, func() {
						updatedCAPIMachine := capiMachineBuilder.Build()
						updatedCAPIMachine.Status.V1Beta2 = &clusterv1.MachineV1Beta2Status{
							Conditions: []metav1.Condition{{
								Type:               clusterv1.PausedV1Beta2Condition,
								Status:             metav1.ConditionTrue,
								LastTransitionTime: metav1.Now(),
							}},
						}
						capiMachine.Status = updatedCAPIMachine.Status
					})).Should(Succeed())

					By("Setting the CAPI infra machine status condition to 'Paused'")
					Eventually(k.UpdateStatus(capaMachine, func() {
						updatedCAPIInfraMachine := capaMachineBuilder.WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).Build()
						updatedCAPIInfraMachine.Status.Conditions = clusterv1.Conditions{
							{
								Type:               clusterv1.PausedV1Beta2Condition,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: metav1.Now(),
							},
						}
						capaMachine.Status = updatedCAPIInfraMachine.Status
					})).Should(Succeed())
					req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)}
				})

				It("should set the new to-be authoritative resource (MAPI) to actually be authoritative and requeue to unpause it", func() {
					result, err := reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())
					Expect(result.Requeue).To(BeFalse())

					Eventually(komega.Object(mapiMachine)).Should(SatisfyAll(
						HaveField("Status.AuthoritativeAPI", Equal(machinev1beta1.MachineAuthorityMachineAPI)),
						HaveField("Status.SynchronizedGeneration", BeZero()),
					))
				})
			})
		})
	})
})

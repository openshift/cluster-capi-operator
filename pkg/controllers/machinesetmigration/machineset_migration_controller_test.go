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

package machinesetmigration

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	clusterv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta2"
	awsv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("With a running MachineSetMigration controller", func() {
	var (
		k          komega.Komega
		reconciler *MachineSetMigrationReconciler

		migrationControllerNamespace *corev1.Namespace
		capiNamespace                *corev1.Namespace
		mapiNamespace                *corev1.Namespace

		mapiMachineSetBuilder      machinev1resourcebuilder.MachineSetBuilder
		mapiMachineSet             *mapiv1beta1.MachineSet
		capiMachineSetBuilder      clusterv1resourcebuilder.MachineSetBuilder
		capiMachineSet             *clusterv1.MachineSet
		capaMachineTemplateBuilder awsv1resourcebuilder.AWSMachineTemplateBuilder
		capaMachineTemplate        *awsv1.AWSMachineTemplate
		capaClusterBuilder         awsv1resourcebuilder.AWSClusterBuilder
		capiClusterBuilder         clusterv1resourcebuilder.ClusterBuilder
		capiCluster                *clusterv1.Cluster
	)

	BeforeEach(func() {
		By("Setting up namespaces for the test")
		migrationControllerNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("machineset-migration-controller-").Build()
		Expect(k8sClient.Create(ctx, migrationControllerNamespace)).To(Succeed(), "migration controller namespace should be able to be created")

		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-machine-api-").Build()
		Expect(k8sClient.Create(ctx, mapiNamespace)).To(Succeed(), "MAPI namespace should be able to be created")

		capiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("openshift-cluster-api-").Build()
		Expect(k8sClient.Create(ctx, capiNamespace)).To(Succeed(), "CAPI namespace should be able to be created")

		mapiMachineSetBuilder = machinev1resourcebuilder.MachineSet().
			WithNamespace(mapiNamespace.GetName()).
			WithName("foo").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil))

		infrastructureName := "cluster-foo"
		capaClusterBuilder = awsv1resourcebuilder.AWSCluster().
			WithNamespace(capiNamespace.GetName()).
			WithName(infrastructureName)
		Expect(k8sClient.Create(ctx, capaClusterBuilder.Build())).To(Succeed(), "CAPA cluster should be able to be created")

		capiClusterBuilder = clusterv1resourcebuilder.Cluster().
			WithNamespace(capiNamespace.GetName()).
			WithName(infrastructureName)
		Expect(k8sClient.Create(ctx, capiClusterBuilder.Build())).To(Succeed(), "CAPI cluster should be able to be created")

		capiCluster = &clusterv1.Cluster{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: infrastructureName, Namespace: capiNamespace.GetName()}, capiCluster)).To(Succeed())

		capaMachineTemplateBuilder = awsv1resourcebuilder.AWSMachineTemplate().
			WithNamespace(capiNamespace.GetName()).
			WithName("machine-template")
		capaMachineTemplate = capaMachineTemplateBuilder.Build()

		capiMachineTemplate := clusterv1.MachineTemplateSpec{
			Spec: clusterv1.MachineSpec{
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: awsv1.GroupVersion.Group,
					Kind:     capaMachineTemplate.Kind,
					Name:     capaMachineTemplate.GetName(),
				},
			},
		}
		capiMachineSetBuilder = clusterv1resourcebuilder.MachineSet().
			WithNamespace(capiNamespace.GetName()).
			WithName("foo").
			WithTemplate(capiMachineTemplate).
			WithClusterName(infrastructureName)

		reconciler = &MachineSetMigrationReconciler{
			Client:        k8sClient,
			CAPINamespace: capiNamespace.GetName(),
			MAPINamespace: mapiNamespace.GetName(),
		}

		k = komega.New(k8sClient)
	})

	AfterEach(func() {
		By("Cleaning up MAPI test resources")
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, mapiNamespace.GetName(),
			&mapiv1beta1.Machine{},
			&mapiv1beta1.MachineSet{},
			&configv1.Infrastructure{},
		)
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, capiNamespace.GetName(),
			&clusterv1.Machine{},
			&clusterv1.MachineSet{},
			&awsv1.AWSCluster{},
			&awsv1.AWSMachineTemplate{},
		)
	})

	Describe("Reconcile", func() {
		var req reconcile.Request

		Context("when no migration is requested (status equals spec)", func() {
			BeforeEach(func() {
				By("Setting the MAPI machine set spec AuthoritativeAPI to MachineAPI")
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				By("Setting the MAPI machine set status AuthoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMachineAPI
				})).Should(Succeed())

				req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
			})

			It("should do nothing", func() {
				initialMAPIMachineSetRV := mapiMachineSet.ResourceVersion
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred(), "reconciler should not have errored")
				Eventually(k.Object(mapiMachineSet)).Should(HaveField("ObjectMeta.ResourceVersion", Equal(initialMAPIMachineSetRV)), "should not have modified the machine set")
			})
		})

		Context("when status.AuthoritativeAPI is empty (first observation)", func() {
			BeforeEach(func() {
				By("Setting the MAPI machine set spec AuthoritativeAPI to MachineAPI")
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				By("Leaving the MAPI machine set status AuthoritativeAPI empty")

				req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
			})

			It("should patch the status to match spec and requeue", func() {
				By("Running one reconciliation")
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred(), "reconciler should not have errored")
				updatedMS := &mapiv1beta1.MachineSet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachineSet), updatedMS)).To(Succeed())
				Expect(updatedMS.Status.AuthoritativeAPI).To(Equal(updatedMS.Spec.AuthoritativeAPI))
			})
		})

		Context("when the Synchronized condition is not True", func() {
			BeforeEach(func() {
				By("Setting the MAPI machine set spec AuthoritativeAPI to ClusterAPI")
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				By("Setting the MAPI machine set status AuthoritativeAPI and SynchronizedAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					updatedMAPIMachineSet := mapiMachineSetBuilder.
						WithAuthoritativeAPIStatus(mapiv1beta1.MachineAuthorityMachineAPI).
						WithConditions([]mapiv1beta1.Condition{{
							Type:               consts.SynchronizedCondition,
							LastTransitionTime: metav1.Now(),
							Status:             corev1.ConditionFalse}}).
						Build()
					mapiMachineSet.Status.AuthoritativeAPI = updatedMAPIMachineSet.Status.AuthoritativeAPI
					mapiMachineSet.Status.SynchronizedAPI = mapiv1beta1.MachineAPISynchronized
					mapiMachineSet.Status.Conditions = updatedMAPIMachineSet.Status.Conditions
				})).Should(Succeed())

				req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
			})

			It("should do nothing", func() {
				initialMAPIMachineSetRV := mapiMachineSet.ResourceVersion
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred(), "reconciler should not have errored")
				Eventually(k.Object(mapiMachineSet)).Should(HaveField("ObjectMeta.ResourceVersion", Equal(initialMAPIMachineSetRV)), "should not have modified the machine set")
			})
		})

		Context("when a migration request is first detected", func() {
			BeforeEach(func() {
				By("Setting the MAPI machine set spec AuthoritativeAPI to ClusterAPI")
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				By("Creating a mirror CAPI machine set")
				capiMachineSet = capiMachineSetBuilder.Build()
				Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

				By("Setting the MAPI machine set status AuthoritativeAPI to MachineAPI")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					updatedMAPIMachineSet := mapiMachineSetBuilder.
						WithAuthoritativeAPIStatus(mapiv1beta1.MachineAuthorityMachineAPI).
						WithConditions([]mapiv1beta1.Condition{{
							Type:               consts.SynchronizedCondition,
							LastTransitionTime: metav1.Now(),
							Status:             corev1.ConditionTrue}}).
						Build()
					mapiMachineSet.Status.AuthoritativeAPI = updatedMAPIMachineSet.Status.AuthoritativeAPI
					mapiMachineSet.Status.SynchronizedAPI = mapiv1beta1.MachineAPISynchronized
					mapiMachineSet.Status.Conditions = updatedMAPIMachineSet.Status.Conditions
					mapiMachineSet.Status.SynchronizedGeneration = capiMachineSet.Generation
				})).Should(Succeed())

				req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
			})

			It("should acknowledge the migration by updating status to 'Migrating' and requeuing", func() {
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				updatedMS := &mapiv1beta1.MachineSet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachineSet), updatedMS)).To(Succeed())
				Expect(updatedMS.Status.AuthoritativeAPI).To(Equal(mapiv1beta1.MachineAuthorityMigrating))
			})
		})

		Context("when the resource migration has been acknowledged (resource status migrating)", func() {
			Context("when migrating from MachineAPI to ClusterAPI", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine set spec AuthoritativeAPI to ClusterAPI")
					mapiMachineSet = mapiMachineSetBuilder.
						WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
						Build()
					Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

					By("Creating a mirror CAPI machine set with paused annotation")
					capiMachineSet = capiMachineSetBuilder.
						WithAnnotations(map[string]string{
							clusterv1.PausedAnnotation: "",
						}).
						Build()
					Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

					By("Setting the MAPI machine set status AuthoritativeAPI to 'Migrating'")
					Eventually(k.UpdateStatus(mapiMachineSet, func() {
						updatedMAPIMachineSet := mapiMachineSetBuilder.
							WithAuthoritativeAPIStatus(mapiv1beta1.MachineAuthorityMigrating).
							WithConditions([]mapiv1beta1.Condition{{
								Type:               consts.SynchronizedCondition,
								LastTransitionTime: metav1.Now(),
								Status:             corev1.ConditionTrue}}).
							Build()
						mapiMachineSet.Status.AuthoritativeAPI = updatedMAPIMachineSet.Status.AuthoritativeAPI
						mapiMachineSet.Status.SynchronizedAPI = mapiv1beta1.MachineAPISynchronized
						mapiMachineSet.Status.Conditions = updatedMAPIMachineSet.Status.Conditions
						mapiMachineSet.Status.SynchronizedGeneration = capiMachineSet.Generation
					})).Should(Succeed())

					req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
				})

				It("should request pausing for the old authoritative resource (MAPI) and stay in Migrating status", func() {
					_, err := reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())

					updatedMS := &mapiv1beta1.MachineSet{}
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachineSet), updatedMS)).To(Succeed())
					// To check for requesting pausing on the the MAPI resource it is sufficient
					// see that the spec.AuthoritativeAPI field is set to ClusterAPI,
					// which is already done anyway on the requester side.
					Expect(updatedMS.Spec.AuthoritativeAPI).To(Equal(mapiv1beta1.MachineAuthorityClusterAPI))
					Expect(updatedMS.Status.AuthoritativeAPI).To(Equal(mapiv1beta1.MachineAuthorityMigrating))
				})
			})
			Context("when migrating from ClusterAPI to MachineAPI", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine set spec AuthoritativeAPI to MachineAPI")
					mapiMachineSet = mapiMachineSetBuilder.WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).Build()
					Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

					By("Creating a mirror CAPI machine set")
					capiMachineSet = capiMachineSetBuilder.Build()
					Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

					By("Setting the MAPI machine set status AuthoritativeAPI to 'Migrating'")
					Eventually(k.UpdateStatus(mapiMachineSet, func() {
						updatedMAPIMachineSet := mapiMachineSetBuilder.
							WithAuthoritativeAPIStatus(mapiv1beta1.MachineAuthorityMigrating).
							WithSynchronizedAPIStatus(mapiv1beta1.ClusterAPISynchronized).
							WithConditions([]mapiv1beta1.Condition{{
								Type:               consts.SynchronizedCondition,
								LastTransitionTime: metav1.Now(),
								Status:             corev1.ConditionTrue}}).
							Build()
						mapiMachineSet.Status.AuthoritativeAPI = updatedMAPIMachineSet.Status.AuthoritativeAPI
						mapiMachineSet.Status.SynchronizedAPI = updatedMAPIMachineSet.Status.SynchronizedAPI
						mapiMachineSet.Status.Conditions = updatedMAPIMachineSet.Status.Conditions
						mapiMachineSet.Status.SynchronizedGeneration = mapiMachineSet.Generation
					})).Should(Succeed())

					req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
				})

				It("should request pausing for the old authoritative resource (CAPI) and stay in Migrating status", func() {
					_, err := reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())

					updatedMS := &mapiv1beta1.MachineSet{}
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachineSet), updatedMS)).To(Succeed())
					Expect(updatedMS.Status.AuthoritativeAPI).To(Equal(mapiv1beta1.MachineAuthorityMigrating))

					updatedCAPIMS := &clusterv1.MachineSet{}
					Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(capiMachineSet), updatedCAPIMS)).To(Succeed())
					Expect(updatedCAPIMS.Annotations).To(HaveKeyWithValue(clusterv1.PausedAnnotation, ""))
				})
			})
		})

		Context("when the old authoritative resource pausing has been requested", func() {
			Context("when migrating from MachineAPI to ClusterAPI", func() {
				Context("when status is not paused for the old authoritative resource (MAPI)", func() {
					BeforeEach(func() {
						By("Setting the MAPI machine set spec AuthoritativeAPI to ClusterAPI")
						mapiMachineSet = mapiMachineSetBuilder.
							// Set desired authoritative API in spec to ClusterAPI.
							// To check for requesting pausing on the the MAPI resource it is sufficient
							// see that the spec.AuthoritativeAPI field is set to ClusterAPI.
							WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
							Build()
						Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

						By("Creating a mirror CAPI machine set")
						capiMachineSet = capiMachineSetBuilder.Build()
						Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

						By("Setting the MAPI machine set status AuthoritativeAPI to 'Migrating'")
						Eventually(k.UpdateStatus(mapiMachineSet, func() {
							updatedMAPIMachineSet := mapiMachineSetBuilder.
								WithAuthoritativeAPIStatus(mapiv1beta1.MachineAuthorityMigrating).
								WithSynchronizedAPIStatus(mapiv1beta1.MachineAPISynchronized).
								WithConditions([]mapiv1beta1.Condition{
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
							mapiMachineSet.Status.AuthoritativeAPI = updatedMAPIMachineSet.Status.AuthoritativeAPI
							mapiMachineSet.Status.SynchronizedAPI = updatedMAPIMachineSet.Status.SynchronizedAPI
							mapiMachineSet.Status.Conditions = updatedMAPIMachineSet.Status.Conditions
							mapiMachineSet.Status.SynchronizedGeneration = capiMachineSet.Generation
						})).Should(Succeed())

						req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
					})

					It("should set old authoritative API (MAPI) status to paused and requeue", func() {
						_, err := reconciler.Reconcile(ctx, req)
						Expect(err).NotTo(HaveOccurred())

						Eventually(komega.Object(mapiMachineSet)).Should(
							HaveField("Status.Conditions", SatisfyAll(
								Not(BeEmpty()),
								ContainElement(SatisfyAll(
									HaveField("Type", BeEquivalentTo("Paused")),
									HaveField("Status", BeEquivalentTo(corev1.ConditionTrue)),
								)),
							)),
						)
					})
				})
			})
			Context("when migrating from ClusterAPI to MachineAPI", func() {
				Context("when status is not paused for the old authoritative resource (CAPI)", func() {
					BeforeEach(func() {
						By("Setting the MAPI machine set spec AuthoritativeAPI to MachineAPI")
						mapiMachineSet = mapiMachineSetBuilder.
							WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
							Build()
						Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

						By("Creating a mirror CAPI machine set")
						capiMachineSet = capiMachineSetBuilder.Build()
						Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

						By("Setting the MAPI machine set status AuthoritativeAPI to 'Migrating'")
						Eventually(k.UpdateStatus(mapiMachineSet, func() {
							updatedMAPIMachineSet := mapiMachineSetBuilder.
								WithAuthoritativeAPIStatus(mapiv1beta1.MachineAuthorityMigrating).
								WithSynchronizedAPIStatus(mapiv1beta1.ClusterAPISynchronized).
								WithConditions([]mapiv1beta1.Condition{{
									Type:               consts.SynchronizedCondition,
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionTrue}}).
								Build()
							mapiMachineSet.Status.AuthoritativeAPI = updatedMAPIMachineSet.Status.AuthoritativeAPI
							mapiMachineSet.Status.SynchronizedAPI = updatedMAPIMachineSet.Status.SynchronizedAPI
							mapiMachineSet.Status.Conditions = updatedMAPIMachineSet.Status.Conditions
						})).Should(Succeed())

						By("Setting the CAPI machine set status condition to 'Paused'")
						Eventually(k.UpdateStatus(capiMachineSet, func() {
							updatedCAPIMachineSet := capiMachineSetBuilder.Build()
							updatedCAPIMachineSet.Status.Conditions = []metav1.Condition{{
								Type:               clusterv1.PausedCondition,
								Status:             metav1.ConditionTrue,
								LastTransitionTime: metav1.Now(),
							}}
							capiMachineSet.Status = updatedCAPIMachineSet.Status
						})).Should(Succeed())

						req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
					})

					It("should set old authoritative API (CAPI) status to paused and requeue", func() {
						_, err := reconciler.Reconcile(ctx, req)
						Expect(err).NotTo(HaveOccurred())

						Eventually(komega.Object(capiMachineSet)).Should(
							HaveField("Status.Conditions", SatisfyAll(
								Not(BeEmpty()),
								ContainElement(SatisfyAll(
									HaveField("Type", Equal(clusterv1.PausedCondition)),
									HaveField("Status", Equal(metav1.ConditionTrue)),
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
						By("Setting the MAPI machine set spec AuthoritativeAPI to ClusterAPI")
						mapiMachineSet = mapiMachineSetBuilder.
							// Set desired authoritative API in spec to ClusterAPI.
							// To check for requesting pausing on the the MAPI resource it is sufficient
							// see that the spec.AuthoritativeAPI field is set to ClusterAPI.
							WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
							Build()
						Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

						By("Creating a mirror CAPI machine set")
						capiMachineSet = capiMachineSetBuilder.Build()
						Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

						By("Setting the MAPI machine set status AuthoritativeAPI to 'Migrating'")
						Eventually(k.UpdateStatus(mapiMachineSet, func() {
							updatedMAPIMachineSet := mapiMachineSetBuilder.
								WithAuthoritativeAPIStatus(mapiv1beta1.MachineAuthorityMigrating).
								WithSynchronizedAPIStatus(mapiv1beta1.MachineAPISynchronized).
								WithSynchronizedGeneration(9999). // Do not match .metadata.generation field.
								WithConditions([]mapiv1beta1.Condition{{
									Type:               consts.SynchronizedCondition,
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionTrue}}).
								Build()
							mapiMachineSet.Status.AuthoritativeAPI = updatedMAPIMachineSet.Status.AuthoritativeAPI
							mapiMachineSet.Status.SynchronizedAPI = updatedMAPIMachineSet.Status.SynchronizedAPI
							mapiMachineSet.Status.SynchronizedGeneration = updatedMAPIMachineSet.Status.SynchronizedGeneration
							mapiMachineSet.Status.Conditions = updatedMAPIMachineSet.Status.Conditions
						})).Should(Succeed())

						req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
					})

					It("should do nothing", func() {
						initialMAPIMachineSetRV := mapiMachineSet.ResourceVersion
						_, err := reconciler.Reconcile(ctx, req)
						Expect(err).NotTo(HaveOccurred(), "reconciler should not have errored")
						Eventually(k.Object(mapiMachineSet)).Should(HaveField("ObjectMeta.ResourceVersion", Equal(initialMAPIMachineSetRV)), "should not have modified the machine set")
					})
				})
			})
			Context("when migrating from ClusterAPI to MachineAPI", func() {
				Context("when status synchronizedGeneration is not matching the old authoritativeAPI generation (CAPI)", func() {
					BeforeEach(func() {
						By("Setting the MAPI machine set spec AuthoritativeAPI to MachineAPI")
						mapiMachineSet = mapiMachineSetBuilder.
							WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
							Build()
						Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

						By("Creating a mirror CAPI machine set")
						capiMachineSet = capiMachineSetBuilder.Build()
						Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

						By("Setting the MAPI machine set status AuthoritativeAPI to 'Migrating'")
						Eventually(k.UpdateStatus(mapiMachineSet, func() {
							updatedMAPIMachineSet := mapiMachineSetBuilder.
								WithAuthoritativeAPIStatus(mapiv1beta1.MachineAuthorityMigrating).
								WithSynchronizedAPIStatus(mapiv1beta1.ClusterAPISynchronized).
								WithSynchronizedGeneration(9999). // Do not match .metadata.generation field.
								WithConditions([]mapiv1beta1.Condition{{
									Type:               consts.SynchronizedCondition,
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionTrue}}).
								Build()
							mapiMachineSet.Status.AuthoritativeAPI = updatedMAPIMachineSet.Status.AuthoritativeAPI
							mapiMachineSet.Status.SynchronizedAPI = updatedMAPIMachineSet.Status.SynchronizedAPI
							mapiMachineSet.Status.SynchronizedGeneration = updatedMAPIMachineSet.Status.SynchronizedGeneration
							mapiMachineSet.Status.Conditions = updatedMAPIMachineSet.Status.Conditions
						})).Should(Succeed())

						req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
					})

					It("should do nothing", func() {
						initialMAPIMachineSetRV := mapiMachineSet.ResourceVersion
						_, err := reconciler.Reconcile(ctx, req)
						Expect(err).NotTo(HaveOccurred(), "reconciler should not have errored")
						Eventually(k.Object(mapiMachineSet)).Should(HaveField("ObjectMeta.ResourceVersion", Equal(initialMAPIMachineSetRV)), "should not have modified the machine set")
					})
				})
			})
		})

		Context("when the new authoritative resource is still paused", func() {
			Context("when migrating from MachineAPI to ClusterAPI", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine set spec AuthoritativeAPI to ClusterAPI")
					mapiMachineSet = mapiMachineSetBuilder.
						WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
						Build()
					Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

					By("Creating a mirror CAPI machine set")
					capiMachineSet = capiMachineSetBuilder.Build()
					Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

					By("Setting the MAPI machine set status AuthoritativeAPI to 'Migrating' with Paused=True (not yet unpaused)")
					Eventually(k.UpdateStatus(mapiMachineSet, func() {
						updatedMAPIMachineSet := mapiMachineSetBuilder.
							WithAuthoritativeAPIStatus(mapiv1beta1.MachineAuthorityMigrating).
							WithSynchronizedAPIStatus(mapiv1beta1.MachineAPISynchronized).
							WithSynchronizedGeneration(mapiMachineSet.Generation).
							WithConditions([]mapiv1beta1.Condition{
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
						mapiMachineSet.Status.AuthoritativeAPI = updatedMAPIMachineSet.Status.AuthoritativeAPI
						mapiMachineSet.Status.SynchronizedAPI = updatedMAPIMachineSet.Status.SynchronizedAPI
						mapiMachineSet.Status.SynchronizedGeneration = updatedMAPIMachineSet.Status.SynchronizedGeneration
						mapiMachineSet.Status.Conditions = updatedMAPIMachineSet.Status.Conditions
					})).Should(Succeed())

					By("Setting the CAPI machine set status condition to 'Paused' (not yet unpaused)")
					Eventually(k.UpdateStatus(capiMachineSet, func() {
						updatedCAPIMachineSet := capiMachineSetBuilder.Build()
						updatedCAPIMachineSet.Status.Conditions = []metav1.Condition{{
							Type:               clusterv1.PausedCondition,
							Status:             metav1.ConditionTrue,
							LastTransitionTime: metav1.Now(),
						}}
						capiMachineSet.Status = updatedCAPIMachineSet.Status
					})).Should(Succeed())

					req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
				})

				It("should wait in Migrating until unpaused, then complete migration", func() {
					result, err := reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(ctrl.Result{}))

					Eventually(komega.Object(mapiMachineSet)).Should(
						HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
					)
					Eventually(komega.Object(capiMachineSet)).ShouldNot(
						HaveField("ObjectMeta.Annotations", HaveKey(clusterv1.PausedAnnotation)),
					)

					By("Unpausing the CAPI machine set")
					Eventually(k.UpdateStatus(capiMachineSet, func() {
						updatedCAPIMachineSet := capiMachineSetBuilder.Build()
						updatedCAPIMachineSet.Status.Conditions = []metav1.Condition{{
							Type:               clusterv1.PausedCondition,
							Status:             metav1.ConditionFalse,
							LastTransitionTime: metav1.Now(),
						}}
						capiMachineSet.Status = updatedCAPIMachineSet.Status
					})).Should(Succeed())

					result, err = reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(ctrl.Result{}))

					Eventually(komega.Object(mapiMachineSet)).Should(SatisfyAll(
						HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
						HaveField("Status.SynchronizedGeneration", BeZero()),
					))
				})
			})

			Context("when migrating from ClusterAPI to MachineAPI", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine set spec AuthoritativeAPI to MachineAPI")
					mapiMachineSet = mapiMachineSetBuilder.
						WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
						Build()
					Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

					By("Creating a mirror CAPI machine set with paused annotation")
					capiMachineSet = capiMachineSetBuilder.
						WithAnnotations(map[string]string{
							clusterv1.PausedAnnotation: "",
						}).
						Build()
					Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

					By("Setting the MAPI machine set status AuthoritativeAPI to 'Migrating' with Paused=True (not yet unpaused)")
					Eventually(k.UpdateStatus(mapiMachineSet, func() {
						updatedMAPIMachineSet := mapiMachineSetBuilder.
							WithAuthoritativeAPIStatus(mapiv1beta1.MachineAuthorityMigrating).
							WithSynchronizedAPIStatus(mapiv1beta1.ClusterAPISynchronized).
							WithSynchronizedGeneration(capiMachineSet.Generation).
							WithConditions([]mapiv1beta1.Condition{
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
						mapiMachineSet.Status.AuthoritativeAPI = updatedMAPIMachineSet.Status.AuthoritativeAPI
						mapiMachineSet.Status.SynchronizedAPI = updatedMAPIMachineSet.Status.SynchronizedAPI
						mapiMachineSet.Status.SynchronizedGeneration = updatedMAPIMachineSet.Status.SynchronizedGeneration
						mapiMachineSet.Status.Conditions = updatedMAPIMachineSet.Status.Conditions
					})).Should(Succeed())

					By("Setting the CAPI machine set status condition to 'Paused'")
					Eventually(k.UpdateStatus(capiMachineSet, func() {
						updatedCAPIMachineSet := capiMachineSetBuilder.Build()
						updatedCAPIMachineSet.Status.Conditions = []metav1.Condition{{
							Type:               clusterv1.PausedCondition,
							Status:             metav1.ConditionTrue,
							LastTransitionTime: metav1.Now(),
						}}
						capiMachineSet.Status = updatedCAPIMachineSet.Status
					})).Should(Succeed())

					req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
				})

				It("should wait in Migrating until unpaused, then complete migration", func() {
					result, err := reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(ctrl.Result{}))

					Eventually(komega.Object(mapiMachineSet)).Should(
						HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
					)

					By("Unpausing the MAPI machine set")
					Eventually(k.UpdateStatus(mapiMachineSet, func() {
						updatedMAPIMachineSet := mapiMachineSetBuilder.
							WithAuthoritativeAPIStatus(mapiv1beta1.MachineAuthorityMigrating).
							WithSynchronizedAPIStatus(mapiv1beta1.ClusterAPISynchronized).
							WithSynchronizedGeneration(capiMachineSet.Generation).
							WithConditions([]mapiv1beta1.Condition{
								{
									Type:               consts.SynchronizedCondition,
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionTrue,
								},
								{
									Type:               "Paused",
									LastTransitionTime: metav1.Now(),
									Status:             corev1.ConditionFalse,
								},
							}).
							Build()
						mapiMachineSet.Status.AuthoritativeAPI = updatedMAPIMachineSet.Status.AuthoritativeAPI
						mapiMachineSet.Status.SynchronizedAPI = updatedMAPIMachineSet.Status.SynchronizedAPI
						mapiMachineSet.Status.SynchronizedGeneration = updatedMAPIMachineSet.Status.SynchronizedGeneration
						mapiMachineSet.Status.Conditions = updatedMAPIMachineSet.Status.Conditions
					})).Should(Succeed())

					result, err = reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())
					Expect(result).To(Equal(ctrl.Result{}))

					Eventually(komega.Object(mapiMachineSet)).Should(SatisfyAll(
						HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
						HaveField("Status.SynchronizedGeneration", BeZero()),
					))
				})
			})
		})

		Context("when transitioning from stable state to Migrating", func() {
			BeforeEach(func() {
				By("Setting the MAPI machine set spec AuthoritativeAPI to MachineAPI (target)")
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				By("Creating mirror CAPI machine set")
				capiMachineSet = capiMachineSetBuilder.Build()
				Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

				By("Setting current authority to ClusterAPI with proper sync status")
				Eventually(k.UpdateStatus(mapiMachineSet, func() {
					mapiMachineSet.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityClusterAPI
					mapiMachineSet.Status.SynchronizedAPI = mapiv1beta1.ClusterAPISynchronized
					mapiMachineSet.Status.SynchronizedGeneration = capiMachineSet.Generation
					mapiMachineSet.Status.Conditions = []mapiv1beta1.Condition{{
						Type:               consts.SynchronizedCondition,
						LastTransitionTime: metav1.Now(),
						Status:             corev1.ConditionTrue,
					}}
				})).Should(Succeed())

				req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
			})

			It("should preserve SynchronizedAPI as ClusterAPI when transitioning to Migrating", func() {
				_, err := reconciler.Reconcile(ctx, req)
				Expect(err).NotTo(HaveOccurred())

				Eventually(k.Object(mapiMachineSet)).Should(SatisfyAll(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
					HaveField("Status.SynchronizedAPI", Equal(mapiv1beta1.ClusterAPISynchronized)),
				))
			})
		})

		Context("when migration is cancelled during Migrating state", func() {
			Context("when cancelling MAPI->CAPI migration back to MAPI", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine set spec AuthoritativeAPI to MachineAPI (cancellation)")
					mapiMachineSet = mapiMachineSetBuilder.
						WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
						Build()
					Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

					By("Creating mirror CAPI machine set")
					capiMachineSet = capiMachineSetBuilder.Build()
					Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

					By("Setting status to Migrating with SynchronizedAPI=MachineAPISynchronized (was migrating from MAPI)")
					Eventually(k.UpdateStatus(mapiMachineSet, func() {
						mapiMachineSet.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMigrating
						mapiMachineSet.Status.SynchronizedAPI = mapiv1beta1.MachineAPISynchronized
						mapiMachineSet.Status.SynchronizedGeneration = mapiMachineSet.Generation
						mapiMachineSet.Status.Conditions = []mapiv1beta1.Condition{{
							Type:               consts.SynchronizedCondition,
							LastTransitionTime: metav1.Now(),
							Status:             corev1.ConditionTrue,
						}}
					})).Should(Succeed())

					req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
				})

				It("should rollback to MachineAPI and reset sync status", func() {
					_, err := reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())

					Eventually(k.Object(mapiMachineSet)).Should(SatisfyAll(
						HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
						HaveField("Status.SynchronizedGeneration", BeZero()),
					))
				})
			})

			Context("when cancelling CAPI->MAPI migration back to CAPI", func() {
				BeforeEach(func() {
					By("Setting the MAPI machine set spec AuthoritativeAPI to ClusterAPI (cancellation)")
					mapiMachineSet = mapiMachineSetBuilder.
						WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
						Build()
					Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

					By("Creating mirror CAPI machine set with paused annotation")
					capiMachineSet = capiMachineSetBuilder.
						WithAnnotations(map[string]string{
							clusterv1.PausedAnnotation: "",
						}).
						Build()
					Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

					By("Setting status to Migrating with SynchronizedAPI=ClusterAPISynchronized (was migrating from CAPI)")
					Eventually(k.UpdateStatus(mapiMachineSet, func() {
						mapiMachineSet.Status.AuthoritativeAPI = mapiv1beta1.MachineAuthorityMigrating
						mapiMachineSet.Status.SynchronizedAPI = mapiv1beta1.ClusterAPISynchronized
						mapiMachineSet.Status.SynchronizedGeneration = capiMachineSet.Generation
						mapiMachineSet.Status.Conditions = []mapiv1beta1.Condition{{
							Type:               consts.SynchronizedCondition,
							LastTransitionTime: metav1.Now(),
							Status:             corev1.ConditionTrue,
						}}
					})).Should(Succeed())

					req = reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)}
				})

				It("should rollback to ClusterAPI, reset sync status, and remove paused annotation from CAPI resources", func() {
					_, err := reconciler.Reconcile(ctx, req)
					Expect(err).NotTo(HaveOccurred())

					Eventually(k.Object(mapiMachineSet)).Should(SatisfyAll(
						HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
						HaveField("Status.SynchronizedGeneration", BeZero()),
					))

					Eventually(k.Object(capiMachineSet)).ShouldNot(
						HaveField("ObjectMeta.Annotations", HaveKey(clusterv1.PausedAnnotation)))
				})
			})
		})
	})
})

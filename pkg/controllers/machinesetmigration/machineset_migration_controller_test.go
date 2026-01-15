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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/migrationcommon"
	migrationcontrollertest "github.com/openshift/cluster-capi-operator/pkg/controllers/migrationcommon/controllertest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("MachineSetMigration controller", func() {
	var (
		k          komega.Komega
		reconciler *MachineSetMigrationReconciler

		migrationControllerNamespace *corev1.Namespace
		capiNamespace                *corev1.Namespace
		mapiNamespace                *corev1.Namespace

		mapiMachineSetBuilder machinev1resourcebuilder.MachineSetBuilder
		mapiMachineSet        *mapiv1beta1.MachineSet
		capiMachineSetBuilder clusterv1resourcebuilder.MachineSetBuilder
		capiMachineSet        *clusterv1.MachineSet
		capaClusterBuilder    awsv1resourcebuilder.AWSClusterBuilder
		capiClusterBuilder    clusterv1resourcebuilder.ClusterBuilder
	)

	createCAPIMachineSet := func() {
		GinkgoHelper()

		capiMachineSet = capiMachineSetBuilder.Build()
		Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed(), "CAPI machine set should be able to be created")
	}

	updateMAPIMachineSetStatus := func(authority mapiv1beta1.MachineAuthority, synchronizedAPI mapiv1beta1.SynchronizedAPI, synchronizedGeneration int64, conditions ...mapiv1beta1.Condition) {
		GinkgoHelper()

		Eventually(k.UpdateStatus(mapiMachineSet, func() {
			mapiMachineSet.Status.AuthoritativeAPI = authority
			mapiMachineSet.Status.SynchronizedAPI = synchronizedAPI
			mapiMachineSet.Status.SynchronizedGeneration = synchronizedGeneration
			mapiMachineSet.Status.Conditions = conditions
		})).Should(Succeed())
	}

	updateCAPIMachineSetStatus := func(conditions ...metav1.Condition) {
		GinkgoHelper()

		Eventually(k.UpdateStatus(capiMachineSet, func() {
			capiMachineSet.Status.Conditions = conditions
		})).Should(Succeed())
	}

	reconcileOnce := func() (ctrl.Result, error) {
		GinkgoHelper()

		return reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachineSet)})
	}

	expectSyncStatusReset := func(authority mapiv1beta1.MachineAuthority) {
		GinkgoHelper()

		migrationcontrollertest.ExpectSyncStatusReset(k, mapiMachineSet, authority)
	}

	expectSuccessfulReconcile := func() {
		GinkgoHelper()

		migrationcontrollertest.ExpectSuccessfulReconcile(reconcileOnce)
	}

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

		capiMachineTemplate := clusterv1.MachineTemplateSpec{
			Spec: clusterv1.MachineSpec{
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: awsv1.GroupVersion.Group,
					Kind:     "AWSMachineTemplate",
					Name:     "machine-template",
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
		By("Cleaning up test resources")
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, mapiNamespace.GetName(),
			&mapiv1beta1.MachineSet{},
		)
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, capiNamespace.GetName(),
			&clusterv1.Cluster{},
			&clusterv1.MachineSet{},
			&awsv1.AWSCluster{},
			&awsv1.AWSMachineTemplate{},
		)
	})

	Describe("Reconcile", func() {
		Context("when no migration is requested and MachineAPI is authoritative", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				capiMachineSet = capiMachineSetBuilder.Build()
				Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachineSet.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should still repair pause drift on the CAPI MachineSet", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(capiMachineSet)).Should(
					HaveField("ObjectMeta.Annotations", HaveKeyWithValue(clusterv1.PausedAnnotation, "")),
				)
				Eventually(k.Object(mapiMachineSet)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
				)
			})
		})

		Context("when no migration is requested and ClusterAPI is authoritative", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				capiMachineSet = capiMachineSetBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					capiMachineSet.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should still repair unpause drift on the CAPI MachineSet", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(capiMachineSet)).ShouldNot(
					HaveField("ObjectMeta.Annotations", HaveKey(clusterv1.PausedAnnotation)),
				)
				Eventually(k.Object(mapiMachineSet)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
				)
			})
		})

		Context("when status.AuthoritativeAPI is empty", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())
			})

			It("should patch the status to match spec", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachineSet)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
				)
			})
		})

		Context("when migrating from MachineAPI to ClusterAPI and status.SynchronizedAPI is empty", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					"",
					mapiMachineSet.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should wait without acknowledging the migration", func() {
				current := &mapiv1beta1.MachineSet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachineSet), current)).To(Succeed())
				initialResourceVersion := current.ResourceVersion

				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachineSet)).Should(SatisfyAll(
					HaveField("ObjectMeta.ResourceVersion", Equal(initialResourceVersion)),
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
					HaveField("Status.SynchronizedAPI", BeEmpty()),
				))
			})
		})

		Context("when migrating from ClusterAPI to MachineAPI and status.SynchronizedAPI points at MachineAPI", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				capiMachineSet = capiMachineSetBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.MachineAPISynchronized,
					capiMachineSet.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should wait without entering Migrating", func() {
				current := &mapiv1beta1.MachineSet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachineSet), current)).To(Succeed())
				initialResourceVersion := current.ResourceVersion

				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachineSet)).Should(SatisfyAll(
					HaveField("ObjectMeta.ResourceVersion", Equal(initialResourceVersion)),
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
					HaveField("Status.SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
				))
			})
		})

		Context("when migrating from MachineAPI to ClusterAPI and the stable sync gate is satisfied", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachineSet.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should patch status to Migrating", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachineSet)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
				)
			})
		})

		Context("when spec.AuthoritativeAPI is ClusterAPI and status.AuthoritativeAPI is Migrating", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())
			})

			Context("when Machine API is not paused yet", func() {
				BeforeEach(func() {
					updateMAPIMachineSetStatus(
						mapiv1beta1.MachineAuthorityMigrating,
						mapiv1beta1.MachineAPISynchronized,
						mapiMachineSet.Generation,
						migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
						migrationcontrollertest.MAPIPausedCondition(corev1.ConditionFalse),
					)
				})

				It("should keep waiting in Migrating", func() {
					expectSuccessfulReconcile()

					Eventually(k.Object(mapiMachineSet)).Should(SatisfyAll(
						HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
						HaveField("Status.SynchronizedGeneration", Equal(mapiMachineSet.Generation)),
					))
				})
			})

			Context("when Machine API is paused", func() {
				BeforeEach(func() {
					capiMachineSet = capiMachineSetBuilder.
						WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
						Build()
					Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

					updateCAPIMachineSetStatus(migrationcontrollertest.CAPIPausedCondition(metav1.ConditionTrue))

					updateMAPIMachineSetStatus(
						mapiv1beta1.MachineAuthorityMigrating,
						mapiv1beta1.MachineAPISynchronized,
						mapiMachineSet.Generation,
						migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
						migrationcontrollertest.MAPIPausedCondition(corev1.ConditionTrue),
					)
				})

				It("should complete the switch to ClusterAPI and reset sync status", func() {
					expectSuccessfulReconcile()

					expectSyncStatusReset(mapiv1beta1.MachineAuthorityClusterAPI)
					Eventually(k.Object(capiMachineSet)).Should(
						HaveField("ObjectMeta.Annotations", HaveKeyWithValue(clusterv1.PausedAnnotation, "")),
					)
				})
			})
		})

		Context("when status.AuthoritativeAPI is Migrating with empty status.SynchronizedAPI and spec.AuthoritativeAPI is MachineAPI", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityMigrating,
					"",
					1,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should converge to MachineAPI without error", func() {
				expectSuccessfulReconcile()

				expectSyncStatusReset(mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})

		Context("when migrating from ClusterAPI to MachineAPI and the CAPI MachineSet is not paused yet", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				createCAPIMachineSet()

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					capiMachineSet.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should pause the CAPI MachineSet before entering Migrating", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(capiMachineSet)).Should(
					HaveField("ObjectMeta.Annotations", HaveKeyWithValue(clusterv1.PausedAnnotation, "")),
				)
				Eventually(k.Object(mapiMachineSet)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
				)
			})
		})

		Context("when migrating from ClusterAPI to MachineAPI and the CAPI MachineSet is missing", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					1,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should wait for the sync controller to restore the authoritative CAPI copy", func() {
				current := &mapiv1beta1.MachineSet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachineSet), current)).To(Succeed())
				initialResourceVersion := current.ResourceVersion

				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachineSet)).Should(SatisfyAll(
					HaveField("ObjectMeta.ResourceVersion", Equal(initialResourceVersion)),
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
					HaveField("Status.SynchronizedGeneration", Equal(int64(1))),
				))
			})
		})

		Context("when migrating from ClusterAPI to MachineAPI and only unrelated finalizers remain", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				capiMachineSet = capiMachineSetBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				capiMachineSet.Finalizers = append(capiMachineSet.Finalizers, "example.com/other-machineset-finalizer")
				Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

				updateCAPIMachineSetStatus(migrationcontrollertest.CAPIPausedCondition(metav1.ConditionFalse))

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					capiMachineSet.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should treat the CAPI side as safely paused and enter Migrating", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachineSet)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
				)
			})
		})

		Context("when migrating from ClusterAPI to MachineAPI and the MachineSet finalizer is still present", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				capiMachineSet = capiMachineSetBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				capiMachineSet.Finalizers = append(capiMachineSet.Finalizers, clusterv1.MachineSetFinalizer)
				Eventually(k8sClient.Create(ctx, capiMachineSet)).Should(Succeed())

				updateCAPIMachineSetStatus(migrationcontrollertest.CAPIPausedCondition(metav1.ConditionFalse))

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					capiMachineSet.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should wait for paused observation before entering Migrating", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachineSet)).Should(SatisfyAll(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
					HaveField("Status.SynchronizedGeneration", Equal(capiMachineSet.Generation)),
				))
			})
		})

		Context("when MachineAPI is authoritative and the CAPI MachineSet is missing", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachineSet.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should treat the missing CAPI MachineSet as already safely paused", func() {
				current := &mapiv1beta1.MachineSet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachineSet), current)).To(Succeed())
				initialResourceVersion := current.ResourceVersion

				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachineSet)).Should(SatisfyAll(
					HaveField("ObjectMeta.ResourceVersion", Equal(initialResourceVersion)),
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
				))
			})
		})

		Context("when ClusterAPI is authoritative and the CAPI MachineSet is missing", func() {
			BeforeEach(func() {
				mapiMachineSet = mapiMachineSetBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachineSet)).Should(Succeed())

				updateMAPIMachineSetStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					1,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should treat the missing CAPI MachineSet as already unpaused", func() {
				current := &mapiv1beta1.MachineSet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachineSet), current)).To(Succeed())
				initialResourceVersion := current.ResourceVersion

				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachineSet)).Should(SatisfyAll(
					HaveField("ObjectMeta.ResourceVersion", Equal(initialResourceVersion)),
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
				))
			})
		})
	})

	Describe("addPausedAnnotation", func() {
		Context("when the object has changed since it was read", func() {
			It("should fail with a conflict", func() {
				staleMachineSet := capiMachineSetBuilder.
					WithName("stale-machineset").
					Build()
				Expect(k8sClient.Create(ctx, staleMachineSet)).To(Succeed(), "machine set should be created")

				staleCopy := &clusterv1.MachineSet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(staleMachineSet), staleCopy)).To(Succeed(), "stale copy should be fetched")

				liveMachineSet := &clusterv1.MachineSet{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(staleMachineSet), liveMachineSet)).To(Succeed(), "live copy should be fetched")

				if liveMachineSet.Annotations == nil {
					liveMachineSet.Annotations = map[string]string{}
				}

				liveMachineSet.Annotations["test.openshift.io/stale"] = "true"
				Expect(k8sClient.Update(ctx, liveMachineSet)).To(Succeed(), "live machine set should be updated to make the stale copy outdated")

				changed, err := migrationcommon.AddPausedAnnotation(ctx, k8sClient, staleCopy)
				Expect(changed).To(BeFalse(), "stale writes should not report a successful change")
				Expect(err).To(HaveOccurred(), "stale writes should fail")
				Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected stale patch to fail with a conflict")
			})
		})
	})
})

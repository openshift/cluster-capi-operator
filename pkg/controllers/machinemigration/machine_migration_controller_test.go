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
	ctrl "sigs.k8s.io/controller-runtime"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	capiv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta2"
	capav1builder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"

	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/migrationcommon"
	migrationcontrollertest "github.com/openshift/cluster-capi-operator/pkg/controllers/migrationcommon/controllertest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("MachineMigration controller", func() {
	var (
		k          komega.Komega
		reconciler *MachineMigrationReconciler

		migrationControllerNamespace *corev1.Namespace
		capiNamespace                *corev1.Namespace
		mapiNamespace                *corev1.Namespace

		mapiMachineBuilder machinev1resourcebuilder.MachineBuilder
		mapiMachine        *mapiv1beta1.Machine
		capiMachineBuilder capiv1resourcebuilder.MachineBuilder
		capiMachine        *clusterv1.Machine
		capaMachine        *awsv1.AWSMachine
		capaMachineBuilder capav1builder.AWSMachineBuilder
		capaClusterBuilder capav1builder.AWSClusterBuilder
		capiClusterBuilder capiv1resourcebuilder.ClusterBuilder
		capiCluster        *clusterv1.Cluster
	)

	capaPausedCondition := func(status corev1.ConditionStatus) clusterv1beta1.Condition {
		return clusterv1beta1.Condition{
			Type:               clusterv1beta1.PausedV1Beta2Condition,
			Status:             status,
			LastTransitionTime: metav1.Now(),
		}
	}

	createCAPIMachinePair := func() {
		GinkgoHelper()

		capiMachine = capiMachineBuilder.Build()
		Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed(), "CAPI machine should be able to be created")

		capaMachine = capaMachineBuilder.Build()
		Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed(), "CAPI infra machine should be able to be created")
	}

	updateMAPIMachineStatus := func(authority mapiv1beta1.MachineAuthority, synchronizedAPI mapiv1beta1.SynchronizedAPI, synchronizedGeneration int64, conditions ...mapiv1beta1.Condition) {
		GinkgoHelper()

		Eventually(k.UpdateStatus(mapiMachine, func() {
			mapiMachine.Status.AuthoritativeAPI = authority
			mapiMachine.Status.SynchronizedAPI = synchronizedAPI
			mapiMachine.Status.SynchronizedGeneration = synchronizedGeneration
			mapiMachine.Status.Conditions = conditions
		})).Should(Succeed())
	}

	updateCAPIMachineStatus := func(conditions ...metav1.Condition) {
		GinkgoHelper()

		Eventually(k.UpdateStatus(capiMachine, func() {
			capiMachine.Status.Conditions = conditions
		})).Should(Succeed())
	}

	updateCAPAStatus := func(conditions ...clusterv1beta1.Condition) {
		GinkgoHelper()

		Eventually(k.UpdateStatus(capaMachine, func() {
			capaMachine.Status.Conditions = conditions
		})).Should(Succeed())
	}

	reconcileOnce := func() (ctrl.Result, error) {
		GinkgoHelper()

		return reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mapiMachine)})
	}

	expectSyncStatusReset := func(authority mapiv1beta1.MachineAuthority) {
		GinkgoHelper()

		migrationcontrollertest.ExpectSyncStatusReset(k, mapiMachine, authority)
	}

	expectSuccessfulReconcile := func() {
		GinkgoHelper()

		migrationcontrollertest.ExpectSuccessfulReconcile(reconcileOnce)
	}

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

		capaMachineRef := clusterv1.ContractVersionedObjectReference{
			APIGroup: awsv1.GroupVersion.Group,
			Kind:     "AWSMachine",
			Name:     "machine-template",
		}

		capiMachineBuilder = capiv1resourcebuilder.Machine().
			WithNamespace(capiNamespace.GetName()).
			WithInfrastructureRef(capaMachineRef).
			WithName("foo").
			WithClusterName(infrastructureName)

		reconciler = &MachineMigrationReconciler{
			Client:        k8sClient,
			Scheme:        testEnv.Scheme,
			Platform:      configv1.AWSPlatformType,
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
			&awsv1.AWSMachine{},
		)
	})

	Describe("Reconcile", func() {
		Context("when no migration is requested and MachineAPI is already authoritative", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachine.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should do nothing", func() {
				current := &mapiv1beta1.Machine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachine), current)).To(Succeed())
				initialResourceVersion := current.ResourceVersion

				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachine)).Should(
					HaveField("ObjectMeta.ResourceVersion", Equal(initialResourceVersion)),
				)
			})
		})

		Context("when status.AuthoritativeAPI is empty", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())
			})

			It("should patch the status to match spec", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachine)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
				)
			})
		})

		Context("when migrating from MachineAPI to ClusterAPI and the stable sync gate is not satisfied", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachine.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionFalse),
				)
			})

			It("should wait without acknowledging the migration", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachine)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
				)
			})
		})

		Context("when migrating from MachineAPI to ClusterAPI and status.SynchronizedAPI is empty", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					"",
					mapiMachine.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should wait without acknowledging the migration", func() {
				current := &mapiv1beta1.Machine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachine), current)).To(Succeed())
				initialResourceVersion := current.ResourceVersion

				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachine)).Should(SatisfyAll(
					HaveField("ObjectMeta.ResourceVersion", Equal(initialResourceVersion)),
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
					HaveField("Status.SynchronizedAPI", BeEmpty()),
				))
			})
		})

		Context("when migrating from MachineAPI to ClusterAPI and the stable sync gate is satisfied", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachine.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should patch status to Migrating", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachine)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
				)
			})
		})

		Context("when spec.AuthoritativeAPI is ClusterAPI and status.AuthoritativeAPI is Migrating", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())
			})

			Context("when Machine API is not paused yet", func() {
				BeforeEach(func() {
					updateMAPIMachineStatus(
						mapiv1beta1.MachineAuthorityMigrating,
						mapiv1beta1.MachineAPISynchronized,
						mapiMachine.Generation,
						migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
						migrationcontrollertest.MAPIPausedCondition(corev1.ConditionFalse),
					)
				})

				It("should keep waiting in Migrating", func() {
					expectSuccessfulReconcile()

					Eventually(k.Object(mapiMachine)).Should(SatisfyAll(
						HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
						HaveField("Status.SynchronizedGeneration", Equal(mapiMachine.Generation)),
					))
				})
			})

			Context("when Machine API is paused", func() {
				BeforeEach(func() {
					updateMAPIMachineStatus(
						mapiv1beta1.MachineAuthorityMigrating,
						mapiv1beta1.MachineAPISynchronized,
						mapiMachine.Generation,
						migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
						migrationcontrollertest.MAPIPausedCondition(corev1.ConditionTrue),
					)
				})

				It("should complete the switch to ClusterAPI and reset sync status", func() {
					expectSuccessfulReconcile()

					expectSyncStatusReset(mapiv1beta1.MachineAuthorityClusterAPI)
				})
			})
		})

		Context("when ClusterAPI is authoritative but the CAPI infra machine is still paused", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				capiMachine = capiMachineBuilder.Build()
				Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed())

				capaMachine = capaMachineBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed())

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					capiMachine.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should remove the paused annotation from the CAPI infra machine", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(capaMachine)).ShouldNot(
					HaveField("ObjectMeta.Annotations", HaveKey(clusterv1.PausedAnnotation)),
				)
				Eventually(k.Object(mapiMachine)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
				)
			})
		})

		Context("when migrating from ClusterAPI to MachineAPI and the CAPI pause request has not been written yet", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				createCAPIMachinePair()

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					capiMachine.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should pause the CAPI machine before entering Migrating", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(capiMachine)).Should(
					HaveField("ObjectMeta.Annotations", HaveKeyWithValue(clusterv1.PausedAnnotation, "")),
				)
				Eventually(k.Object(capaMachine)).ShouldNot(
					HaveField("ObjectMeta.Annotations", HaveKey(clusterv1.PausedAnnotation)),
				)
				Eventually(k.Object(mapiMachine)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
				)
			})
		})

		Context("when migrating from ClusterAPI to MachineAPI and status.SynchronizedAPI points at MachineAPI", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				capiMachine = capiMachineBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed())

				capaMachine = capaMachineBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed())

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.MachineAPISynchronized,
					capiMachine.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should wait without entering Migrating", func() {
				current := &mapiv1beta1.Machine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachine), current)).To(Succeed())
				initialResourceVersion := current.ResourceVersion

				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachine)).Should(SatisfyAll(
					HaveField("ObjectMeta.ResourceVersion", Equal(initialResourceVersion)),
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
					HaveField("Status.SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
				))
			})
		})

		Context("when MachineAPI is authoritative but the CAPI infra machine is not paused", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				capiMachine = capiMachineBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed())

				capaMachine = capaMachineBuilder.Build()
				Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed())

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachine.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should add the paused annotation to the CAPI infra machine", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(capaMachine)).Should(
					HaveField("ObjectMeta.Annotations", HaveKeyWithValue(clusterv1.PausedAnnotation, "")),
				)
				Eventually(k.Object(mapiMachine)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
				)
			})
		})

		Context("when MachineAPI is authoritative and the CAPI machine is missing", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachine.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should treat the missing CAPI machine as already safely paused", func() {
				current := &mapiv1beta1.Machine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachine), current)).To(Succeed())
				initialResourceVersion := current.ResourceVersion

				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachine)).Should(SatisfyAll(
					HaveField("ObjectMeta.ResourceVersion", Equal(initialResourceVersion)),
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
				))
			})
		})

		Context("when migrating from ClusterAPI to MachineAPI and only unrelated finalizers remain", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				capiMachine = capiMachineBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				capiMachine.Finalizers = append(capiMachine.Finalizers, "example.com/other-machine-finalizer")
				Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed())

				capaMachine = capaMachineBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				capaMachine.Finalizers = append(capaMachine.Finalizers, "example.com/other-infra-finalizer")
				Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed())

				updateCAPIMachineStatus(migrationcontrollertest.CAPIPausedCondition(metav1.ConditionFalse))
				updateCAPAStatus(capaPausedCondition(corev1.ConditionFalse))

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					capiMachine.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should treat the CAPI side as safely paused and enter Migrating", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachine)).Should(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
				)
			})
		})

		Context("when migrating from ClusterAPI to MachineAPI and controller finalizers are still present", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				capiMachine = capiMachineBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				capiMachine.Finalizers = append(capiMachine.Finalizers, clusterv1.MachineFinalizer)
				Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed())

				capaMachine = capaMachineBuilder.
					WithAnnotations(map[string]string{clusterv1.PausedAnnotation: ""}).
					Build()
				capaMachine.Finalizers = append(capaMachine.Finalizers, awsv1.MachineFinalizer)
				Eventually(k8sClient.Create(ctx, capaMachine)).Should(Succeed())

				updateCAPIMachineStatus(migrationcontrollertest.CAPIPausedCondition(metav1.ConditionFalse))
				updateCAPAStatus(capaPausedCondition(corev1.ConditionFalse))

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					capiMachine.Generation,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should wait for paused observation before entering Migrating", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachine)).Should(SatisfyAll(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
					HaveField("Status.SynchronizedGeneration", Equal(capiMachine.Generation)),
				))
			})
		})

		Context("when spec.AuthoritativeAPI is MachineAPI and status.AuthoritativeAPI is Migrating", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())
			})

			Context("when Cluster API objects exist and are not paused", func() {
				BeforeEach(func() {
					createCAPIMachinePair()

					updateMAPIMachineStatus(
						mapiv1beta1.MachineAuthorityMigrating,
						mapiv1beta1.MachineAPISynchronized,
						mapiMachine.Generation,
						migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
					)
				})

				It("should converge to MachineAPI without pausing the Cluster API objects", func() {
					expectSuccessfulReconcile()

					expectSyncStatusReset(mapiv1beta1.MachineAuthorityMachineAPI)
					Eventually(k.Object(capiMachine)).ShouldNot(
						HaveField("ObjectMeta.Annotations", HaveKey(clusterv1.PausedAnnotation)),
					)
					Eventually(k.Object(capaMachine)).ShouldNot(
						HaveField("ObjectMeta.Annotations", HaveKey(clusterv1.PausedAnnotation)),
					)
				})
			})

			Context("when status.SynchronizedAPI is empty", func() {
				BeforeEach(func() {
					updateMAPIMachineStatus(
						mapiv1beta1.MachineAuthorityMigrating,
						"",
						1,
						migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
					)
				})

				It("should still converge to MachineAPI", func() {
					expectSuccessfulReconcile()

					expectSyncStatusReset(mapiv1beta1.MachineAuthorityMachineAPI)
				})
			})
		})

		Context("when ClusterAPI is authoritative but the CAPI machine is missing", func() {
			BeforeEach(func() {
				mapiMachine = mapiMachineBuilder.
					WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
					Build()
				Eventually(k8sClient.Create(ctx, mapiMachine)).Should(Succeed())

				updateMAPIMachineStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					1,
					migrationcontrollertest.SynchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should wait for the sync controller to restore the authoritative CAPI copy", func() {
				expectSuccessfulReconcile()

				Eventually(k.Object(mapiMachine)).Should(SatisfyAll(
					HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)),
					HaveField("Status.SynchronizedGeneration", Equal(int64(1))),
				))
			})
		})
	})

	Describe("addPausedAnnotation", func() {
		Context("when the object has changed since it was read", func() {
			It("should fail with a conflict", func() {
				staleInfraMachine := capaMachineBuilder.
					WithName("stale-infra-machine").
					Build()
				Expect(k8sClient.Create(ctx, staleInfraMachine)).To(Succeed(), "infra machine should be created")

				staleCopy := &awsv1.AWSMachine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(staleInfraMachine), staleCopy)).To(Succeed(), "stale copy should be fetched")

				liveInfraMachine := &awsv1.AWSMachine{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(staleInfraMachine), liveInfraMachine)).To(Succeed(), "live copy should be fetched")

				if liveInfraMachine.Annotations == nil {
					liveInfraMachine.Annotations = map[string]string{}
				}

				liveInfraMachine.Annotations["test.openshift.io/stale"] = "true"
				Expect(k8sClient.Update(ctx, liveInfraMachine)).To(Succeed(), "live infra machine should be updated to make the stale copy outdated")

				changed, err := migrationcommon.AddPausedAnnotation(ctx, k8sClient, staleCopy)
				Expect(changed).To(BeFalse(), "stale writes should not report a successful change")
				Expect(err).To(HaveOccurred(), "stale writes should fail")
				Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected stale patch to fail with a conflict")
			})
		})
	})
})

/*
Copyright 2026 Red Hat, Inc.

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

package migrationcommon

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	capiv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta2"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	consts "github.com/openshift/cluster-capi-operator/pkg/controllers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

type fakeMachineMigratable struct {
	mapiMachine *mapiv1beta1.Machine
	desired     *mapiv1beta1.MachineAuthority

	ensurePausedResult   bool
	ensurePausedErr      error
	ensurePausedCalls    int
	ensureUnpausedResult bool
	ensureUnpausedErr    error
	ensureUnpausedCalls  int
}

func (f *fakeMachineMigratable) MAPIObject() client.Object {
	return f.mapiMachine
}

func (f *fakeMachineMigratable) DesiredAuthority() mapiv1beta1.MachineAuthority {
	if f.desired != nil {
		return *f.desired
	}

	return f.mapiMachine.Spec.AuthoritativeAPI
}

func (f *fakeMachineMigratable) CurrentAuthority() mapiv1beta1.MachineAuthority {
	return f.mapiMachine.Status.AuthoritativeAPI
}

func (f *fakeMachineMigratable) SynchronizedAPI() mapiv1beta1.SynchronizedAPI {
	return f.mapiMachine.Status.SynchronizedAPI
}

func (f *fakeMachineMigratable) SynchronizedGeneration() int64 {
	return f.mapiMachine.Status.SynchronizedGeneration
}

func (f *fakeMachineMigratable) MAPIConditions() []mapiv1beta1.Condition {
	return f.mapiMachine.Status.Conditions
}

func (f *fakeMachineMigratable) EnsureCAPIPaused(_ context.Context, _ *clusterv1.Machine) (bool, error) {
	f.ensurePausedCalls++
	return f.ensurePausedResult, f.ensurePausedErr
}

func (f *fakeMachineMigratable) EnsureCAPIUnpaused(_ context.Context, _ *clusterv1.Machine) (bool, error) {
	f.ensureUnpausedCalls++
	return f.ensureUnpausedResult, f.ensureUnpausedErr
}

var _ = Describe("Reconcile", func() {
	var (
		k              komega.Komega
		mapiNamespace  *corev1.Namespace
		capiNamespace  *corev1.Namespace
		mapiMachine    *mapiv1beta1.Machine
		capiMachine    *clusterv1.Machine
		mapiBuilder    machinev1resourcebuilder.MachineBuilder
		capiBuilder    capiv1resourcebuilder.MachineBuilder
		migratable     *fakeMachineMigratable
		controllerName string
	)

	synchronizedCondition := func(status corev1.ConditionStatus) mapiv1beta1.Condition {
		return mapiv1beta1.Condition{
			Type:               consts.SynchronizedCondition,
			Status:             status,
			LastTransitionTime: metav1.Now(),
		}
	}

	mapiPausedCondition := func(status corev1.ConditionStatus) mapiv1beta1.Condition {
		return mapiv1beta1.Condition{
			Type:               "Paused",
			Status:             status,
			LastTransitionTime: metav1.Now(),
		}
	}

	createCAPIMachine := func() {
		GinkgoHelper()

		capiMachine = capiBuilder.Build()
		Eventually(k8sClient.Create(ctx, capiMachine)).Should(Succeed(), "CAPI machine should be created for the test")
	}

	updateMAPIStatus := func(authority mapiv1beta1.MachineAuthority, synchronizedAPI mapiv1beta1.SynchronizedAPI, synchronizedGeneration int64, conditions ...mapiv1beta1.Condition) {
		GinkgoHelper()

		Eventually(k.UpdateStatus(mapiMachine, func() {
			mapiMachine.Status.AuthoritativeAPI = authority
			mapiMachine.Status.SynchronizedAPI = synchronizedAPI
			mapiMachine.Status.SynchronizedGeneration = synchronizedGeneration
			mapiMachine.Status.Conditions = conditions
		})).Should(Succeed(), "MAPI machine status should be updated for the test")
	}

	reconcileOnce := func() error {
		GinkgoHelper()

		_, err := Reconcile[*machinev1applyconfigs.MachineStatusApplyConfiguration](
			ctx,
			k8sClient,
			controllerName,
			capiNamespace.GetName(),
			machinev1applyconfigs.Machine,
			migratable,
		)

		return err
	}

	expectSyncStatusReset := func(authority mapiv1beta1.MachineAuthority) {
		GinkgoHelper()

		Eventually(k.Object(mapiMachine)).Should(SatisfyAll(
			HaveField("Status.AuthoritativeAPI", Equal(authority)),
			HaveField("Status.SynchronizedGeneration", BeZero()),
			HaveField("Status.Conditions", ContainElement(SatisfyAll(
				HaveField("Type", Equal(consts.SynchronizedCondition)),
				HaveField("Status", Equal(corev1.ConditionUnknown)),
				HaveField("Reason", Equal(consts.ReasonAuthoritativeAPIChanged)),
				HaveField("Message", Equal("Waiting for resync after change of AuthoritativeAPI")),
				HaveField("Severity", Equal(mapiv1beta1.ConditionSeverityInfo)),
			))),
		))
	}

	BeforeEach(func() {
		mapiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("migrationcommon-mapi-").Build()
		Expect(k8sClient.Create(ctx, mapiNamespace)).To(Succeed(), "MAPI namespace should be created")

		capiNamespace = corev1resourcebuilder.Namespace().
			WithGenerateName("migrationcommon-capi-").Build()
		Expect(k8sClient.Create(ctx, capiNamespace)).To(Succeed(), "CAPI namespace should be created")

		mapiBuilder = machinev1resourcebuilder.Machine().
			WithNamespace(mapiNamespace.GetName()).
			WithName("foo")
		capiBuilder = capiv1resourcebuilder.Machine().
			WithNamespace(capiNamespace.GetName()).
			WithName("foo")

		controllerName = "MigrationCommonTestController"
		k = komega.New(k8sClient)
	})

	AfterEach(func() {
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, mapiNamespace.GetName(),
			&mapiv1beta1.Machine{},
		)
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, capiNamespace.GetName(),
			&clusterv1.Machine{},
		)
	})

	Context("when status.authoritativeAPI is empty", func() {
		BeforeEach(func() {
			mapiMachine = mapiBuilder.
				WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
				Build()
			Expect(k8sClient.Create(ctx, mapiMachine)).To(Succeed(), "MAPI machine should be created")

			migratable = &fakeMachineMigratable{mapiMachine: mapiMachine}
		})

		It("should initialize status.authoritativeAPI from spec and stop", func() {
			Expect(reconcileOnce()).To(Succeed())

			Eventually(k.Object(mapiMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))
			Expect(migratable.ensurePausedCalls).To(BeZero(), "expected no pause reconciliation during status initialization")
			Expect(migratable.ensureUnpausedCalls).To(BeZero(), "expected no unpause reconciliation during status initialization")
		})

		Context("when spec.authoritativeAPI is not a supported stable target", func() {
			BeforeEach(func() {
				desiredAuthority := mapiv1beta1.MachineAuthorityMigrating
				migratable.desired = &desiredAuthority
			})

			It("should return an error without seeding status.authoritativeAPI", func() {
				Expect(reconcileOnce()).To(MatchError(ContainSubstring("unable to determine desired migration direction")))

				Consistently(func(g Gomega) {
					current := &mapiv1beta1.Machine{}
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(mapiMachine), current)).To(Succeed())
					g.Expect(current.Status.AuthoritativeAPI).To(BeEmpty(), "status.authoritativeAPI should remain unset for an unsupported desired authority")
				}).Should(Succeed())
				Expect(migratable.ensurePausedCalls).To(BeZero(), "expected no pause reconciliation during status initialization")
				Expect(migratable.ensureUnpausedCalls).To(BeZero(), "expected no unpause reconciliation during status initialization")
			})
		})
	})

	Context("when migrating to ClusterAPI", func() {
		BeforeEach(func() {
			mapiMachine = mapiBuilder.
				WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityClusterAPI).
				Build()
			Expect(k8sClient.Create(ctx, mapiMachine)).To(Succeed(), "MAPI machine should be created")

			migratable = &fakeMachineMigratable{
				mapiMachine:          mapiMachine,
				ensureUnpausedResult: true,
				ensurePausedResult:   true,
				ensurePausedCalls:    0,
				ensureUnpausedCalls:  0,
			}
		})

		Context("when the Machine API side is not yet synchronized", func() {
			BeforeEach(func() {
				updateMAPIStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachine.Generation,
					synchronizedCondition(corev1.ConditionFalse),
				)
			})

			It("should wait without acknowledging the migration", func() {
				Expect(reconcileOnce()).To(Succeed())

				Eventually(k.Object(mapiMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)))
			})
		})

		Context("when the Machine API side is synchronized", func() {
			BeforeEach(func() {
				updateMAPIStatus(
					mapiv1beta1.MachineAuthorityMachineAPI,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachine.Generation,
					synchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should set status.authoritativeAPI to Migrating", func() {
				Expect(reconcileOnce()).To(Succeed())

				Eventually(k.Object(mapiMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)))
			})
		})

		Context("when already in Migrating and Machine API is not paused", func() {
			BeforeEach(func() {
				updateMAPIStatus(
					mapiv1beta1.MachineAuthorityMigrating,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachine.Generation,
					synchronizedCondition(corev1.ConditionTrue),
					mapiPausedCondition(corev1.ConditionFalse),
				)
			})

			It("should keep waiting in Migrating", func() {
				Expect(reconcileOnce()).To(Succeed())

				Eventually(k.Object(mapiMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)))
			})
		})

		Context("when already in Migrating and Machine API is paused", func() {
			BeforeEach(func() {
				updateMAPIStatus(
					mapiv1beta1.MachineAuthorityMigrating,
					mapiv1beta1.MachineAPISynchronized,
					mapiMachine.Generation,
					synchronizedCondition(corev1.ConditionTrue),
					mapiPausedCondition(corev1.ConditionTrue),
				)
			})

			It("should acknowledge ClusterAPI and reset sync status", func() {
				Expect(reconcileOnce()).To(Succeed())

				expectSyncStatusReset(mapiv1beta1.MachineAuthorityClusterAPI)
			})
		})

		Context("when ClusterAPI is already authoritative and the primary CAPI object is missing", func() {
			BeforeEach(func() {
				updateMAPIStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					1,
					synchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should treat the missing primary object as already safe", func() {
				Expect(reconcileOnce()).To(Succeed())

				Eventually(k.Object(mapiMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))
				Expect(migratable.ensureUnpausedCalls).To(BeZero(), "expected no unpause call when the primary CAPI object is missing")
			})
		})
	})

	Context("when migrating to MachineAPI", func() {
		BeforeEach(func() {
			mapiMachine = mapiBuilder.
				WithAuthoritativeAPI(mapiv1beta1.MachineAuthorityMachineAPI).
				Build()
			Expect(k8sClient.Create(ctx, mapiMachine)).To(Succeed(), "MAPI machine should be created")

			migratable = &fakeMachineMigratable{
				mapiMachine:          mapiMachine,
				ensurePausedResult:   true,
				ensureUnpausedResult: true,
			}
		})

		Context("when the authoritative CAPI copy is missing", func() {
			BeforeEach(func() {
				updateMAPIStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					1,
					synchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should wait for the sync controller to restore it", func() {
				Expect(reconcileOnce()).To(Succeed())

				Eventually(k.Object(mapiMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))
				Expect(migratable.ensurePausedCalls).To(BeZero(), "expected no pause call when the primary CAPI object is missing")
			})
		})

		Context("when the primary CAPI object is not yet paused", func() {
			BeforeEach(func() {
				createCAPIMachine()
				updateMAPIStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					capiMachine.Generation,
					synchronizedCondition(corev1.ConditionTrue),
				)

				migratable.ensurePausedResult = false
			})

			It("should wait without entering Migrating", func() {
				Expect(reconcileOnce()).To(Succeed())

				Eventually(k.Object(mapiMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))
				Expect(migratable.ensurePausedCalls).To(Equal(1), "expected a pause attempt against the primary CAPI object")
			})
		})

		Context("when the primary CAPI object is paused but not yet synchronized", func() {
			BeforeEach(func() {
				createCAPIMachine()
				updateMAPIStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					capiMachine.Generation+1,
					synchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should wait without entering Migrating", func() {
				Expect(reconcileOnce()).To(Succeed())

				Eventually(k.Object(mapiMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityClusterAPI)))
				Expect(migratable.ensurePausedCalls).To(Equal(1), "expected a pause attempt before the stable sync gate")
			})
		})

		Context("when the primary CAPI object is paused and synchronized", func() {
			BeforeEach(func() {
				createCAPIMachine()
				updateMAPIStatus(
					mapiv1beta1.MachineAuthorityClusterAPI,
					mapiv1beta1.ClusterAPISynchronized,
					capiMachine.Generation,
					synchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should set status.authoritativeAPI to Migrating", func() {
				Expect(reconcileOnce()).To(Succeed())

				Eventually(k.Object(mapiMachine)).Should(HaveField("Status.AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)))
				Expect(migratable.ensurePausedCalls).To(Equal(1), "expected a pause attempt before acknowledging the migration")
			})
		})

		Context("when already in Migrating", func() {
			BeforeEach(func() {
				updateMAPIStatus(
					mapiv1beta1.MachineAuthorityMigrating,
					mapiv1beta1.ClusterAPISynchronized,
					1,
					synchronizedCondition(corev1.ConditionTrue),
				)
			})

			It("should acknowledge MachineAPI and reset sync status", func() {
				Expect(reconcileOnce()).To(Succeed())

				expectSyncStatusReset(mapiv1beta1.MachineAuthorityMachineAPI)
			})
		})
	})
})

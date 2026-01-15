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

package synccommon

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ApplySyncStatus", func() {
	const (
		migrationControllerName = "MachineSetMigrationController"
		syncControllerName      = "MachineSetSyncController"
		successMessage          = "Successfully synchronized MAPI MachineSet to CAPI"
	)

	var (
		namespace       *corev1.Namespace
		machineSet      *mapiv1beta1.MachineSet
		machineSetKey   client.ObjectKey
		staleMachineSet *mapiv1beta1.MachineSet
	)

	BeforeEach(func() {
		By("Creating a namespace and Machine API machine set")

		namespace = corev1resourcebuilder.Namespace().
			WithGenerateName("synccommon-syncstatus-").
			Build()
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

		machineSet = machinev1resourcebuilder.MachineSet().
			WithNamespace(namespace.Name).
			WithName("machine-set").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil)).
			Build()
		Expect(k8sClient.Create(ctx, machineSet)).To(Succeed())

		machineSetKey = client.ObjectKeyFromObject(machineSet)

		By("Setting status.AuthoritativeAPI to MachineAPI through the migration helper")

		Expect(ApplyMigrationStatus[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](
			ctx,
			k8sClient,
			migrationControllerName,
			machinev1applyconfigs.MachineSet,
			machineSet,
			mapiv1beta1.MachineAuthorityMachineAPI,
		)).To(Succeed())

		Expect(k8sClient.Get(ctx, machineSetKey, machineSet)).To(Succeed())

		By("Recording synchronized status through the sync helper")

		Expect(ApplySyncStatus[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](
			ctx,
			k8sClient,
			syncControllerName,
			machinev1applyconfigs.MachineSet,
			machineSet,
			corev1.ConditionTrue,
			controllers.ReasonResourceSynchronized,
			successMessage,
			&machineSet.Generation,
			AuthoritativeAPIToSynchronizedAPI(mapiv1beta1.MachineAuthorityMachineAPI),
		)).To(Succeed())

		Expect(k8sClient.Get(ctx, machineSetKey, machineSet)).To(Succeed())
		Expect(machineSet.Status).To(SatisfyAll(
			HaveField("AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
			HaveField("SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
		))

		staleMachineSet = machineSet.DeepCopy()

		By("Switching status.AuthoritativeAPI to Migrating through the migration helper")

		Expect(ApplyMigrationStatus[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](
			ctx,
			k8sClient,
			migrationControllerName,
			machinev1applyconfigs.MachineSet,
			machineSet,
			mapiv1beta1.MachineAuthorityMigrating,
		)).To(Succeed())
	})

	AfterEach(func() {
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, namespace.Name,
			&mapiv1beta1.MachineSet{},
		)
	})

	Context("when reapplying sync status from a fresh post-migration object", func() {
		It("should preserve synchronizedAPI", func() {
			By("Fetching the MachineSet again after migration was acknowledged")

			freshMachineSet := &mapiv1beta1.MachineSet{}
			Expect(k8sClient.Get(ctx, machineSetKey, freshMachineSet)).To(Succeed())
			Expect(freshMachineSet.Status).To(SatisfyAll(
				HaveField("AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
				HaveField("SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
			))

			By("Reapplying sync status with the sync controller field owner")

			Expect(ApplySyncStatus[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](
				ctx,
				k8sClient,
				syncControllerName,
				machinev1applyconfigs.MachineSet,
				freshMachineSet,
				corev1.ConditionTrue,
				controllers.ReasonResourceSynchronized,
				successMessage,
				&freshMachineSet.Generation,
				AuthoritativeAPIToSynchronizedAPI(freshMachineSet.Status.AuthoritativeAPI),
			)).To(Succeed())

			updatedMachineSet := &mapiv1beta1.MachineSet{}
			Expect(k8sClient.Get(ctx, machineSetKey, updatedMachineSet)).To(Succeed())
			Expect(updatedMachineSet.Status).To(SatisfyAll(
				HaveField("AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
				HaveField("SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
			))
		})
	})

	Context("when reapplying sync status from a stale pre-migration object", func() {
		It("should fail with a conflict and preserve synchronizedAPI", func() {
			By("Verifying the stale object still reflects the pre-migration state")

			Expect(staleMachineSet.Status).To(SatisfyAll(
				HaveField("AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
				HaveField("SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
			))

			By("Reapplying sync status with the stale resourceVersion")

			err := ApplySyncStatus[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](
				ctx,
				k8sClient,
				syncControllerName,
				machinev1applyconfigs.MachineSet,
				staleMachineSet,
				corev1.ConditionTrue,
				controllers.ReasonResourceSynchronized,
				successMessage,
				&staleMachineSet.Generation,
				AuthoritativeAPIToSynchronizedAPI(staleMachineSet.Status.AuthoritativeAPI),
			)
			Expect(err).To(SatisfyAll(
				HaveOccurred(),
				WithTransform(apierrors.IsConflict, BeTrue()),
			))

			updatedMachineSet := &mapiv1beta1.MachineSet{}
			Expect(k8sClient.Get(ctx, machineSetKey, updatedMachineSet)).To(Succeed())
			Expect(updatedMachineSet.Status).To(SatisfyAll(
				HaveField("AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
				HaveField("SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
			))
		})
	})
})

var _ = Describe("ApplySyncStatus for Machine", func() {
	const (
		machineMigrationControllerName = "MachineMigrationController"
		machineSyncControllerName      = "MachineSyncController"
		machineSuccessMessage          = "Successfully synchronized MAPI Machine to CAPI"
	)

	var (
		namespace  *corev1.Namespace
		machine    *mapiv1beta1.Machine
		machineKey client.ObjectKey
	)

	BeforeEach(func() {
		By("Creating a namespace and Machine API machine")

		namespace = corev1resourcebuilder.Namespace().
			WithGenerateName("synccommon-machine-syncstatus-").
			Build()
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

		machine = machinev1resourcebuilder.Machine().
			WithNamespace(namespace.Name).
			WithName("machine").
			WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil)).
			Build()
		Expect(k8sClient.Create(ctx, machine)).To(Succeed())

		machineKey = client.ObjectKeyFromObject(machine)

		By("Setting status.AuthoritativeAPI to MachineAPI through the migration helper")

		Expect(ApplyMigrationStatus[*machinev1applyconfigs.MachineStatusApplyConfiguration](
			ctx,
			k8sClient,
			machineMigrationControllerName,
			machinev1applyconfigs.Machine,
			machine,
			mapiv1beta1.MachineAuthorityMachineAPI,
		)).To(Succeed())

		Expect(k8sClient.Get(ctx, machineKey, machine)).To(Succeed())

		By("Recording synchronized status through the sync helper")

		Expect(ApplySyncStatus[*machinev1applyconfigs.MachineStatusApplyConfiguration](
			ctx,
			k8sClient,
			machineSyncControllerName,
			machinev1applyconfigs.Machine,
			machine,
			corev1.ConditionTrue,
			controllers.ReasonResourceSynchronized,
			machineSuccessMessage,
			&machine.Generation,
			AuthoritativeAPIToSynchronizedAPI(mapiv1beta1.MachineAuthorityMachineAPI),
		)).To(Succeed())

		Expect(k8sClient.Get(ctx, machineKey, machine)).To(Succeed())
		Expect(machine.Status).To(SatisfyAll(
			HaveField("AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMachineAPI)),
			HaveField("SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
		))

		By("Switching status.AuthoritativeAPI to Migrating through the migration helper")

		Expect(ApplyMigrationStatus[*machinev1applyconfigs.MachineStatusApplyConfiguration](
			ctx,
			k8sClient,
			machineMigrationControllerName,
			machinev1applyconfigs.Machine,
			machine,
			mapiv1beta1.MachineAuthorityMigrating,
		)).To(Succeed())
	})

	AfterEach(func() {
		testutils.CleanupResources(Default, ctx, cfg, k8sClient, namespace.Name,
			&mapiv1beta1.Machine{},
		)
	})

	Context("when reapplying sync status from a fresh post-migration object", func() {
		It("should preserve synchronizedAPI", func() {
			By("Fetching the Machine again after migration was acknowledged")

			freshMachine := &mapiv1beta1.Machine{}
			Expect(k8sClient.Get(ctx, machineKey, freshMachine)).To(Succeed())
			Expect(freshMachine.Status).To(SatisfyAll(
				HaveField("AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
				HaveField("SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
			))

			By("Reapplying sync status with the sync controller field owner")

			Expect(ApplySyncStatus[*machinev1applyconfigs.MachineStatusApplyConfiguration](
				ctx,
				k8sClient,
				machineSyncControllerName,
				machinev1applyconfigs.Machine,
				freshMachine,
				corev1.ConditionTrue,
				controllers.ReasonResourceSynchronized,
				machineSuccessMessage,
				&freshMachine.Generation,
				AuthoritativeAPIToSynchronizedAPI(freshMachine.Status.AuthoritativeAPI),
			)).To(Succeed())

			updatedMachine := &mapiv1beta1.Machine{}
			Expect(k8sClient.Get(ctx, machineKey, updatedMachine)).To(Succeed())
			Expect(updatedMachine.Status).To(SatisfyAll(
				HaveField("AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
				HaveField("SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
			))
		})
	})
})

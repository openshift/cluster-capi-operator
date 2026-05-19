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

package synccommon

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	machinev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Migration status helpers", func() {
	Describe("ApplyMigrationStatus", func() {
		It("should preserve synchronizedAPI when setting a machine to Migrating", func() {
			By("Creating a namespace and Machine API machine")

			namespace := corev1resourcebuilder.Namespace().
				WithGenerateName("synccommon-machine-").
				Build()
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			machine := machinev1resourcebuilder.Machine().
				WithNamespace(namespace.Name).
				WithName("machine").
				WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil)).
				Build()
			Expect(k8sClient.Create(ctx, machine)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())

			By("Recording synchronized status through the sync helper")

			Expect(ApplySyncStatus[*machinev1applyconfigs.MachineStatusApplyConfiguration](
				ctx,
				k8sClient,
				"machine-sync-controller",
				machinev1applyconfigs.Machine,
				machine,
				corev1.ConditionTrue,
				controllers.ReasonResourceSynchronized,
				"Successfully synchronized MAPI Machine to CAPI",
				&machine.Generation,
				AuthoritativeAPIToSynchronizedAPI(mapiv1beta1.MachineAuthorityMachineAPI),
			)).To(Succeed())

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), machine)).To(Succeed())
			Expect(machine.Status.SynchronizedAPI).To(Equal(mapiv1beta1.MachineAPISynchronized))

			By("Setting status.AuthoritativeAPI to Migrating through the migration helper")

			Expect(ApplyMigrationStatus[*machinev1applyconfigs.MachineStatusApplyConfiguration](
				ctx,
				k8sClient,
				"machine-migration-controller",
				machinev1applyconfigs.Machine,
				machine,
				mapiv1beta1.MachineAuthorityMigrating,
			)).To(Succeed())

			updatedMachine := &mapiv1beta1.Machine{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machine), updatedMachine)).To(Succeed())
			Expect(updatedMachine.Status).To(SatisfyAll(
				HaveField("AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
				HaveField("SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
			))
		})

		It("should preserve synchronizedAPI when setting a machine set to Migrating", func() {
			By("Creating a namespace and Machine API machine set")

			namespace := corev1resourcebuilder.Namespace().
				WithGenerateName("synccommon-machineset-").
				Build()
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			machineSet := machinev1resourcebuilder.MachineSet().
				WithNamespace(namespace.Name).
				WithName("machine-set").
				WithProviderSpecBuilder(machinev1resourcebuilder.AWSProviderSpec().WithLoadBalancers(nil)).
				Build()
			Expect(k8sClient.Create(ctx, machineSet)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machineSet), machineSet)).To(Succeed())

			By("Recording synchronized status through the sync helper")

			Expect(ApplySyncStatus[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](
				ctx,
				k8sClient,
				"machineset-sync-controller",
				machinev1applyconfigs.MachineSet,
				machineSet,
				corev1.ConditionTrue,
				controllers.ReasonResourceSynchronized,
				"Successfully synchronized MAPI MachineSet to CAPI",
				&machineSet.Generation,
				AuthoritativeAPIToSynchronizedAPI(mapiv1beta1.MachineAuthorityMachineAPI),
			)).To(Succeed())

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machineSet), machineSet)).To(Succeed())
			Expect(machineSet.Status.SynchronizedAPI).To(Equal(mapiv1beta1.MachineAPISynchronized))

			By("Setting status.AuthoritativeAPI to Migrating through the migration helper")

			Expect(ApplyMigrationStatus[*machinev1applyconfigs.MachineSetStatusApplyConfiguration](
				ctx,
				k8sClient,
				"machineset-migration-controller",
				machinev1applyconfigs.MachineSet,
				machineSet,
				mapiv1beta1.MachineAuthorityMigrating,
			)).To(Succeed())

			updatedMachineSet := &mapiv1beta1.MachineSet{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(machineSet), updatedMachineSet)).To(Succeed())
			Expect(updatedMachineSet.Status).To(SatisfyAll(
				HaveField("AuthoritativeAPI", Equal(mapiv1beta1.MachineAuthorityMigrating)),
				HaveField("SynchronizedAPI", Equal(mapiv1beta1.MachineAPISynchronized)),
			))
		})
	})

	Describe("ApplyMigrationStatusAndResetSyncStatus", func() {
		It("should reject unsupported Machine API object types before patching", func() {
			err := ApplyMigrationStatusAndResetSyncStatus[*machinev1applyconfigs.MachineStatusApplyConfiguration](
				ctx,
				nil,
				"machine-migration-controller",
				machinev1applyconfigs.Machine,
				&corev1.ConfigMap{},
				mapiv1beta1.MachineAuthorityClusterAPI,
			)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, errUnsupportedSyncStatusType)).To(BeTrue())
		})
	})
})

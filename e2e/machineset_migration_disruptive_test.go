// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration][Disruptive] MachineSet Migration Rollback Tests", Ordered, Serial, func() {
	var (
		disruptionState    machineSetMigrationDisruptionState
		awsMachineTemplate *awsv1.AWSMachineTemplate
		capiMachineSet     *clusterv1.MachineSet
		mapiMachineSet     *mapiv1beta1.MachineSet
	)

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateMachineAPIMigration) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}

		disruptionState = readAndValidateMachineSetMigrationDisruptionBaseline(ctx, cl)

		DeferCleanup(func() {
			By("Cleaning up MachineSet rollback test resources")
			cleanupMachineSetTestResources(
				ctx,
				cl,
				[]*clusterv1.MachineSet{capiMachineSet},
				[]*awsv1.AWSMachineTemplate{awsMachineTemplate},
				[]*mapiv1beta1.MachineSet{mapiMachineSet},
			)
		})

		DeferCleanup(func() {
			By("Restoring Deployment/openshift-cluster-api/capi-controller-manager to its original replica count")
			scaleDeploymentAndWaitForAvailableReplicas(
				ctx,
				cl,
				capiframework.CAPINamespace,
				capiControllerManagerDeploymentName,
				disruptionState.capiControllerManagerReplicas,
			)

			By("Restoring Deployment/openshift-cluster-api-operator/capi-operator to its original replica count")
			scaleDeploymentAndWaitForAvailableReplicas(
				ctx,
				cl,
				capiframework.CAPIOperatorNamespace,
				capiOperatorDeploymentName,
				disruptionState.capiOperatorReplicas,
			)

			if disruptionState.capiOperatorOverrideExpectedAbsent {
				By("Removing the targeted ClusterVersion unmanaged override for Deployment/openshift-cluster-api-operator/capi-operator")
				setMachineSetMigrationCAPIOperatorOverride(ctx, cl, false)
			}

			waitForClusterAPIOperatorHealthy(ctx, cl)
		})
	})

	It("should roll back a stalled zero-replica MachineSet migration while the target controller is down", func() {
		machineSetName := generateName("ms-disruptive-rollback-")

		mapiMachineSet = createMAPIMachineSetWithAuthoritativeAPI(
			ctx,
			cl,
			0,
			machineSetName,
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAuthorityMachineAPI,
		)
		capiMachineSet, awsMachineTemplate = waitForMAPIMachineSetMirrors(machineSetName)
		trackResource(awsMachineTemplate)

		By("Verifying the healthy zero-replica baseline before disruption")
		verifyMAPIMachineSetSynchronizedState(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAPISynchronized)
		verifyCAPIMachineSetPausedState(capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

		By("Marking Deployment/openshift-cluster-api-operator/capi-operator unmanaged through ClusterVersion overrides")
		setMachineSetMigrationCAPIOperatorOverride(ctx, cl, true)

		By("Scaling Deployment/openshift-cluster-api-operator/capi-operator to zero")
		scaleDeploymentAndWaitForAvailableReplicas(
			ctx,
			cl,
			capiframework.CAPIOperatorNamespace,
			capiOperatorDeploymentName,
			0,
		)

		By("Scaling Deployment/openshift-cluster-api/capi-controller-manager to zero")
		scaleDeploymentAndWaitForAvailableReplicas(
			ctx,
			cl,
			capiframework.CAPINamespace,
			capiControllerManagerDeploymentName,
			0,
		)

		By("Changing the MachineSet authoritativeAPI to ClusterAPI")
		switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)

		By("Verifying migration becomes stuck in Migrating while the Cluster API side is unavailable")
		verifyMachineSetAuthoritative(mapiMachineSet, mapiv1beta1.MachineAuthorityMigrating)

		By("Changing the MachineSet authoritativeAPI back to MachineAPI while the target controller is still down")
		switchMachineSetAuthoritativeAPI(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

		By("Verifying rollback completes before restoring the target controller")
		verifyMAPIMachineSetSynchronizedState(mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI, mapiv1beta1.MachineAPISynchronized)
		verifyCAPIMachineSetPausedState(capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

		By("Keeping Deployment/openshift-cluster-api/capi-controller-manager scaled to zero until rollback steady state is confirmed")
		waitForDeploymentAvailableReplicas(
			ctx,
			cl,
			capiframework.CAPINamespace,
			capiControllerManagerDeploymentName,
			0,
		)
	})
})

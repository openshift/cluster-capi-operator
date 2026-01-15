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
	"time"

	. "github.com/onsi/ginkgo/v2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

type machineSetMigrationDisruptiveFixture struct {
	awsMachineTemplate *awsv1.AWSMachineTemplate
	capiMachineSet     *clusterv1.MachineSet
	mapiMachineSet     *mapiv1beta1.MachineSet
}

func createZeroReplicaMachineSetMigrationDisruptiveFixture(machineSetNamePrefix string) machineSetMigrationDisruptiveFixture {
	GinkgoHelper()

	machineSetName := generateName(machineSetNamePrefix)
	mapiMachineSet := createMAPIMachineSetWithAuthoritativeAPI(
		ctx,
		cl,
		0,
		machineSetName,
		mapiv1beta1.MachineAuthorityMachineAPI,
		mapiv1beta1.MachineAuthorityMachineAPI,
	)
	capiMachineSet, awsMachineTemplate := waitForMAPIMachineSetMirrors(machineSetName)
	trackResource(awsMachineTemplate)

	return machineSetMigrationDisruptiveFixture{
		awsMachineTemplate: awsMachineTemplate,
		capiMachineSet:     capiMachineSet,
		mapiMachineSet:     mapiMachineSet,
	}
}

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:MachineAPIMigration][Disruptive] MachineSet Migration Outage Tests", Ordered, Serial, func() {
	var (
		disruptionState         *machineSetMigrationDisruptionState
		disruptionStateRestored bool
		fixtureA                machineSetMigrationDisruptiveFixture
		fixtureB                machineSetMigrationDisruptiveFixture
	)

	BeforeAll(func() {
		if platform != configv1.AWSPlatformType {
			Skip(fmt.Sprintf("Skipping tests on %s, this is only supported on AWS", platform))
		}

		if !capiframework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateMachineAPIMigration) {
			Skip("Skipping, this feature is only supported on MachineAPIMigration enabled clusters")
		}

		DeferCleanup(func() {
			By("Cleaning up MachineSet outage test resources")
			cleanupMachineSetTestResources(
				ctx,
				cl,
				[]*clusterv1.MachineSet{fixtureA.capiMachineSet, fixtureB.capiMachineSet},
				[]*awsv1.AWSMachineTemplate{fixtureA.awsMachineTemplate, fixtureB.awsMachineTemplate},
				[]*mapiv1beta1.MachineSet{fixtureA.mapiMachineSet, fixtureB.mapiMachineSet},
			)
		})

		DeferCleanup(func() {
			if disruptionState == nil || disruptionStateRestored {
				return
			}

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

	It("should reuse one outage to verify paused-target and unpaused-target rollback behavior", func() {
		By("Creating the paused-target rollback fixture before the outage")
		fixtureA = createZeroReplicaMachineSetMigrationDisruptiveFixture("ms-disruptive-paused-target-")
		verifyMAPIMachineSetSynchronizedState(
			fixtureA.mapiMachineSet,
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAPISynchronized,
		)
		verifyCAPIMachineSetPausedState(fixtureA.capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

		By("Creating the unpaused-target rollback fixture before the outage")
		fixtureB = createZeroReplicaMachineSetMigrationDisruptiveFixture("ms-disruptive-unpaused-target-")
		verifyMAPIMachineSetSynchronizedState(
			fixtureB.mapiMachineSet,
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAPISynchronized,
		)
		verifyCAPIMachineSetPausedState(fixtureB.capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

		By("Migrating the unpaused-target rollback fixture to a healthy ClusterAPI steady state before the outage")
		switchMachineSetAuthoritativeAPI(fixtureB.mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
		verifyMAPIMachineSetSynchronizedState(
			fixtureB.mapiMachineSet,
			mapiv1beta1.MachineAuthorityClusterAPI,
			mapiv1beta1.ClusterAPISynchronized,
		)
		verifyMachineSetPausedCondition(fixtureB.mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
		verifyCAPIMachineSetPausedState(fixtureB.capiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)

		By("Reading and validating the outage baseline after both fixtures are prepared")
		disruptionStateValue := readAndValidateMachineSetMigrationDisruptionBaseline(ctx, cl)
		disruptionState = &disruptionStateValue

		By("Marking Deployment/openshift-cluster-api-operator/capi-operator unmanaged through ClusterVersion overrides")
		setMachineSetMigrationCAPIOperatorOverride(ctx, cl, true)

		By("Creating the shared outage by scaling Deployment/openshift-cluster-api-operator/capi-operator to zero")
		scaleDeploymentAndWaitForAvailableReplicas(
			ctx,
			cl,
			capiframework.CAPIOperatorNamespace,
			capiOperatorDeploymentName,
			0,
		)

		By("Scaling Deployment/openshift-cluster-api/capi-controller-manager to zero in the same outage window")
		scaleDeploymentAndWaitForAvailableReplicas(
			ctx,
			cl,
			capiframework.CAPINamespace,
			capiControllerManagerDeploymentName,
			0,
		)

		By("Verifying rollback succeeds during the outage when the target CAPI MachineSet was never observed unpaused")
		switchMachineSetAuthoritativeAPI(fixtureA.mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
		verifyMachineSetAuthoritative(fixtureA.mapiMachineSet, mapiv1beta1.MachineAuthorityClusterAPI)
		switchMachineSetAuthoritativeAPI(fixtureA.mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
		verifyMAPIMachineSetSynchronizedState(
			fixtureA.mapiMachineSet,
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAPISynchronized,
		)
		verifyCAPIMachineSetPausedState(fixtureA.capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

		By("Requesting rollback during the outage after the target CAPI MachineSet was observed unpaused")
		switchMachineSetAuthoritativeAPI(fixtureB.mapiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)

		By("Verifying rollback stays pinned at ClusterAPI while the outage persists and the target CAPI MachineSet remains unpaused")
		consistentlyVerifyMachineSetRollbackPinnedAtClusterAPI(
			fixtureB.mapiMachineSet,
			fixtureB.capiMachineSet,
			10*time.Second,
		)

		By("Starting recovery for Deployment/openshift-cluster-api/capi-controller-manager and Deployment/openshift-cluster-api-operator/capi-operator together")
		scaleDeployment(
			ctx,
			cl,
			capiframework.CAPINamespace,
			capiControllerManagerDeploymentName,
			disruptionState.capiControllerManagerReplicas,
		)
		scaleDeployment(
			ctx,
			cl,
			capiframework.CAPIOperatorNamespace,
			capiOperatorDeploymentName,
			disruptionState.capiOperatorReplicas,
		)

		By("Waiting for Deployment/openshift-cluster-api/capi-controller-manager to become healthy after recovery starts")
		waitForDeploymentAvailableReplicas(
			ctx,
			cl,
			capiframework.CAPINamespace,
			capiControllerManagerDeploymentName,
			disruptionState.capiControllerManagerReplicas,
		)

		By("Waiting for Deployment/openshift-cluster-api-operator/capi-operator to become healthy after recovery starts")
		waitForDeploymentAvailableReplicas(
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

		By("Waiting for the Cluster API operator to return to a healthy state after the outage")
		waitForClusterAPIOperatorHealthy(ctx, cl)
		disruptionStateRestored = true

		By("Verifying the already-requested rollback resumes automatically after recovery")
		verifyMAPIMachineSetSynchronizedState(
			fixtureB.mapiMachineSet,
			mapiv1beta1.MachineAuthorityMachineAPI,
			mapiv1beta1.MachineAPISynchronized,
		)
		verifyCAPIMachineSetPausedState(fixtureB.capiMachineSet, mapiv1beta1.MachineAuthorityMachineAPI)
	})
})

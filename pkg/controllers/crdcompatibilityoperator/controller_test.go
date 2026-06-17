// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package crdcompatibilityoperator

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

const (
	timeout  = 10 * time.Second
	interval = 100 * time.Millisecond
)

var _ = Describe("CRDCompatibilityOperatorController", Ordered, func() {
	var (
		infraObj      *configv1.Infrastructure
		testCtx       context.Context
		testCtxCancel context.CancelFunc
		deploymentKey types.NamespacedName
		pdbKey        types.NamespacedName
		cfg           *rest.Config
		cl            client.Client
		testEnv       *envtest.Environment
	)

	BeforeAll(func() {
		testCtx, testCtxCancel = context.WithCancel(ctx)

		By("Starting fresh test environment")

		testEnv = &envtest.Environment{}

		var err error

		cfg, cl, err = test.StartEnvTest(testEnv)
		Expect(err).NotTo(HaveOccurred(), "failed to start test environment")

		By("Creating test namespace")

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		}
		Expect(cl.Create(testCtx, ns)).To(Succeed(), "failed to create test namespace")

		By("Creating Infrastructure CR")

		infraObj = &configv1.Infrastructure{
			ObjectMeta: metav1.ObjectMeta{
				Name: controllers.InfrastructureResourceName,
			},
			Spec: configv1.InfrastructureSpec{},
			Status: configv1.InfrastructureStatus{
				ControlPlaneTopology: configv1.HighlyAvailableTopologyMode,
			},
		}
		Expect(cl.Create(testCtx, infraObj)).To(Succeed(), "failed to create Infrastructure CR")

		deploymentKey = types.NamespacedName{
			Name:      operandDeploymentName,
			Namespace: testNamespace,
		}
		pdbKey = types.NamespacedName{
			Name:      operandPDBName,
			Namespace: testNamespace,
		}

		By("Starting controller manager")

		_, err = startManager(testCtx, cfg, cl)
		Expect(err).NotTo(HaveOccurred(), "failed to start controller manager")
	})

	AfterAll(func() {
		By("Tearing down test environment")
		testCtxCancel()
		Expect(testEnv.Stop()).To(Succeed(), "failed to stop test environment")
	})

	Context("when cluster topology is HighlyAvailable", func() {
		It("should create Deployment with 2 replicas", func() {
			deployment := &appsv1.Deployment{}

			Eventually(func() error {
				return cl.Get(testCtx, deploymentKey, deployment)
			}, timeout, interval).Should(Succeed(), "Deployment should be created")

			Expect(deployment.Spec.Replicas).NotTo(BeNil(), "Deployment replicas should not be nil")
			Expect(*deployment.Spec.Replicas).To(Equal(int32(2)), "Deployment should have 2 replicas")
			Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1), "Deployment should have exactly 1 container")
			Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal(testOperandImage), "Container image should match test image")
			Expect(deployment.Spec.Template.Spec.Containers[0].Command).To(Equal([]string{"./crd-compatibility-checker"}), "Container command should be crd-compatibility-checker")
			Expect(deployment.Spec.Template.Labels["k8s-app"]).To(Equal(operandLabel), "Pod template should have correct k8s-app label")
		})

		It("should create PodDisruptionBudget", func() {
			pdb := &policyv1.PodDisruptionBudget{}

			Eventually(func() error {
				return cl.Get(testCtx, pdbKey, pdb)
			}, timeout, interval).Should(Succeed(), "PDB should be created")

			Expect(pdb.Spec.MinAvailable).NotTo(BeNil(), "PDB minAvailable should not be nil")
			Expect(pdb.Spec.MinAvailable.IntVal).To(Equal(int32(1)), "PDB should have minAvailable=1")
			Expect(pdb.Spec.Selector.MatchLabels["k8s-app"]).To(Equal(operandLabel), "PDB selector should match operand label")
		})
	})

	Context("when cluster topology changes to SingleReplica", func() {
		BeforeEach(func() {
			By("Updating Infrastructure to SingleReplica topology")
			Eventually(func() error {
				if err := cl.Get(testCtx, types.NamespacedName{Name: controllers.InfrastructureResourceName}, infraObj); err != nil {
					return err
				}

				infraObj.Status.ControlPlaneTopology = configv1.SingleReplicaTopologyMode

				return cl.Status().Update(testCtx, infraObj)
			}, timeout, interval).Should(Succeed(), "Infrastructure topology should be updated to SingleReplica")
		})

		It("should update Deployment to 1 replica", func() {
			deployment := &appsv1.Deployment{}

			Eventually(func() int32 {
				if err := cl.Get(testCtx, deploymentKey, deployment); err != nil {
					return -1
				}

				if deployment.Spec.Replicas == nil {
					return -1
				}

				return *deployment.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(1)), "Deployment should be scaled down to 1 replica")
		})

		It("should delete PodDisruptionBudget", func() {
			pdb := &policyv1.PodDisruptionBudget{}

			Eventually(func() bool {
				err := cl.Get(testCtx, pdbKey, pdb)
				return apierrors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue(), "PDB should be deleted")
		})
	})

	Context("when cluster topology changes back to HighlyAvailable", func() {
		BeforeEach(func() {
			By("Updating Infrastructure to HighlyAvailable topology")
			Eventually(func() error {
				if err := cl.Get(testCtx, types.NamespacedName{Name: controllers.InfrastructureResourceName}, infraObj); err != nil {
					return err
				}

				infraObj.Status.ControlPlaneTopology = configv1.HighlyAvailableTopologyMode

				return cl.Status().Update(testCtx, infraObj)
			}, timeout, interval).Should(Succeed(), "Infrastructure topology should be updated to HighlyAvailable")
		})

		It("should update Deployment to 2 replicas", func() {
			deployment := &appsv1.Deployment{}

			Eventually(func() int32 {
				if err := cl.Get(testCtx, deploymentKey, deployment); err != nil {
					return -1
				}

				if deployment.Spec.Replicas == nil {
					return -1
				}

				return *deployment.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(2)), "Deployment should be scaled up to 2 replicas")
		})

		It("should recreate PodDisruptionBudget", func() {
			pdb := &policyv1.PodDisruptionBudget{}

			Eventually(func() error {
				return cl.Get(testCtx, pdbKey, pdb)
			}, timeout, interval).Should(Succeed(), "PDB should be recreated")

			Expect(pdb.Spec.MinAvailable).NotTo(BeNil(), "PDB minAvailable should not be nil")
			Expect(pdb.Spec.MinAvailable.IntVal).To(Equal(int32(1)), "PDB should have minAvailable=1")
		})
	})

	Context("when Deployment is manually modified", func() {
		It("should correct drift in replica count", func() {
			By("Manually setting replicas to 5")

			deployment := &appsv1.Deployment{}

			Eventually(func() error {
				if err := cl.Get(testCtx, deploymentKey, deployment); err != nil {
					return err
				}

				wrongReplicas := int32(5)
				deployment.Spec.Replicas = &wrongReplicas

				return cl.Update(testCtx, deployment)
			}, timeout, interval).Should(Succeed(), "Deployment should be manually updated to 5 replicas")

			By("Verifying controller corrects replicas back to 2")
			Eventually(func() int32 {
				if err := cl.Get(testCtx, deploymentKey, deployment); err != nil {
					return -1
				}

				if deployment.Spec.Replicas == nil {
					return -1
				}

				return *deployment.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(2)), "Controller should correct replicas back to 2")
		})
	})

	Context("when PDB is manually deleted in HA mode", func() {
		BeforeEach(func() {
			By("Ensuring Infrastructure is HA mode")
			Eventually(func() error {
				if err := cl.Get(testCtx, types.NamespacedName{Name: controllers.InfrastructureResourceName}, infraObj); err != nil {
					return err
				}

				infraObj.Status.ControlPlaneTopology = configv1.HighlyAvailableTopologyMode

				return cl.Status().Update(testCtx, infraObj)
			}, timeout, interval).Should(Succeed(), "Infrastructure should be in HA mode")

			By("Waiting for PDB to exist")

			pdb := &policyv1.PodDisruptionBudget{}

			Eventually(func() error {
				return cl.Get(testCtx, pdbKey, pdb)
			}, timeout, interval).Should(Succeed(), "PDB should exist before deletion test")
		})

		It("should recreate the PDB", func() {
			By("Manually deleting PDB")

			pdb := &policyv1.PodDisruptionBudget{}
			Expect(cl.Get(testCtx, pdbKey, pdb)).To(Succeed(), "PDB should be retrieved for deletion")
			Expect(cl.Delete(testCtx, pdb)).To(Succeed(), "PDB should be deleted")

			By("Verifying controller recreates PDB")
			Eventually(func() error {
				return cl.Get(testCtx, pdbKey, pdb)
			}, timeout, interval).Should(Succeed(), "Controller should recreate PDB")

			Expect(pdb.Spec.MinAvailable).NotTo(BeNil(), "Recreated PDB minAvailable should not be nil")
			Expect(pdb.Spec.MinAvailable.IntVal).To(Equal(int32(1)), "Recreated PDB should have minAvailable=1")
		})
	})

	Context("when using other topology modes", func() {
		DescribeTable("should create Deployment with 2 replicas and PDB",
			func(topology configv1.TopologyMode) {
				By("Updating Infrastructure topology")
				Eventually(func() error {
					if err := cl.Get(testCtx, types.NamespacedName{Name: controllers.InfrastructureResourceName}, infraObj); err != nil {
						return err
					}

					infraObj.Status.ControlPlaneTopology = topology

					return cl.Status().Update(testCtx, infraObj)
				}, timeout, interval).Should(Succeed(), "Infrastructure topology should be updated")

				By("Verifying Deployment has 2 replicas")

				deployment := &appsv1.Deployment{}

				Eventually(func() int32 {
					if err := cl.Get(testCtx, deploymentKey, deployment); err != nil {
						return -1
					}

					if deployment.Spec.Replicas == nil {
						return -1
					}

					return *deployment.Spec.Replicas
				}, timeout, interval).Should(Equal(int32(2)), "Deployment should have 2 replicas for non-SNO topology")

				By("Verifying PDB exists")

				pdb := &policyv1.PodDisruptionBudget{}

				Eventually(func() error {
					return cl.Get(testCtx, pdbKey, pdb)
				}, timeout, interval).Should(Succeed(), "PDB should exist for non-SNO topology")
			},
			Entry("DualReplica topology", configv1.DualReplicaTopologyMode),
			Entry("HighlyAvailableArbiter topology", configv1.HighlyAvailableArbiterMode),
			Entry("External topology", configv1.ExternalTopologyMode),
		)
	})

	AfterEach(func() {
		By("Cleaning up test resources")

		deployment := &appsv1.Deployment{}
		if err := cl.Get(testCtx, deploymentKey, deployment); err == nil {
			Expect(client.IgnoreNotFound(cl.Delete(testCtx, deployment))).To(Succeed(), "Deployment should be deleted in cleanup")
		}

		pdb := &policyv1.PodDisruptionBudget{}
		if err := cl.Get(testCtx, pdbKey, pdb); err == nil {
			Expect(client.IgnoreNotFound(cl.Delete(testCtx, pdb))).To(Succeed(), "PDB should be deleted in cleanup")
		}
	})
})

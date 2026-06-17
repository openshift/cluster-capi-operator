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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

const (
	timeout  = 10 * time.Second
	interval = 100 * time.Millisecond
)

var _ = Describe("CRDCompatibilityOperatorController", Serial, func() {
	var (
		deploymentKey types.NamespacedName
		pdbKey        types.NamespacedName
	)

	BeforeEach(func() {
		deploymentKey = types.NamespacedName{
			Name:      operandDeploymentName,
			Namespace: testNamespace,
		}
		pdbKey = types.NamespacedName{
			Name:      operandPDBName,
			Namespace: testNamespace,
		}
	})

	// assertHA verifies the HA state: 2 replicas + PDB exists with minAvailable=1
	assertHA := func() {
		By("Asserting HA state: 2 replicas and PDB exists")

		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      operandDeploymentName,
				Namespace: testNamespace,
			},
		}
		Eventually(komega.Object(deployment)).WithTimeout(timeout).WithPolling(interval).Should(SatisfyAll(
			HaveField("Spec.Replicas", HaveValue(BeEquivalentTo(2))),
			HaveField("Spec.Template.Spec.Containers", And(HaveLen(1), ContainElement(HaveField("Image", Equal(testOperandImage))))),
			HaveField("Spec.Template.Labels", HaveKeyWithValue("k8s-app", operandLabel)),
		))

		pdb := &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				Name:      operandPDBName,
				Namespace: testNamespace,
			},
		}
		Eventually(komega.Object(pdb)).WithTimeout(timeout).WithPolling(interval).Should(SatisfyAll(
			HaveField("Spec.MinAvailable.IntVal", BeEquivalentTo(1)),
			HaveField("Spec.Selector.MatchLabels", HaveKeyWithValue("k8s-app", operandLabel)),
		))
	}

	// assertSingleReplica verifies the SNO state: 1 replica + PDB deleted
	assertSingleReplica := func() {
		By("Asserting SingleReplica state: 1 replica and no PDB")

		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      operandDeploymentName,
				Namespace: testNamespace,
			},
		}
		Eventually(komega.Object(deployment)).WithTimeout(timeout).WithPolling(interval).Should(
			HaveField("Spec.Replicas", HaveValue(BeEquivalentTo(1))),
		)

		Eventually(func() bool {
			pdb := &policyv1.PodDisruptionBudget{}
			err := k8sClient.Get(ctx, pdbKey, pdb)

			return apierrors.IsNotFound(err)
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())
	}

	Context("when cluster topology is HighlyAvailable", func() {
		var infrastructure *configv1.Infrastructure

		BeforeEach(func() {
			By("Creating Infrastructure CR with HighlyAvailable topology")

			infrastructure = &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: controllers.InfrastructureResourceName,
				},
				Spec: configv1.InfrastructureSpec{},
			}
			Expect(k8sClient.Create(ctx, infrastructure)).To(Succeed())
			infrastructure.Status = configv1.InfrastructureStatus{
				ControlPlaneTopology: configv1.HighlyAvailableTopologyMode,
			}
			Expect(k8sClient.Status().Update(ctx, infrastructure)).To(Succeed())
			DeferCleanup(func() {
				Expect(test.CleanupAndWait(ctx, k8sClient, infrastructure)).To(Succeed())
			})
		})

		It("should create Deployment with 2 replicas and PDB", func() {
			assertHA()
		})

		Context("and changes to SingleReplica", func() {
			BeforeEach(func() {
				By("Waiting for HA state to be fully rolled out")
				assertHA()

				By("Updating Infrastructure to SingleReplica topology")
				Eventually(func() error {
					if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(infrastructure), infrastructure); err != nil {
						return err
					}

					infrastructure.Status.ControlPlaneTopology = configv1.SingleReplicaTopologyMode

					return k8sClient.Status().Update(ctx, infrastructure)
				}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
			})

			It("should update to 1 replica and delete PDB", func() {
				assertSingleReplica()
			})

			Context("and changes back to HighlyAvailable", func() {
				BeforeEach(func() {
					By("Waiting for SingleReplica state to be fully rolled out")
					assertSingleReplica()

					By("Updating Infrastructure back to HighlyAvailable topology")
					Eventually(func() error {
						if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(infrastructure), infrastructure); err != nil {
							return err
						}

						infrastructure.Status.ControlPlaneTopology = configv1.HighlyAvailableTopologyMode

						return k8sClient.Status().Update(ctx, infrastructure)
					}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
				})

				It("should restore 2 replicas and recreate PDB", func() {
					assertHA()
				})
			})
		})

		Context("and Deployment is manually modified", func() {
			BeforeEach(func() {
				By("Waiting for HA state to be fully rolled out")
				assertHA()

				By("Manually setting replicas to 5")

				deployment := &appsv1.Deployment{}

				Eventually(func() error {
					if err := k8sClient.Get(ctx, deploymentKey, deployment); err != nil {
						return err
					}

					wrongReplicas := int32(5)
					deployment.Spec.Replicas = &wrongReplicas

					return k8sClient.Update(ctx, deployment)
				}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
			})

			It("should correct drift back to 2 replicas", func() {
				assertHA()
			})
		})

		Context("and PDB is manually deleted", func() {
			BeforeEach(func() {
				By("Waiting for HA state to be fully rolled out")
				assertHA()

				By("Manually deleting PDB")

				pdb := &policyv1.PodDisruptionBudget{}
				Expect(k8sClient.Get(ctx, pdbKey, pdb)).To(Succeed())
				Expect(k8sClient.Delete(ctx, pdb)).To(Succeed())
			})

			It("should recreate the PDB", func() {
				By("Verifying controller recreates PDB")

				pdb := &policyv1.PodDisruptionBudget{
					ObjectMeta: metav1.ObjectMeta{
						Name:      operandPDBName,
						Namespace: testNamespace,
					},
				}
				Eventually(komega.Object(pdb)).WithTimeout(timeout).WithPolling(interval).Should(SatisfyAll(
					HaveField("Spec.MinAvailable.IntVal", BeEquivalentTo(1)),
					HaveField("Spec.Selector.MatchLabels", HaveKeyWithValue("k8s-app", operandLabel)),
				))
			})
		})
	})

	Context("when cluster topology is SingleReplica", func() {
		var infrastructure *configv1.Infrastructure

		BeforeEach(func() {
			By("Creating Infrastructure CR with SingleReplica topology")

			infrastructure = &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: controllers.InfrastructureResourceName,
				},
				Spec: configv1.InfrastructureSpec{},
			}
			Expect(k8sClient.Create(ctx, infrastructure)).To(Succeed())
			infrastructure.Status = configv1.InfrastructureStatus{
				ControlPlaneTopology: configv1.SingleReplicaTopologyMode,
			}
			Expect(k8sClient.Status().Update(ctx, infrastructure)).To(Succeed())
			DeferCleanup(func() {
				Expect(test.CleanupAndWait(ctx, k8sClient, infrastructure)).To(Succeed())
			})
		})

		It("should create Deployment with 1 replica and no PDB", func() {
			assertSingleReplica()
		})
	})

	Context("when using other topology modes", func() {
		DescribeTable("should create Deployment with 2 replicas and PDB",
			func(topology configv1.TopologyMode) {
				By("Creating Infrastructure CR with specified topology")

				infrastructure := &configv1.Infrastructure{
					ObjectMeta: metav1.ObjectMeta{
						Name: controllers.InfrastructureResourceName,
					},
					Spec: configv1.InfrastructureSpec{},
				}
				Expect(k8sClient.Create(ctx, infrastructure)).To(Succeed())
				infrastructure.Status = configv1.InfrastructureStatus{
					ControlPlaneTopology: topology,
				}
				Expect(k8sClient.Status().Update(ctx, infrastructure)).To(Succeed())
				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, k8sClient, infrastructure)).To(Succeed())
				})

				assertHA()
			},
			Entry("DualReplica topology", configv1.DualReplicaTopologyMode),
			Entry("HighlyAvailableArbiter topology", configv1.HighlyAvailableArbiterMode),
			Entry("External topology", configv1.ExternalTopologyMode),
		)
	})
})

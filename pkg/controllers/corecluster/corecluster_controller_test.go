/*
Copyright 2024 Red Hat, Inc.

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
package corecluster

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-api-actuator-pkg/testutils"
	clusterv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/core/v1beta2"
	awsv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/cluster-api/infrastructure/v1beta2"
	configv1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/config/v1"
	corev1resourcebuilder "github.com/openshift/cluster-api-actuator-pkg/testutils/resourcebuilder/core/v1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

var _ = Describe("Reconcile Core cluster", func() {
	var coreCluster *clusterv1.Cluster
	var testNamespaceName string
	testInfraName := "test-ocp-infrastructure-name"
	testRegionName := "eu-west-2"
	desiredOperatorReleaseVersion := "this-is-the-desired-release-version"
	var infra *configv1.Infrastructure
	var mgrCancel context.CancelFunc
	var mgrDone chan struct{}

	startManager := func(infra *configv1.Infrastructure) (context.CancelFunc, chan struct{}) {
		mgrCtx, mgrCancel := context.WithCancel(context.Background())
		mgrDone := make(chan struct{})

		By("Setting up a manager and controller")
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: testScheme,
			Metrics: server.Options{
				BindAddress: "0",
			},
			Controller: config.Controller{
				SkipNameValidation: ptr.To(true),
			},
		})
		Expect(err).ToNot(HaveOccurred(), "Manager should be able to be created")

		r := &CoreClusterController{
			ClusterOperatorStatusClient: operatorstatus.ClusterOperatorStatusClient{
				Client:           cl,
				ManagedNamespace: testNamespaceName,
				ReleaseVersion:   desiredOperatorReleaseVersion,
			},
			Cluster:  &clusterv1.Cluster{},
			Infra:    infra.DeepCopy(),
			Platform: infra.Status.PlatformStatus.Type,
		}
		Expect(r.SetupWithManager(mgr)).To(Succeed(), "Reconciler should be able to setup with manager")

		By("Starting the manager")
		go func() {
			defer GinkgoRecover()
			defer close(mgrDone)

			Expect((mgr).Start(mgrCtx)).To(Succeed())
		}()

		return mgrCancel, mgrDone
	}

	stopManager := func() {
		By("Stopping the manager")
		mgrCancel()
		// Wait for the mgrDone to be closed, which will happen once the mgr has stopped.
		<-mgrDone

		Eventually(mgrDone).Should(BeClosed())
	}

	BeforeEach(func() {
		By("Creating the testing namespace")
		namespace := corev1resourcebuilder.Namespace().WithGenerateName("test-capi-corecluster-").Build()
		Expect(cl.Create(ctx, namespace)).To(Succeed())
		testNamespaceName = namespace.Name

		By("Creating the testing ClusterOperator object")
		cO := configv1resourcebuilder.ClusterOperator().WithName(clusterOperatorName).Build()
		Expect(cl.Create(ctx, cO)).To(Succeed())
	})

	AfterEach(func() {
		By("Cleaning up the testing resources")
		testutils.CleanupResources(Default, ctx, testEnv.Config, cl, testNamespaceName,
			&configv1.Infrastructure{}, &clusterv1.Cluster{}, &awsv1.AWSCluster{}, &configv1.ClusterOperator{})
	})

	Context("With a supported platform", func() {
		BeforeEach(func() {
			By("Creating the testing infrastructure for AWS")
			openshiftInfrastructure := configv1resourcebuilder.Infrastructure().AsAWS(testInfraName, testRegionName).Build()
			Expect(cl.Create(ctx, openshiftInfrastructure)).To(Succeed())

			By("Patching the testing infrastructure status for AWS")
			infra = openshiftInfrastructure.DeepCopy()
			infra.Status = configv1.InfrastructureStatus{
				APIServerInternalURL: "https://test:8081",
				InfrastructureName:   testInfraName,
				Platform:             configv1.AWSPlatformType,
				PlatformStatus: &configv1.PlatformStatus{
					Type: configv1.AWSPlatformType,
					AWS: &configv1.AWSPlatformStatus{
						Region: testRegionName,
					},
				},
			}
			Expect(cl.Status().Patch(ctx, openshiftInfrastructure, client.MergeFrom(infra))).To(Succeed())
		})

		JustBeforeEach(func() {
			mgrCancel, mgrDone = startManager(infra)
		})

		JustAfterEach(func() {
			stopManager()
		})

		Context("When there is no core cluster", func() {
			Context("When there is no infra cluster", func() {
				It("should not create core or infra cluster", func() {

					testInfraCluster := awsv1resourcebuilder.AWSCluster().WithName(testInfraName).WithNamespace(testNamespaceName).Build()
					Consistently(komega.Get(testInfraCluster)).Should(MatchError("awsclusters.infrastructure.cluster.x-k8s.io \"test-ocp-infrastructure-name\" not found"))

					testCoreCluster := clusterv1resourcebuilder.Cluster().WithName(testInfraName).WithNamespace(testNamespaceName).Build()
					Consistently(komega.Get(testCoreCluster)).Should(MatchError("clusters.cluster.x-k8s.io \"test-ocp-infrastructure-name\" not found"))
				})
			})

			Context("When there is an infra cluster", func() {
				BeforeEach(func() {
					By("Creating a testing infra cluster")
					infraCluster := awsv1resourcebuilder.AWSCluster().WithName(testInfraName).WithNamespace(testNamespaceName).Build()
					Eventually(cl.Create(ctx, infraCluster)).Should(Succeed())
				})

				It("should create the core cluster", func() {
					testCoreCluster := clusterv1resourcebuilder.Cluster().WithName(testInfraName).WithNamespace(testNamespaceName).Build()
					Eventually(komega.Get(testCoreCluster)).Should(Succeed(), "should have been able to successfully get the core cluster")
					Eventually(komega.Object(testCoreCluster)).Should(
						HaveField("Status.Deprecated.V1Beta1.Conditions", SatisfyAll(
							Not(BeEmpty()),
							ContainElement(SatisfyAll(
								HaveField("Type", Equal(clusterv1.ControlPlaneInitializedV1Beta1Condition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
							)),
						)),
					)
				})

				Context("With a ClusterOperator", func() {
					It("should update the ClusterOperator status to be available, upgradeable, non-progressing, non-degraded", func() {
						co := komega.Object(configv1resourcebuilder.ClusterOperator().WithName(clusterOperatorName).Build())
						Eventually(co).Should(
							HaveField("Status.Conditions", SatisfyAll(
								ContainElement(And(HaveField("Type", Equal(configv1.OperatorAvailable)), HaveField("Status", Equal(configv1.ConditionTrue)))),
								ContainElement(And(HaveField("Type", Equal(configv1.OperatorProgressing)), HaveField("Status", Equal(configv1.ConditionFalse)))),
								ContainElement(And(HaveField("Type", Equal(configv1.OperatorDegraded)), HaveField("Status", Equal(configv1.ConditionFalse)))),
								ContainElement(And(HaveField("Type", Equal(configv1.OperatorUpgradeable)), HaveField("Status", Equal(configv1.ConditionTrue)))),
							)),
						)
					})

					It("should update the ClusterOperator status version to the desired one", func() {
						co := komega.Object(configv1resourcebuilder.ClusterOperator().WithName(clusterOperatorName).Build())
						Eventually(co).Should(
							HaveField("Status.Versions", ContainElement(SatisfyAll(
								HaveField("Name", Equal("operator")),
								HaveField("Version", Equal(desiredOperatorReleaseVersion)),
							))),
						)
					})
				})
			})
		})

		Context("When there is an existing core cluster", func() {
			BeforeEach(func() {
				By("Creating a testing core cluster object")
				coreCluster = clusterv1resourcebuilder.Cluster().WithNamespace(testNamespaceName).WithName(testInfraName).Build()
				Eventually(cl.Create(ctx, coreCluster)).Should(Succeed())
			})

			Context("When there is no infra cluster", func() {
				It("should not create the infra cluster", func() {
					testInfraCluster := awsv1resourcebuilder.AWSCluster().WithName(testInfraName).WithNamespace(testNamespaceName).Build()
					Consistently(komega.Get(testInfraCluster), "1s").Should(MatchError("awsclusters.infrastructure.cluster.x-k8s.io \"test-ocp-infrastructure-name\" not found"))
				})
			})

			Context("When there is an infra cluster", func() {
				BeforeEach(func() {
					By("Creating a testing infra cluster")
					infraCluster := awsv1resourcebuilder.AWSCluster().WithName(testInfraName).WithNamespace(testNamespaceName).Build()
					Eventually(cl.Create(ctx, infraCluster)).Should(Succeed())
				})

				It("should update core cluster status", func() {
					testCoreCluster := clusterv1resourcebuilder.Cluster().WithName(testInfraName).WithNamespace(testNamespaceName).Build()
					Eventually(komega.Get(testCoreCluster)).Should(Succeed(), "should have been able to successfully get the core cluster")
					Eventually(komega.Object(testCoreCluster)).Should(
						HaveField("Status.Deprecated.V1Beta1.Conditions", SatisfyAll(
							Not(BeEmpty()),
							ContainElement(SatisfyAll(
								HaveField("Type", Equal(clusterv1.ControlPlaneInitializedV1Beta1Condition)),
								HaveField("Status", Equal(corev1.ConditionTrue)),
							)),
						)),
					)
				})
			})
		})
	})

	Context("With an unsupported platform", func() {
		BeforeEach(func() {
			By("Creating the testing infrastructure for NonePlatform")
			noneInfra := configv1resourcebuilder.Infrastructure().WithName(testInfraName).Build()
			Expect(cl.Create(ctx, noneInfra)).To(Succeed())

			By("Patching the testing infrastructure status for NonePlatform ")
			infra = noneInfra.DeepCopy()
			infra.Status = configv1.InfrastructureStatus{
				APIServerInternalURL: "https://test:8081",
				InfrastructureName:   testInfraName,
				Platform:             configv1.NonePlatformType,
				PlatformStatus: &configv1.PlatformStatus{
					Type: configv1.NonePlatformType,
				},
			}
			Expect(cl.Status().Patch(ctx, noneInfra, client.MergeFrom(infra))).To(Succeed())
		})

		JustBeforeEach(func() {
			mgrCancel, mgrDone = startManager(infra)
		})

		JustAfterEach(func() {
			stopManager()
		})

		Context("When there is no core cluster", func() {
			It("should not create a core cluster", func() {
				testCoreCluster := clusterv1resourcebuilder.Cluster().WithName(testInfraName).WithNamespace(testNamespaceName).Build()
				Consistently(komega.Get(testCoreCluster), "1s").Should(MatchError("clusters.cluster.x-k8s.io \"test-ocp-infrastructure-name\" not found"))
			})
		})
	})
})

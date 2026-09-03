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
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIMachineManagement] Cluster API Machine Management", Label("skip-topology:External"), func() {
	BeforeEach(func() {
		if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
			Skip("Cluster API Machine Management tests are not supported on External topology clusters.")
		}
		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagement) {
			Skip("Feature gate ClusterAPIMachineManagement is not enabled.")
		}
	})

	Context("Operator & controller deployments", func() {
		It("should have the capi-operator deployment available", func() {
			framework.AssertDeploymentAvailable("capi-operator", framework.CAPIOperatorNamespace)
		})

		It("should have the cluster-api ClusterOperator reporting healthy", func() {
			co := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: framework.CAPIClusterOperatorName}}
			Eventually(komega.Object(co)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(SatisfyAll(
				HaveField("Status.Conditions", test.HaveCondition(configv1.OperatorAvailable).WithStatus(configv1.ConditionTrue)),
				HaveField("Status.Conditions", test.HaveCondition(configv1.OperatorDegraded).WithStatus(configv1.ConditionFalse)),
				HaveField("Status.Conditions", test.HaveCondition(configv1.OperatorProgressing).WithStatus(configv1.ConditionFalse)),
			))
		})

		It("should have the capi-controllers deployment available", func() {
			framework.AssertDeploymentAvailable("capi-controllers", framework.CAPINamespace)
		})

		It("should have the machine-api-migration deployment available", func() {
			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateMachineAPIMigration) {
				Skip("Skipping, machine-api-migration is only deployed when MachineAPIMigration is enabled")
			}

			framework.AssertDeploymentAvailable("machine-api-migration", framework.CAPINamespace)
		})

		It("should have the capi-installer deployment available", func() {
			framework.AssertDeploymentAvailable("capi-installer", framework.CAPIOperatorNamespace)
		})
	})

	Context("CRD & API readiness", func() {
		// TODO: revisit once OCPCLOUD-3435 is done
		DescribeTable("should have core Cluster API CRDs installed and established",
			func(crdName string) {
				crd := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: crdName}}
				Eventually(komega.Object(crd)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
					HaveField("Status.Conditions", test.HaveCondition(apiextensionsv1.Established).WithStatus(apiextensionsv1.ConditionTrue)),
				)
			},
			Entry("clusters CRD", "clusters.cluster.x-k8s.io"),
			Entry("machines CRD", "machines.cluster.x-k8s.io"),
			Entry("machinesets CRD", "machinesets.cluster.x-k8s.io"),
		)
	})

	Context("Management cluster resources", func() {
		It("should have the management cluster kubeconfig Secret present", func() {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-kubeconfig", clusterName),
				Namespace: framework.CAPINamespace,
			}}
			Eventually(komega.Object(secret)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
				HaveField("Data", HaveKey("value")),
			)
		})
	})

})

var _ = Describe("[OTP][Jira:OCPCLOUD][OCPFeatureGate:ClusterAPIMachineManagement][sig-cluster-lifecycle] Cluster_Infrastructure CAPI",
	Label("skip-topology:External"),
	func() {
		BeforeEach(func() {
			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagement) {
				Skip("ClusterAPIMachineManagement feature gate is not enabled")
			}

			if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
				Skip("Tests are not supported on External topology clusters.")
			}
		})

		It("should have workload management annotations on all deployments", Label("Lifecycle:informing"), func() {
			By("Listing deployments in the Cluster API namespace")
			deploys := &appsv1.DeploymentList{}
			Expect(cl.List(ctx, deploys, client.InNamespace(framework.CAPINamespace))).To(Succeed())
			Expect(deploys.Items).NotTo(BeEmpty(), "expected at least one deployment in %s", framework.CAPINamespace)

			By("Checking workload annotation on each deployment")
			for _, deploy := range deploys.Items {
				annotations := deploy.Spec.Template.Annotations
				Expect(annotations).To(HaveKeyWithValue(
					"target.workload.openshift.io/management",
					`{"effect": "PreferredDuringScheduling"}`,
				), "deployment %s is missing the workload management annotation", deploy.Name)
			}
		})

		It("should have FallbackToLogsOnError as terminationMessagePolicy", Label("Lifecycle:informing"), func() {
			By("Listing deployments in the Cluster API namespace")
			deploys := &appsv1.DeploymentList{}
			Expect(cl.List(ctx, deploys, client.InNamespace(framework.CAPINamespace))).To(Succeed())
			Expect(deploys.Items).NotTo(BeEmpty(), "expected at least one deployment in %s", framework.CAPINamespace)

			By("Checking terminationMessagePolicy on each deployment's containers")
			for _, deploy := range deploys.Items {
				for _, container := range deploy.Spec.Template.Spec.Containers {
					Expect(string(container.TerminationMessagePolicy)).To(
						Equal(string(corev1.TerminationMessageFallbackToLogsOnError)),
						"deployment %s container %s should have FallbackToLogsOnError", deploy.Name, container.Name)
				}
			}
		})
	})

var _ = Describe("[OTP][Jira:OCPCLOUD][OCPFeatureGate:ClusterAPIMachineManagement][sig-cluster-lifecycle] Cluster API Webhook Validation",
	Label("skip-topology:External"),
	func() {
		BeforeEach(func() {
			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagement) {
				Skip("ClusterAPIMachineManagement feature gate is not enabled")
			}

			if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
				Skip("Tests are not supported on External topology clusters.")
			}
		})

		It("should deny deletion of infrastructure cluster resources", Label("Disruptive"), Label("Lifecycle:informing"), func() {
			infraTypes, _, err := util.GetCAPITypesForInfrastructure(infra)
			if errors.Is(err, util.ErrUnsupportedPlatform) {
				Skip(fmt.Sprintf("Infra cluster deletion test not supported on %s", platform))
			}
			Expect(err).ToNot(HaveOccurred())

			infraCluster := infraTypes.Cluster()
			Expect(cl.Get(ctx, client.ObjectKey{Namespace: framework.CAPINamespace, Name: clusterName}, infraCluster)).To(Succeed())

			By(fmt.Sprintf("Attempting to delete %T/%s (dry-run)", infraCluster, clusterName))
			err = cl.Delete(ctx, infraCluster, client.DryRunAll)
			Expect(err).To(HaveOccurred(), "deletion should be denied")
			Expect(err.Error()).To(ContainSubstring("denied"), "error should mention denial")
		})

		It("should enforce webhook validations for Cluster API cluster resources", Label("Disruptive"), Label("Lifecycle:informing"), func() {
			switch platform {
			case configv1.AWSPlatformType, configv1.GCPPlatformType:
			default:
				Skip("Cluster API machine webhook tests only supported on AWS and GCP")
			}

			By("Getting the Cluster API cluster object")
			cluster := &clusterv1.Cluster{}
			Expect(cl.Get(ctx, client.ObjectKey{Namespace: framework.CAPINamespace, Name: clusterName}, cluster)).To(Succeed())

			By("Attempting to patch cluster with invalid infrastructureRef kind")
			patch := client.MergeFrom(cluster.DeepCopy())
			cluster.Spec.InfrastructureRef.Kind = "invalid"
			err := cl.Patch(ctx, cluster, patch)
			Expect(err).To(HaveOccurred(), "patching with invalid kind should be rejected")
			Expect(err.Error()).To(ContainSubstring("invalid"), "error should mention the invalid kind")

			By("Attempting to delete the cluster (dry-run)")
			freshCluster := &clusterv1.Cluster{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(cluster), freshCluster)).To(Succeed())
			err = cl.Delete(ctx, freshCluster, client.DryRunAll)
			Expect(err).To(HaveOccurred(), "cluster deletion should be denied")
			Expect(err.Error()).Should(MatchRegexp(`(?i)(denied|not allowed)`), "error should indicate deletion was denied")
		})
	})

// IPAM CRDs are installed unconditionally - they are required by MAPI vSphere (MAPV) regardless of
// the ClusterAPIMachineManagement feature gate state. This test can/should be removed some time after the feature
// gate is promoted and the special-case installation logic is dropped from the CAPI manifests.
// https://github.com/openshift/cluster-api/blob/303d9786a5017d299b6e7fc702bb92f5cb4550cf/openshift/manifests/0000_30_cluster-api_04_crd.core-cluster-api.yaml#L9
var _ = Describe("[OTP][Jira:OCPCLOUD][sig-cluster-lifecycle] Cluster_Infrastructure CAPI IPAM",
	Label("skip-topology:External"),
	func() {
		BeforeEach(func() {
			if IsMicroShift {
				Skip("Cluster API is not supported on MicroShift")
			}

			if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
				Skip("Tests are not supported on External topology clusters.")
			}
		})

		It("should have IPAM CRDs installed", Label("Lifecycle:informing"), func() {
			ipamCRDs := []string{
				"ipaddressclaims.ipam.cluster.x-k8s.io",
				"ipaddresses.ipam.cluster.x-k8s.io",
			}

			for _, crdName := range ipamCRDs {
				By(fmt.Sprintf("Checking CRD %s exists", crdName))
				crd := &apiextensionsv1.CustomResourceDefinition{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: crdName}, crd)).To(Succeed(),
					"CRD %s should exist", crdName)
			}
		})
	})

func secretSyncTest() {
	secretKey := client.ObjectKey{
		Namespace: framework.CAPINamespace,
		Name:      "worker-user-data",
	}

	By("Verifying worker-user-data secret exists and capturing its UID")
	secret := &corev1.Secret{}
	Expect(cl.Get(ctx, secretKey, secret)).To(Succeed())
	originalUID := secret.UID

	By("Deleting worker-user-data secret")
	Expect(cl.Delete(ctx, secret)).To(Succeed())

	By("Waiting for secret to be re-created with a new UID")
	recreated := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretKey.Name, Namespace: secretKey.Namespace}}
	Eventually(komega.Object(recreated)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
		HaveField("UID", Not(Equal(originalUID))),
		"worker-user-data secret should be re-created from %s", framework.MAPINamespace)
}

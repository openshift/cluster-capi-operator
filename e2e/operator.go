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
	. "github.com/onsi/gomega"

	"github.com/openshift/api/features"
	framework "github.com/openshift/cluster-capi-operator/e2e/framework"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("[OTP][Jira:OCPCLOUD][OCPFeatureGate:ClusterAPIMachineManagement][sig-cluster-lifecycle] Cluster_Infrastructure CAPI", func() {
	BeforeEach(func() {
		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagement) {
			Skip("ClusterAPIMachineManagement feature gate is not enabled")
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

var _ = Describe("[OTP][Jira:OCPCLOUD][OCPFeatureGate:ClusterAPIMachineManagementAWS][sig-cluster-lifecycle] Cluster API Secret Sync", func() {
	BeforeEach(func() {
		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagementAWS) {
			Skip("ClusterAPIMachineManagementAWS feature gate is not enabled")
		}
	})

	It("should re-sync worker-user-data secret after deletion", Label("Disruptive"), Label("Lifecycle:informing"), secretSyncTest)
})

var _ = Describe("[OTP][Jira:OCPCLOUD][OCPFeatureGate:ClusterAPIMachineManagementGCP][sig-cluster-lifecycle] Cluster API Secret Sync", func() {
	BeforeEach(func() {
		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagementGCP) {
			Skip("ClusterAPIMachineManagementGCP feature gate is not enabled")
		}
	})

	It("should re-sync worker-user-data secret after deletion", Label("Disruptive"), Label("Lifecycle:informing"), secretSyncTest)
})

var _ = Describe("[OTP][Jira:OCPCLOUD][OCPFeatureGate:ClusterAPIMachineManagementVSphere][sig-cluster-lifecycle] Cluster API Secret Sync", func() {
	BeforeEach(func() {
		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagementVSphere) {
			Skip("ClusterAPIMachineManagementVSphere feature gate is not enabled")
		}
	})

	It("should re-sync worker-user-data secret after deletion", Label("Disruptive"), Label("Lifecycle:informing"), secretSyncTest)
})

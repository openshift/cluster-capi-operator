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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The openshift-capi-controllers ValidatingWebhookConfiguration must be scoped
// to the managed namespace so it does not intercept Cluster API
// calls in other namespaces. This is critical because:
//
//   - On unsupported platforms the webhook server does not start, so cluster-wide
//     interception would reject every Cluster API Cluster operation with
//     "connection refused".
//   - Layered product operators (e.g. MCE/ACM) install Cluster API independently
//     and create Cluster API Cluster objects in their own namespaces. A
//     cluster-scoped webhook would block those operations even on supported
//     platforms.
//
// The test creates a Cluster API Cluster in the "default" namespace that is
// deliberately crafted to violate the webhook's validation rules:
//   - The infrastructureRef kind "UnsupportedInfraCluster" is not in the allowed
//     list (AWSCluster, AzureCluster, GCPCluster, etc.).
//   - The cluster name "webhook-scope-test" does not match the cluster's
//     infrastructureName, which the webhook requires for objects in the
//     managed namespace.
//
// If the webhook were cluster-scoped, all three operations (create, update,
// delete) would be rejected. The test proves the namespaceSelector is working
// by asserting they succeed.
var _ = Describe("Cluster API Webhook Namespace Scoping", Serial, func() {
	var cluster *clusterv1.Cluster

	BeforeEach(func() {
		skipUnlessClusterAPIClusterCRD()

		// Craft a Cluster API Cluster object that deliberately violates the
		// managed namespace scoped webhook's validation rules.
		// - name: not matching the infrastructure id
		// - kind: not in the allowed list
		// - namespace: outside the managed namespace
		cluster = newTestClusterAPICluster("default", "webhook-scope-test")
	})

	AfterEach(func() {
		cleanupTestCluster(cluster)
	})

	It("should allow creating a non-webhook-compliant Cluster API Cluster outside the managed namespace", func() {
		By("Creating a Cluster API Cluster in the default namespace")
		Eventually(func() error {
			return cl.Create(ctx, cluster)
		}).Should(Succeed(),
			"Non-Webhook-Compliant Cluster API Cluster creation should be allowed outside the managed namespace")
	})

	It("should allow updating a non-webhook-compliant Cluster API Cluster outside the managed namespace", func() {
		By("Creating the Cluster API Cluster in the default namespace")
		Eventually(func() error {
			return cl.Create(ctx, cluster)
		}).Should(Succeed())

		By("Updating the Cluster API Cluster in the default namespace")
		Eventually(func() error {
			if err := cl.Get(ctx, client.ObjectKeyFromObject(cluster), cluster); err != nil {
				return err
			}
			cluster.Spec.InfrastructureRef.Name = "webhook-scope-test-updated"
			return cl.Update(ctx, cluster)
		}).Should(Succeed(),
			"Non-Webhook-Compliant Cluster API Cluster update should be allowed outside the managed namespace")
	})

	It("should allow deleting a non-webhook-compliant Cluster API Cluster outside the managed namespace", func() {
		By("Creating the Cluster API Cluster in the default namespace")
		Eventually(func() error {
			return cl.Create(ctx, cluster)
		}).Should(Succeed())

		By("Deleting the Cluster API Cluster in the default namespace")
		Eventually(func() error {
			return cl.Delete(ctx, cluster)
		}).Should(Succeed(),
			"Non-Webhook-Compliant Cluster API Cluster deletion should be allowed outside the managed namespace")
	})
})

// This is the positive counterpart: verify that the webhook does reject invalid
// Cluster API Cluster objects inside the managed namespace on
// supported platforms.
var _ = Describe("Cluster API Webhook Validation Inside Managed Namespace", Serial, func() {
	var cluster *clusterv1.Cluster
	BeforeEach(func() {
		skipUnlessClusterAPIClusterCRD()

		_, err := util.GetCAPITypesForInfrastructure(infra)
		if errors.Is(err, util.ErrUnsupportedPlatform) {
			Skip("Platform is not supported for Cluster API. Skip webhook validation checks.")
		}
		Expect(err).ToNot(HaveOccurred(), "should not fail checking if platform is supported for Cluster API")

		// Craft a Cluster API Cluster object that deliberately violates the
		// managed namespace scoped webhook's validation rules.
		// - name: not matching the infrastructure id
		// - kind: not in the allowed list
		// - namespace: the managed namespace
		cluster = newTestClusterAPICluster(framework.CAPINamespace, "webhook-reject-test")
	})

	AfterEach(func() {
		cleanupTestCluster(cluster)
	})

	It("should reject creating a non-webhook-compliant Cluster API Cluster", func() {
		Eventually(func() error {
			err := cl.Create(ctx, cluster)
			if err == nil {
				// A Successful create is undesired. The webhook should reject the Cluster spec.
				// Return a StopTrying error to abort the test.
				return StopTrying("Cluster API Cluster create succeeded but invalid Cluster spec should have been rejected by admission")
			}
			if apierrors.IsForbidden(err) {
				// This is the expected error. Happy path.
				return nil
			}
			// This is a retryable error. Return it to the Eventually loop, so it can be retried.
			return err
		}).Should(Succeed(),
			"Expected a validation error for unsupported infrastructureRef kind/mismatched cluster name")
	})
})

// cleanupTestCluster deletes the test Cluster, strips its finalizers, and
// waits for it to disappear. We delete first so the object gets a
// deletionTimestamp, which prevents CAPI controllers from re-adding
// finalizers. Then we strip existing finalizers so the object can be
// garbage collected.
func cleanupTestCluster(cluster *clusterv1.Cluster) {
	GinkgoHelper()

	key := client.ObjectKeyFromObject(cluster)

	// Delete the object first. This sets the deletionTimestamp.
	Eventually(func() error {
		err := cl.Delete(ctx, cluster)
		if apierrors.IsNotFound(err) {
			// Object is already deleted. Nothing to do.
			return nil
		}
		// This is a retryable error. Return it to the Eventually loop, so it can be retried.
		return err
	}).Should(Succeed(), "Should have successfully deleted Cluster API Cluster")

	// Strip finalizers so the object can be garbage collected.
	// Retry on conflict since CAPI controllers may still be reconciling.
	Eventually(func() error {
		existing := &clusterv1.Cluster{}
		err := cl.Get(ctx, key, existing)
		if apierrors.IsNotFound(err) {
			// Object is already deleted. Nothing to do.
			return nil
		}
		if err != nil {
			// This is a retryable error. Return it to the Eventually loop, so it can be retried.
			return err
		}
		if len(existing.Finalizers) == 0 {
			// No finalizers to strip. Happy path.
			return nil
		}
		// Strip finalizers from the object.
		existing.Finalizers = nil
		return cl.Update(ctx, existing)
	}).WithTimeout(framework.WaitShort).Should(Succeed(), "Should have successfully stripped finalizers from Cluster API Cluster")

	// Wait for the object to be fully removed.
	Eventually(func() bool {
		err := cl.Get(ctx, key, &clusterv1.Cluster{})
		return apierrors.IsNotFound(err)
	}).WithTimeout(framework.WaitShort).Should(BeTrue(), "Should have successfully deleted Cluster API Cluster")
}

const clusterAPIClusterCRDName = "clusters.cluster.x-k8s.io"

// newTestClusterAPICluster returns a Cluster API Cluster
// that for webhook testing.
func newTestClusterAPICluster(namespace, name string) *clusterv1.Cluster {
	return &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: clusterv1.ClusterSpec{
			InfrastructureRef: clusterv1.ContractVersionedObjectReference{
				Kind:     "UnsupportedInfraCluster", // violates the namespace-scoped webhook's validation rules
				Name:     name,
				APIGroup: "infrastructure.cluster.x-k8s.io",
			},
		},
	}
}

// skipUnlessClusterAPIClusterCRD checks for the Cluster API Cluster CRD presence.
// If the CRD is not installed, the test is skipped.
//
// The Cluster API Cluster CRD may or may not be installed depending on
// whether this operator or another component (e.g. MCE/ACM) has deployed
// it. Skip if the CRD is absent since we cannot create a Cluster API
// Cluster object.
func skipUnlessClusterAPIClusterCRD() {
	GinkgoHelper()

	By("Checking for the presence of the Cluster API Cluster CRD")

	crd := &apiextensionsv1.CustomResourceDefinition{}
	Eventually(func() error {
		err := cl.Get(ctx, client.ObjectKey{Name: clusterAPIClusterCRDName}, crd)
		if apierrors.IsNotFound(err) {
			Skip("Cluster API's Cluster CRD is not installed. Skip webhook validation checks.")
		}
		return err
	}).Should(Succeed(),
		"failed to get Cluster API's Cluster CRD")
}

// Copyright 2025 Red Hat, Inc.
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
package e2e

import (
	nutanixv1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Cluster API Nutanix InfraCluster", Ordered, func() {
	var nutanixCluster *nutanixv1.NutanixCluster

	BeforeAll(func() {
		if platform != configv1.NutanixPlatformType {
			Skip("Skipping Nutanix E2E tests")
		}
	})

	AfterEach(func() {
		if platform != configv1.NutanixPlatformType {
			// Because AfterEach always runs, even when tests are skipped, we have to
			// explicitly skip it here for other platforms.
			Skip("Skipping Nutanix E2E tests")
		}
	})

	It("should have a NutanixCluster created by the infracluster controller", func() {
		By("Fetching the NutanixCluster object")
		nutanixCluster = &nutanixv1.NutanixCluster{}
		err := cl.Get(ctx, client.ObjectKey{
			Name:      clusterName,
			Namespace: framework.CAPINamespace,
		}, nutanixCluster)
		Expect(err).ToNot(HaveOccurred(), "should be able to get the NutanixCluster")
		Expect(nutanixCluster).ToNot(BeNil())
	})

	It("should have the correct ManagedBy annotation", func() {
		By("Validating the ManagedBy annotation")
		Expect(nutanixCluster.Annotations).To(HaveKey(clusterv1.ManagedByAnnotation))
		Expect(nutanixCluster.Annotations[clusterv1.ManagedByAnnotation]).To(Equal(managedByAnnotationValueClusterCAPIOperatorInfraClusterController))
	})

	It("should have the control plane endpoint configured", func() {
		By("Validating control plane endpoint")
		Expect(nutanixCluster.Spec.ControlPlaneEndpoint.Host).ToNot(BeEmpty(), "control plane endpoint host should not be empty")
		Expect(nutanixCluster.Spec.ControlPlaneEndpoint.Port).To(BeNumerically(">", 0), "control plane endpoint port should be greater than 0")
	})

	It("should have PrismCentral configuration if specified in Infrastructure", func() {
		By("Checking Infrastructure for Nutanix PrismCentral configuration")
		if mapiInfrastructure.Spec.PlatformSpec.Nutanix != nil &&
			mapiInfrastructure.Spec.PlatformSpec.Nutanix.PrismCentral.Address != "" {
			By("Validating PrismCentral configuration in NutanixCluster")
			Expect(nutanixCluster.Spec.PrismCentral).ToNot(BeNil(), "PrismCentral should be configured")
			Expect(nutanixCluster.Spec.PrismCentral.Address).To(Equal(mapiInfrastructure.Spec.PlatformSpec.Nutanix.PrismCentral.Address))
			Expect(nutanixCluster.Spec.PrismCentral.Port).To(Equal(mapiInfrastructure.Spec.PlatformSpec.Nutanix.PrismCentral.Port))
		}
	})

	It("should have failure domains configured if specified in Infrastructure", func() {
		By("Checking Infrastructure for Nutanix failure domains")
		if mapiInfrastructure.Spec.PlatformSpec.Nutanix != nil &&
			len(mapiInfrastructure.Spec.PlatformSpec.Nutanix.FailureDomains) > 0 {
			By("Validating failure domains in NutanixCluster")
			Expect(nutanixCluster.Spec.ControlPlaneFailureDomains).ToNot(BeEmpty(), "failure domains should be configured")
			Expect(len(nutanixCluster.Spec.ControlPlaneFailureDomains)).To(Equal(len(mapiInfrastructure.Spec.PlatformSpec.Nutanix.FailureDomains)))

			// Verify that each failure domain from Infrastructure is present in NutanixCluster
			infraFDNames := make(map[string]bool)
			for _, fd := range mapiInfrastructure.Spec.PlatformSpec.Nutanix.FailureDomains {
				infraFDNames[fd.Name] = true
			}

			for _, fd := range nutanixCluster.Spec.ControlPlaneFailureDomains {
				Expect(infraFDNames).To(HaveKey(fd.Name), "failure domain %s should exist in Infrastructure spec", fd.Name)
			}
		}
	})

	It("should eventually become ready", func() {
		By("Waiting for NutanixCluster to become ready")
		Eventually(func() bool {
			updatedCluster := &nutanixv1.NutanixCluster{}
			err := cl.Get(ctx, client.ObjectKey{
				Name:      clusterName,
				Namespace: framework.CAPINamespace,
			}, updatedCluster)
			if err != nil {
				return false
			}
			return updatedCluster.Status.Ready
		}, framework.WaitLong).Should(BeTrue(), "NutanixCluster should eventually become ready")
	})
})

var _ = Describe("Cluster API Nutanix Cluster", Ordered, func() {
	var capiCluster *clusterv1.Cluster

	BeforeAll(func() {
		if platform != configv1.NutanixPlatformType {
			Skip("Skipping Nutanix E2E tests")
		}
	})

	It("should have a CAPI Cluster with NutanixCluster infrastructure reference", func() {
		By("Fetching the CAPI Cluster object")
		capiCluster = &clusterv1.Cluster{}
		err := cl.Get(ctx, client.ObjectKey{
			Name:      clusterName,
			Namespace: framework.CAPINamespace,
		}, capiCluster)
		Expect(err).ToNot(HaveOccurred(), "should be able to get the CAPI Cluster")
		Expect(capiCluster).ToNot(BeNil())

		By("Validating infrastructure reference")
		Expect(capiCluster.Spec.InfrastructureRef).ToNot(BeNil())
		Expect(capiCluster.Spec.InfrastructureRef.Kind).To(Equal("NutanixCluster"))
		Expect(capiCluster.Spec.InfrastructureRef.Name).To(Equal(clusterName))
		Expect(capiCluster.Spec.InfrastructureRef.Namespace).To(Equal(framework.CAPINamespace))
	})

	It("should have control plane endpoint initialized", func() {
		By("Validating CAPI Cluster control plane endpoint")
		Expect(capiCluster.Spec.ControlPlaneEndpoint.Host).ToNot(BeEmpty())
		Expect(capiCluster.Spec.ControlPlaneEndpoint.Port).To(BeNumerically(">", 0))
	})
})

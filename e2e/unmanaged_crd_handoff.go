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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

const (
	clusterCRDName                          = "clusters.cluster.x-k8s.io"
	clusterCompatibilityRequirementName     = "ccapio-" + clusterCRDName
	compatibleTestSchemaProperty            = "ocpcloud3318E2ETest"
	installerControllerProgressingCondition = "InstallerControllerProgressing"
)

func storageVersion(crd *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinitionVersion {
	GinkgoHelper()

	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Storage {
			return &crd.Spec.Versions[i]
		}
	}

	Fail("CRD has no storage version")
	return nil
}

func addOptionalSpecProperty(crd *apiextensionsv1.CustomResourceDefinition, name string) {
	GinkgoHelper()

	version := storageVersion(crd)
	Expect(version.Schema).ToNot(BeNil())
	Expect(version.Schema.OpenAPIV3Schema).ToNot(BeNil())

	specSchema, ok := version.Schema.OpenAPIV3Schema.Properties["spec"]
	Expect(ok).To(BeTrue(), "storage version should have a spec schema")
	if specSchema.Properties == nil {
		specSchema.Properties = map[string]apiextensionsv1.JSONSchemaProps{}
	}
	specSchema.Properties[name] = apiextensionsv1.JSONSchemaProps{Type: "string"}
	version.Schema.OpenAPIV3Schema.Properties["spec"] = specSchema
}

func removeSpecProperty(crd *apiextensionsv1.CustomResourceDefinition, name string) bool {
	GinkgoHelper()

	version := storageVersion(crd)
	Expect(version.Schema).ToNot(BeNil())
	Expect(version.Schema.OpenAPIV3Schema).ToNot(BeNil())

	specSchema, ok := version.Schema.OpenAPIV3Schema.Properties["spec"]
	Expect(ok).To(BeTrue(), "storage version should have a spec schema")
	if _, ok := specSchema.Properties[name]; !ok {
		return false
	}

	delete(specSchema.Properties, name)
	version.Schema.OpenAPIV3Schema.Properties["spec"] = specSchema
	return true
}

var _ = FDescribe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIMachineManagement] Cluster API unmanaged CRD handoff",
	Label("Disruptive"), Label("skip-topology:External"), func() {
		BeforeEach(func() {
			if IsMicroShift {
				Skip("Cluster API is not supported on MicroShift")
			}
			if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
				Skip("Cluster API unmanaged CRD handoff is not supported on External topology clusters.")
			}
			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagement) {
				Skip("Feature gate ClusterAPIMachineManagement is not enabled.")
			}
		})

		It("should hand the Cluster CRD from the installer to compatibility requirements", func() {
			clusterAPI := &operatorv1alpha1.ClusterAPI{}
			Eventually(func(g Gomega) {
				g.Expect(cl.Get(ctx, client.ObjectKey{Name: "cluster"}, clusterAPI)).To(Succeed())
				g.Expect(clusterAPI.Spec).ToNot(BeNil())
				g.Expect(clusterAPI.Spec.UnmanagedCustomResourceDefinitions).ToNot(ContainElement(clusterCRDName),
					"the test requires the Cluster CRD to initially be managed")
				g.Expect(clusterAPI.Status.CurrentRevision).ToNot(BeEmpty())
				g.Expect(clusterAPI.Status.CurrentRevision).To(Equal(clusterAPI.Status.DesiredRevision))
			}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(Succeed())
			initialDesiredRevision := clusterAPI.Status.DesiredRevision
			trackResource(clusterAPI)

			clusterCRD := &apiextensionsv1.CustomResourceDefinition{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: clusterCRDName}, clusterCRD)).To(Succeed())
			trackResource(clusterCRD)

			By("Marking the Cluster CRD unmanaged")
			base := clusterAPI.DeepCopy()
			clusterAPI.Spec.UnmanagedCustomResourceDefinitions = append(
				clusterAPI.Spec.UnmanagedCustomResourceDefinitions, clusterCRDName,
			)
			Expect(cl.Patch(ctx, clusterAPI, client.MergeFrom(base))).To(Succeed())
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterAPI), clusterAPI)).To(Succeed())
			handoffGeneration := clusterAPI.Generation

			By("Waiting for the revision controller to observe the handoff")
			var desiredRevision operatorv1alpha1.RevisionName
			Eventually(func(g Gomega) {
				fresh := &operatorv1alpha1.ClusterAPI{}
				g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterAPI), fresh)).To(Succeed())
				g.Expect(fresh.Status.ObservedRevisionGeneration).To(BeNumerically(">=", handoffGeneration))
				g.Expect(fresh.Status.DesiredRevision).ToNot(Equal(initialDesiredRevision))
				g.Expect(fresh.Status.Revisions).To(ContainElement(SatisfyAll(
					HaveField("Name", Equal(fresh.Status.DesiredRevision)),
					HaveField("UnmanagedCustomResourceDefinitions", ContainElement(clusterCRDName)),
				)))
				desiredRevision = fresh.Status.DesiredRevision
			}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(Succeed())

			By("Waiting for the installer to create a compatible requirement")
			requirement := &apiextensionsv1alpha1.CompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{Name: clusterCompatibilityRequirementName},
			}
			trackResource(requirement)
			Eventually(komega.Object(requirement)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(SatisfyAll(
				HaveField("Status.CRDName", Equal(clusterCRDName)),
				HaveField("Status.ObservedCRD.UID", Equal(string(clusterCRD.UID))),
				HaveField("Status.Conditions", SatisfyAll(
					test.HaveCondition(apiextensionsv1alpha1.CompatibilityRequirementAdmitted).WithStatus(metav1.ConditionTrue),
					test.HaveCondition(apiextensionsv1alpha1.CompatibilityRequirementCompatible).WithStatus(metav1.ConditionTrue),
				)),
			))

			By("Waiting for the installer to complete the unmanaged revision")
			Eventually(komega.Object(clusterAPI)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(SatisfyAll(
				HaveField("Status.CurrentRevision", Equal(desiredRevision)),
				HaveField("Status.DesiredRevision", Equal(desiredRevision)),
			))
			clusterOperator := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: framework.CAPIClusterOperatorName}}
			Eventually(komega.Object(clusterOperator)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
				HaveField("Status.Conditions", test.HaveCondition(installerControllerProgressingCondition).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonAsExpected)),
			)

			By("Rejecting an incompatible Cluster CRD update")
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterCRD), clusterCRD)).To(Succeed())
			generationBeforeRejectedUpdate := clusterCRD.Generation
			incompatibleCRD := clusterCRD.DeepCopy()
			incompatibleVersion := storageVersion(incompatibleCRD)
			Expect(incompatibleVersion.Schema).ToNot(BeNil())
			Expect(incompatibleVersion.Schema.OpenAPIV3Schema).ToNot(BeNil())
			Expect(incompatibleVersion.Schema.OpenAPIV3Schema.Properties).To(HaveKey("status"))
			delete(incompatibleVersion.Schema.OpenAPIV3Schema.Properties, "status")
			incompatibleVersion.Subresources = nil

			err := cl.Update(ctx, incompatibleCRD)
			Expect(err).To(MatchError(ContainSubstring("CRD is not compatible with CompatibilityRequirements")))
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterCRD), clusterCRD)).To(Succeed())
			Expect(clusterCRD.Generation).To(Equal(generationBeforeRejectedUpdate))

			By("Accepting a compatible Cluster CRD update")
			currentSpecSchema := storageVersion(clusterCRD).Schema.OpenAPIV3Schema.Properties["spec"]
			Expect(currentSpecSchema.Properties).ToNot(HaveKey(compatibleTestSchemaProperty))
			DeferCleanup(func() {
				Eventually(func() error {
					fresh := &apiextensionsv1.CustomResourceDefinition{}
					if err := cl.Get(ctx, client.ObjectKey{Name: clusterCRDName}, fresh); err != nil {
						return err
					}
					if !removeSpecProperty(fresh, compatibleTestSchemaProperty) {
						return nil
					}
					return cl.Update(ctx, fresh)
				}).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed())
			})

			Eventually(func() error {
				fresh := &apiextensionsv1.CustomResourceDefinition{}
				if err := cl.Get(ctx, client.ObjectKey{Name: clusterCRDName}, fresh); err != nil {
					return err
				}
				addOptionalSpecProperty(fresh, compatibleTestSchemaProperty)
				return cl.Update(ctx, fresh)
			}).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed())

			Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterCRD), clusterCRD)).To(Succeed())
			updatedCRDGeneration := clusterCRD.Generation
			Expect(updatedCRDGeneration).To(BeNumerically(">", generationBeforeRejectedUpdate))
			updatedSpecSchema := storageVersion(clusterCRD).Schema.OpenAPIV3Schema.Properties["spec"]
			property, ok := updatedSpecSchema.Properties[compatibleTestSchemaProperty]
			Expect(ok).To(BeTrue())
			Expect(property.Type).To(Equal("string"))
			Expect(updatedSpecSchema.Required).ToNot(ContainElement(compatibleTestSchemaProperty))

			By("Waiting for the compatibility requirement to observe the compatible update")
			Eventually(komega.Object(requirement)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(SatisfyAll(
				HaveField("Status.ObservedCRD.Generation", Equal(updatedCRDGeneration)),
				HaveField("Status.Conditions", SatisfyAll(
					test.HaveCondition(apiextensionsv1alpha1.CompatibilityRequirementAdmitted).WithStatus(metav1.ConditionTrue),
					test.HaveCondition(apiextensionsv1alpha1.CompatibilityRequirementCompatible).WithStatus(metav1.ConditionTrue),
				)),
			))
			Eventually(komega.Object(clusterAPI)).WithTimeout(framework.WaitShort).WithPolling(framework.RetryMedium).Should(SatisfyAll(
				HaveField("Status.CurrentRevision", Equal(desiredRevision)),
				HaveField("Status.DesiredRevision", Equal(desiredRevision)),
			))
		})
	})

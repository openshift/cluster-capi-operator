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

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIMachineManagement] Cluster API unmanaged CRD handoff",
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
			clusterAPI := &operatorv1alpha1.ClusterAPI{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
			By("Waiting for the installer to be stable", func() {
				Eventually(komega.Object(clusterAPI)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(SatisfyAll(
					HaveField("Spec", Not(BeNil())),
					HaveField("Spec.UnmanagedCustomResourceDefinitions", Not(ContainElement(clusterCRDName))),
					HaveField("Status.CurrentRevision", Not(BeEmpty())),
					WithTransform(func(obj client.Object) bool {
						clusterAPI := obj.(*operatorv1alpha1.ClusterAPI)
						return clusterAPI.Status.CurrentRevision == clusterAPI.Status.DesiredRevision
					}, BeTrue()),
				), "installer should be stable before starting the unmanaged CRD handoff")
			})
			initialDesiredRevision := clusterAPI.Status.DesiredRevision
			trackResource(clusterAPI)

			clusterCRD := &apiextensionsv1.CustomResourceDefinition{}
			Expect(cl.Get(ctx, client.ObjectKey{Name: clusterCRDName}, clusterCRD)).To(Succeed(),
				"the managed Cluster CRD should exist before handoff")
			trackResource(clusterCRD)

			By("Marking the Cluster CRD unmanaged", func() {
				Eventually(komega.Update(clusterAPI, func() {
					clusterAPI.Spec.UnmanagedCustomResourceDefinitions = append(
						clusterAPI.Spec.UnmanagedCustomResourceDefinitions, clusterCRDName,
					)
				})).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed(),
					"should mark the Cluster CRD unmanaged")
			})
			handoffGeneration := clusterAPI.Generation

			By("Waiting for the revision controller to observe the handoff", func() {
				Eventually(komega.Object(clusterAPI)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
					HaveField("Status.ObservedRevisionGeneration", BeNumerically(">=", handoffGeneration)),
					"revision controller should observe ClusterAPI generation %d", handoffGeneration,
				)
			})
			Expect(clusterAPI.Status.DesiredRevision).ToNot(Equal(initialDesiredRevision),
				"desiredRevision should change after the handoff is observed")
			desiredRevision := clusterAPI.Status.DesiredRevision
			Expect(clusterAPI.Status.Revisions).To(ContainElement(SatisfyAll(
				HaveField("Name", Equal(desiredRevision)),
				HaveField("UnmanagedCustomResourceDefinitions", ContainElement(clusterCRDName)),
			)), "the desired revision should contain the unmanaged Cluster CRD")

			By("Waiting for the installer to complete the unmanaged revision", func() {
				Eventually(komega.Object(clusterAPI)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(SatisfyAll(
					HaveField("Status.CurrentRevision", Equal(desiredRevision)),
					HaveField("Status.DesiredRevision", Equal(desiredRevision)),
				), "installer should converge on unmanaged revision %q", desiredRevision)
			})

			requirement := &apiextensionsv1alpha1.CompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{Name: clusterCompatibilityRequirementName},
			}
			trackResource(requirement)
			By("Verifying the installer created a compatible requirement", func() {
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(requirement), requirement)).To(Succeed(),
					"installer completed revision %q without a readable CompatibilityRequirement; ClusterAPI status: %#v",
					desiredRevision, clusterAPI.Status)
				Expect(requirement).To(SatisfyAll(
					HaveField("Status.CRDName", Equal(clusterCRDName)),
					HaveField("Status.ObservedCRD.UID", Equal(string(clusterCRD.UID))),
					HaveField("Status.Conditions", SatisfyAll(
						test.HaveCondition(apiextensionsv1alpha1.CompatibilityRequirementAdmitted).WithStatus(metav1.ConditionTrue),
						test.HaveCondition(apiextensionsv1alpha1.CompatibilityRequirementCompatible).WithStatus(metav1.ConditionTrue),
					)),
				), "CompatibilityRequirement should be admitted and compatible with the installed Cluster CRD")
			})
			clusterOperator := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: framework.CAPIClusterOperatorName}}
			By("Waiting for the installer status to report success", func() {
				Eventually(komega.Object(clusterOperator)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
					HaveField("Status.Conditions", test.HaveCondition(installerControllerProgressingCondition).
						WithStatus(configv1.ConditionFalse).
						WithReason(operatorstatus.ReasonAsExpected)),
					"installer should report AsExpected after completing revision %q", desiredRevision,
				)
			})

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

			var updatedCRDGeneration int64
			By("Accepting a compatible Cluster CRD update", func() {
				currentSpecSchema := storageVersion(clusterCRD).Schema.OpenAPIV3Schema.Properties["spec"]
				Expect(currentSpecSchema.Properties).ToNot(HaveKey(compatibleTestSchemaProperty),
					"temporary schema property should not exist before the compatible update")

				cleanupCRD := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: clusterCRDName}}
				DeferCleanup(func() {
					Eventually(komega.Update(cleanupCRD, func() {
						removeSpecProperty(cleanupCRD, compatibleTestSchemaProperty)
					})).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed(),
						"cleanup should remove the temporary schema property")
				})

				Eventually(komega.Update(clusterCRD, func() {
					addOptionalSpecProperty(clusterCRD, compatibleTestSchemaProperty)
				})).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed(),
					"should add the optional schema property to the Cluster CRD")

				updatedCRDGeneration = clusterCRD.Generation
				Expect(updatedCRDGeneration).To(BeNumerically(">", generationBeforeRejectedUpdate),
					"compatible CRD update should advance the CRD generation")
				updatedSpecSchema := storageVersion(clusterCRD).Schema.OpenAPIV3Schema.Properties["spec"]
				property, ok := updatedSpecSchema.Properties[compatibleTestSchemaProperty]
				Expect(ok).To(BeTrue(), "compatible update should add the temporary schema property")
				Expect(property.Type).To(Equal("string"), "temporary schema property should have the requested type")
				Expect(updatedSpecSchema.Required).ToNot(ContainElement(compatibleTestSchemaProperty),
					"temporary schema property should remain optional")
			})

			By("Waiting for the compatibility requirement to observe the compatible update", func() {
				Eventually(komega.Object(requirement)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(SatisfyAll(
					HaveField("Status.ObservedCRD.Generation", Equal(updatedCRDGeneration)),
					HaveField("Status.Conditions", SatisfyAll(
						test.HaveCondition(apiextensionsv1alpha1.CompatibilityRequirementAdmitted).WithStatus(metav1.ConditionTrue),
						test.HaveCondition(apiextensionsv1alpha1.CompatibilityRequirementCompatible).WithStatus(metav1.ConditionTrue),
					)),
				), "CompatibilityRequirement should observe and accept CRD generation %d", updatedCRDGeneration)
			})
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterAPI), clusterAPI)).To(Succeed(),
				"should read ClusterAPI after the compatible CRD update")
			Expect(clusterAPI).To(SatisfyAll(
				HaveField("Status.CurrentRevision", Equal(desiredRevision)),
				HaveField("Status.DesiredRevision", Equal(desiredRevision)),
			), "compatible CRD update should not change the installed revision")
		})
	})

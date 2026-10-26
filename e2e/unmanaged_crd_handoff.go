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
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

const (
	clusterCRDName                          = "clusters.cluster.x-k8s.io"
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
		It("should hand off the unmanaged Cluster CRD and enforce compatibility", func() {
			var (
				clusterAPI  *operatorv1alpha1.ClusterAPI
				clusterCRD  *apiextensionsv1.CustomResourceDefinition
				requirement *openshiftapiextensionsv1.CompatibilityRequirement

				initialDesiredRevision         operatorv1alpha1.RevisionName
				desiredRevision                operatorv1alpha1.RevisionName
				generationBeforeRejectedUpdate int64
				updatedCRDGeneration           int64
			)

			if IsMicroShift {
				Skip("Cluster API is not supported on MicroShift")
			}
			if infra.Status.ControlPlaneTopology == configv1.ExternalTopologyMode {
				Skip("Cluster API unmanaged CRD handoff is not supported on External topology clusters.")
			}

			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateClusterAPIMachineManagement) {
				Skip("Feature gate ClusterAPIMachineManagement is not enabled.")
			}
			if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateCRDCompatibilityRequirementOperator) {
				Skip("Feature gate CRDCompatibilityRequirementOperator is not enabled.")
			}

			clusterAPI = &operatorv1alpha1.ClusterAPI{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
			clusterCRD = &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: clusterCRDName}}

			trackResource(clusterAPI)
			trackResource(clusterCRD)

			DeferCleanup(func() {
				cleanupCRD := &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: clusterCRDName}}
				Eventually(komega.Update(cleanupCRD, func() {
					removeSpecProperty(cleanupCRD, compatibleTestSchemaProperty)
				})).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed(),
					"cleanup should remove the temporary schema property")
			})

			By("Verifying the managed starting state")
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterAPI), clusterAPI)).To(Succeed(),
				"ClusterAPI configuration should exist before the handoff")
			Expect(clusterAPI.Spec).ToNot(BeNil(), "ClusterAPI should have a spec")
			Expect(clusterAPI.Spec.UnmanagedCustomResourceDefinitions).ToNot(ContainElement(clusterCRDName),
				"the Cluster CRD is already unmanaged; this irreversible handoff test requires a fresh cluster")

			By("Waiting for the installer to be stable")
			Eventually(func(g Gomega) {
				g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterAPI), clusterAPI)).To(Succeed())
				g.Expect(clusterAPI.Status.CurrentRevision).ToNot(BeEmpty())
				g.Expect(clusterAPI.Status.CurrentRevision).To(Equal(clusterAPI.Status.DesiredRevision))
			}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(Succeed(),
				"installer should be stable before starting the unmanaged CRD handoff")

			initialDesiredRevision = clusterAPI.Status.DesiredRevision

			Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterCRD), clusterCRD)).To(Succeed(),
				"the managed Cluster CRD should exist before handoff")

			By("Marking the Cluster CRD unmanaged")
			Eventually(komega.Update(clusterAPI, func() {
				// The mutation is replayed on conflict, so only append if a
				// previous attempt did not already add the CRD.
				if !slices.Contains(clusterAPI.Spec.UnmanagedCustomResourceDefinitions, clusterCRDName) {
					clusterAPI.Spec.UnmanagedCustomResourceDefinitions = append(
						clusterAPI.Spec.UnmanagedCustomResourceDefinitions, clusterCRDName,
					)
				}
			})).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed(),
				"should mark the Cluster CRD unmanaged")

			handoffGeneration := clusterAPI.Generation

			By("Waiting for the revision controller to observe the handoff")
			Eventually(komega.Object(clusterAPI)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
				HaveField("Status.ObservedRevisionGeneration", BeNumerically(">=", handoffGeneration)),
				"revision controller should observe ClusterAPI generation %d", handoffGeneration,
			)
			Expect(clusterAPI.Status.DesiredRevision).ToNot(Equal(initialDesiredRevision),
				"desiredRevision should change after the handoff is observed")
			desiredRevision = clusterAPI.Status.DesiredRevision
			Expect(desiredRevision).ToNot(BeEmpty(), "the unmanaged revision should have a nonempty name")

			var desiredAPIRevision operatorv1alpha1.ClusterAPIInstallerRevision
			Expect(clusterAPI.Status.Revisions).To(ContainElement(SatisfyAll(
				HaveField("Name", Equal(desiredRevision)),
				HaveField("UnmanagedCustomResourceDefinitions", ContainElement(clusterCRDName)),
			), &desiredAPIRevision), "the desired revision should contain the unmanaged Cluster CRD")

			requirement = &openshiftapiextensionsv1.CompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("ccapio-%s-%d", clusterCRDName, desiredAPIRevision.Revision)},
			}
			trackResource(requirement)

			By("Waiting for the installer to complete the unmanaged revision")
			Eventually(komega.Object(clusterAPI)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(SatisfyAll(
				HaveField("Status.CurrentRevision", Equal(desiredRevision)),
				HaveField("Status.DesiredRevision", Equal(desiredRevision)),
			), "installer should converge on unmanaged revision %q", desiredRevision)

			By("Verifying the installer created a compatible requirement")
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(requirement), requirement)).To(Succeed(),
				"installer completed revision %q without a readable CompatibilityRequirement; ClusterAPI status: %#v",
				desiredRevision, clusterAPI.Status)
			Expect(requirement).To(SatisfyAll(
				HaveField("Status.CRDName", Equal(clusterCRDName)),
				HaveField("Status.ObservedCRD.UID", Equal(string(clusterCRD.UID))),
				HaveField("Status.Conditions", SatisfyAll(
					test.HaveCondition(openshiftapiextensionsv1.CompatibilityRequirementAdmitted).WithStatus(metav1.ConditionTrue),
					test.HaveCondition(openshiftapiextensionsv1.CompatibilityRequirementCompatible).WithStatus(metav1.ConditionTrue),
				)),
			), "CompatibilityRequirement should be admitted and compatible with the installed Cluster CRD")

			By("Waiting for the installer status to report success")
			clusterOperator := &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: framework.CAPIClusterOperatorName}}
			Eventually(komega.Object(clusterOperator)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(
				HaveField("Status.Conditions", test.HaveCondition(installerControllerProgressingCondition).
					WithStatus(configv1.ConditionFalse).
					WithReason(operatorstatus.ReasonAsExpected)),
				"installer should report AsExpected after completing revision %q", desiredRevision,
			)

			By("Rejecting an incompatible Cluster CRD update")
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterCRD), clusterCRD)).To(Succeed())
			generationBeforeRejectedUpdate = clusterCRD.Generation

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
			Expect(clusterCRD.Generation).To(Equal(generationBeforeRejectedUpdate),
				"the rejected update should not have advanced the CRD generation")

			By("Accepting a compatible Cluster CRD update")
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterCRD), clusterCRD)).To(Succeed(),
				"should read the Cluster CRD before inspecting its schema")
			currentSpecSchema := storageVersion(clusterCRD).Schema.OpenAPIV3Schema.Properties["spec"]
			Expect(currentSpecSchema.Properties).ToNot(HaveKey(compatibleTestSchemaProperty),
				"temporary schema property should not exist before the compatible update")

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

			By("Waiting for the compatibility requirement to observe the compatible update")
			Eventually(komega.Object(requirement)).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(SatisfyAll(
				// The CRD is not re-fetched on each poll, so its generation
				// may have advanced past the one we observed.
				HaveField("Status.ObservedCRD.Generation", BeNumerically(">=", updatedCRDGeneration)),
				HaveField("Status.Conditions", SatisfyAll(
					test.HaveCondition(openshiftapiextensionsv1.CompatibilityRequirementAdmitted).WithStatus(metav1.ConditionTrue),
					test.HaveCondition(openshiftapiextensionsv1.CompatibilityRequirementCompatible).WithStatus(metav1.ConditionTrue),
				)),
			), "CompatibilityRequirement should observe and accept CRD generation %d", updatedCRDGeneration)

			Expect(cl.Get(ctx, client.ObjectKeyFromObject(clusterAPI), clusterAPI)).To(Succeed(),
				"should read ClusterAPI after the compatible CRD update")
			Expect(clusterAPI).To(SatisfyAll(
				HaveField("Status.CurrentRevision", Equal(desiredRevision)),
				HaveField("Status.DesiredRevision", Equal(desiredRevision)),
			), "compatible CRD update should not change the installed revision")
		})
	})

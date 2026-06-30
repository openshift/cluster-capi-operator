/*
Copyright 2026 Red Hat, Inc.

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
package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

const (
	crdCompatibilityDeploymentName = "compatibility-requirements-controllers"

	staticVWCCRDValidation = "openshift-compatibility-requirements-apiextensions-k8s-io-v1-customresourcedefinition-validation"
	staticVWCCRValidation  = "openshift-compatibility-requirements-apiextensions-openshift-io-v1alpha1-compatibilityrequirement-validation"
)

func waitForRequirementReady(crd *apiextensionsv1.CustomResourceDefinition, requirement *apiextensionsv1alpha1.CompatibilityRequirement) {
	GinkgoHelper()

	By("Refreshing the CRD to get its UID and generation", func() {
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(crd), crd)).To(Succeed())
	})

	By("Waiting for CompatibilityRequirement to be admitted and up to date", func() {
		Eventually(func(g Gomega) {
			fresh := &apiextensionsv1alpha1.CompatibilityRequirement{}
			g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(requirement), fresh)).To(Succeed())

			g.Expect(fresh.Status.ObservedCRD.UID).To(Equal(string(crd.UID)),
				"requirement should have observed the test CRD")
			g.Expect(fresh.Status.ObservedCRD.Generation).To(Equal(crd.Generation),
				"requirement should have observed the current CRD generation")

			g.Expect(fresh.Status.Conditions).To(SatisfyAll(
				test.HaveCondition(apiextensionsv1alpha1.CompatibilityRequirementAdmitted).
					WithStatus(metav1.ConditionTrue),
				test.HaveCondition(apiextensionsv1alpha1.CompatibilityRequirementProgressing).
					WithStatus(metav1.ConditionFalse).
					WithReason(apiextensionsv1alpha1.CompatibilityRequirementUpToDateReason),
			))
		}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(Succeed())
	})
}

func createTestCRDAndRequirement(crd *apiextensionsv1.CustomResourceDefinition, requirement *apiextensionsv1alpha1.CompatibilityRequirement) {
	GinkgoHelper()

	By("Creating the test CRD", func() {
		framework.CreateAndCleanup(ctx, cl, crd)
		trackResource(crd)
	})

	By("Creating the CompatibilityRequirement", func() {
		framework.CreateAndCleanup(ctx, cl, requirement)
		trackResource(requirement)
	})
}

func testCRDGVK(crd *apiextensionsv1.CustomResourceDefinition) schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   crd.Spec.Group,
		Version: crd.Spec.Versions[0].Name,
		Kind:    crd.Spec.Names.Kind,
	}
}

var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:CRDCompatibilityRequirementOperator] CRD Compatibility Checker", Ordered, func() {
	BeforeAll(func() {
		if !framework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateCRDCompatibilityRequirementOperator) {
			Skip("Feature gate CRDCompatibilityRequirementOperator is not enabled.")
		}
	})

	Context("Deployment health", func() {
		It("should have a running deployment and static ValidatingWebhookConfigs", func() {
			By("Checking the compatibility-requirements-controllers deployment is Available", func() {
				deployment := &appsv1.Deployment{}

				Eventually(func(g Gomega) {
					g.Expect(cl.Get(ctx, client.ObjectKey{
						Namespace: framework.CompatibilityRequirementsNamespace,
						Name:      crdCompatibilityDeploymentName,
					}, deployment)).To(Succeed())

					available := false
					for _, c := range deployment.Status.Conditions {
						if c.Type == appsv1.DeploymentAvailable && c.Status == corev1.ConditionTrue {
							available = true

							break
						}
					}

					g.Expect(available).To(BeTrue(), "deployment should have Available=True condition")
				}).WithTimeout(framework.WaitLong).WithPolling(framework.RetryMedium).Should(Succeed())
			})

			By("Checking all pods are Running", func() {
				Eventually(func(g Gomega) {
					pods := &corev1.PodList{}
					g.Expect(cl.List(ctx, pods,
						client.InNamespace(framework.CompatibilityRequirementsNamespace),
						client.MatchingLabels{"k8s-app": crdCompatibilityDeploymentName},
					)).To(Succeed())
					g.Expect(pods.Items).ToNot(BeEmpty(), "expected at least one pod")

					for _, pod := range pods.Items {
						g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning), fmt.Sprintf("pod %s is not Running", pod.Name))
					}
				}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(Succeed())
			})

			By("Checking the CRD validation ValidatingWebhookConfiguration exists", func() {
				Eventually(func() error {
					return cl.Get(ctx, client.ObjectKey{Name: staticVWCCRDValidation}, &admissionregistrationv1.ValidatingWebhookConfiguration{})
				}).WithTimeout(framework.WaitShort).WithPolling(framework.RetryMedium).Should(Succeed())
			})

			By("Checking the CompatibilityRequirement validation ValidatingWebhookConfiguration exists", func() {
				Eventually(func() error {
					return cl.Get(ctx, client.ObjectKey{Name: staticVWCCRValidation}, &admissionregistrationv1.ValidatingWebhookConfiguration{})
				}).WithTimeout(framework.WaitShort).WithPolling(framework.RetryMedium).Should(Succeed())
			})
		})
	})

	Context("Deployment replicas", func() {
		It("should have the correct number of replicas based on cluster topology", func() {
			deployment := &appsv1.Deployment{}
			Expect(cl.Get(ctx, client.ObjectKey{
				Namespace: framework.CompatibilityRequirementsNamespace,
				Name:      crdCompatibilityDeploymentName,
			}, deployment)).To(Succeed())

			Expect(deployment.Spec.Replicas).ToNot(BeNil())

			switch infra.Status.ControlPlaneTopology {
			case configv1.SingleReplicaTopologyMode:
				Expect(*deployment.Spec.Replicas).To(Equal(int32(1)),
					"SNO cluster should have 1 replica")
			case configv1.HighlyAvailableTopologyMode:
				Expect(*deployment.Spec.Replicas).To(BeNumerically(">", int32(1)),
					"HA cluster should have more than 1 replica")
			default:
				Fail(fmt.Sprintf("unknown control plane topology %q, update this test to handle it", infra.Status.ControlPlaneTopology))
			}
		})
	})

	Context("CRD admission", func() {
		var (
			testCRD     *apiextensionsv1.CustomResourceDefinition
			requirement *apiextensionsv1alpha1.CompatibilityRequirement
		)

		BeforeEach(func() {
			testCRD = test.GenerateTestCRD()
			requirement = test.GenerateTestCompatibilityRequirement(testCRD)
			createTestCRDAndRequirement(testCRD, requirement)
			waitForRequirementReady(testCRD, requirement)
		})

		It("should allow compatible CRD updates", func() {
			By("Adding a new optional property to the CRD (compatible change)", func() {
				Eventually(func() error {
					fresh := &apiextensionsv1.CustomResourceDefinition{}
					if err := cl.Get(ctx, client.ObjectKeyFromObject(testCRD), fresh); err != nil {
						return err
					}

					specProps := fresh.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
					if specProps.Properties == nil {
						specProps.Properties = make(map[string]apiextensionsv1.JSONSchemaProps)
					}

					specProps.Properties["newOptionalField"] = apiextensionsv1.JSONSchemaProps{Type: "string"}
					fresh.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = specProps

					return cl.Update(ctx, fresh)
				}).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed(), "compatible CRD update should succeed")
			})
		})

		It("should reject incompatible CRD updates", func() {
			By("Attempting to remove the status property from the CRD", func() {
				Eventually(func() error {
					fresh := &apiextensionsv1.CustomResourceDefinition{}
					if err := cl.Get(ctx, client.ObjectKeyFromObject(testCRD), fresh); err != nil {
						return err
					}

					delete(fresh.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties, "status")
					fresh.Spec.Versions[0].Subresources = nil

					err := cl.Update(ctx, fresh)
					if err == nil {
						return StopTrying("CRD update succeeded but should have been rejected")
					}

					return err
				}).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(MatchError(ContainSubstring("CRD is not compatible with CompatibilityRequirements")))
			})
		})
	})

	Context("Object validation admission", func() {
		var (
			testCRD     *apiextensionsv1.CustomResourceDefinition
			requirement *apiextensionsv1alpha1.CompatibilityRequirement
		)

		BeforeEach(func() {
			testCRD = test.GenerateTestCRD()

			specSchema := testCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
			specSchema.Properties = map[string]apiextensionsv1.JSONSchemaProps{
				"appName": {Type: "string"},
			}
			specSchema.Required = []string{"appName"}
			testCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = specSchema

			requirement = test.GenerateTestCompatibilityRequirement(testCRD)
			requirement.Spec.ObjectSchemaValidation.Action = apiextensionsv1alpha1.CRDAdmitActionDeny
			createTestCRDAndRequirement(testCRD, requirement)
			waitForRequirementReady(testCRD, requirement)

			By("Waiting for the dynamic ValidatingWebhookConfiguration to exist", func() {
				Eventually(func() error {
					return cl.Get(ctx, client.ObjectKey{Name: requirement.Name}, &admissionregistrationv1.ValidatingWebhookConfiguration{})
				}).WithTimeout(framework.WaitMedium).WithPolling(framework.RetryMedium).Should(Succeed())
			})
		})

		It("should accept CRs that conform to the compatibility schema", func() {
			By("Submitting a valid CR with the required appName field", func() {
				validCR := test.NewTestObject(testCRDGVK(testCRD)).
					WithName("valid-cr-test").
					WithSpec(map[string]any{
						"appName": "my-app",
					}).
					Build()

				Eventually(func() error {
					return cl.Create(ctx, validCR)
				}).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(Succeed(), "valid CR should be accepted")

				DeferCleanup(func() {
					_ = cl.Delete(ctx, validCR)
				})
			})
		})

		It("should reject CRs that do not conform to the compatibility schema", func() {
			By("Submitting a CR missing the required appName field", func() {
				invalidCR := test.NewTestObject(testCRDGVK(testCRD)).
					WithSpec(map[string]any{}).
					Build()

				Eventually(func() error {
					err := cl.Create(ctx, invalidCR.DeepCopy())
					if err == nil {
						return StopTrying("CR creation succeeded but should have been rejected")
					}

					return err
				}).WithTimeout(framework.WaitShort).WithPolling(framework.RetryShort).Should(MatchError(ContainSubstring("spec.appName")))
			})
		})
	})
})

/*
Copyright 2025 Red Hat, Inc.

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

package objectvalidation

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
)

// createValidatingWebhookConfig creates a ValidatingWebhookConfiguration for end-to-end testing.
func createValidatingWebhookConfig(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, crd *apiextensionsv1.CustomResourceDefinition) *admissionv1.ValidatingWebhookConfiguration {
	webhookPath := fmt.Sprintf("%s%s", webhookPrefix, compatibilityRequirement.Name)
	hostPort := fmt.Sprintf("%s:%d", testEnv.WebhookInstallOptions.LocalServingHost, testEnv.WebhookInstallOptions.LocalServingPort)
	webhookURL := fmt.Sprintf("https://%s%s", hostPort, webhookPath)

	validatingWebhookConfig := ValidatingWebhookConfigurationFor(compatibilityRequirement, crd)

	validatingWebhookConfig.Webhooks[0].ClientConfig = admissionv1.WebhookClientConfig{
		URL:      ptr.To(webhookURL),
		CABundle: testEnv.WebhookInstallOptions.LocalServingCAData,
	}

	return validatingWebhookConfig
}

// createWarningCompatibilityRequirement creates a CompatibilityRequirement with Warn action
// and minimal configuration to avoid selector validation issues.
func createWarningCompatibilityRequirement(crd *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1alpha1.CompatibilityRequirement {
	compatibilityRequirement := test.GenerateTestCompatibilityRequirement(crd)
	compatibilityRequirement.Spec.CustomResourceDefinitionSchemaValidation.Action = apiextensionsv1alpha1.CRDAdmitActionWarn
	compatibilityRequirement.Spec.ObjectSchemaValidation.Action = apiextensionsv1alpha1.CRDAdmitActionWarn

	return compatibilityRequirement
}

var _ = Describe("End-to-End Admission Webhook Integration", Ordered, ContinueOnFailure, func() {
	var (
		namespace                = "default"
		compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
		startWebhookServer       func()
		compatibilityCRD         *apiextensionsv1.CustomResourceDefinition
	)

	BeforeAll(func() {
		// Initialize validator and webhook server for all tests
		_, managerClient, startWebhookServer = InitValidator(context.Background())
		startWebhookServer()
	})

	BeforeEach(func(ctx context.Context) {
		compatibilityCRD = suiteCompatibilityCRD()
	})

	Describe("Schema Matching Scenarios", func() {
		Context("when schemas match exactly", func() {
			BeforeEach(func(ctx context.Context) {
				// Create and store the compatibility requirement
				compatibilityRequirement = test.GenerateTestCompatibilityRequirement(compatibilityCRD)
				Expect(cl.Create(ctx, compatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration to enable end-to-end testing
				webhookConfig := createValidatingWebhookConfig(compatibilityRequirement, compatibilityCRD)
				Expect(cl.Create(ctx, webhookConfig)).To(Succeed())
				waitForCompatibilityRequirementInWebhookManagerCache(ctx, compatibilityRequirement)

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl,
						webhookConfig,
						compatibilityRequirement,
					)).To(Succeed())
				})
			}, defaultNodeTimeout)

			It("should allow valid objects through API server", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				validObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "valid-value").
					WithField("testField", "test-value").
					WithField("optionalNumber", int64(42)).
					Build()

				// This should succeed - object conforms to schema
				Expect(cl.Create(ctx, validObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, validObj)).To(Succeed())
				})

				// Verify object was created successfully
				Eventually(kWithCtx(ctx).Get(validObj)).WithContext(ctx).Should(Succeed())
			}, defaultNodeTimeout)

			It("should reject invalid objects through API server", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				invalidObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("testField", "value").
					// Missing requiredField - should be rejected
					Build()

				expectInvalidCreateEventually(ctx, cl, invalidObj, "requiredField: Required value")
			}, defaultNodeTimeout)
		})
	})

	Describe("Tighter Validation Scenarios", func() {
		Context("when compatibility requirement has stricter validation than live CRD", func() {
			var (
				tighterCRD                      *apiextensionsv1.CustomResourceDefinition
				tighterCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
			)

			BeforeEach(func(ctx context.Context) {
				// Create a CRD with tighter validation (more required fields)
				tighterCRD = test.FromCRD(compatibilityCRD.DeepCopy()).
					WithRequiredField("testField").      // Make optional field required
					WithRequiredField("optionalNumber"). // Make optional field required
					Build()

				tighterCompatibilityRequirement = test.GenerateTestCompatibilityRequirement(tighterCRD)
				Expect(cl.Create(ctx, tighterCompatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration to enable end-to-end testing
				webhookConfig := createValidatingWebhookConfig(tighterCompatibilityRequirement, tighterCRD)
				Expect(cl.Create(ctx, webhookConfig)).To(Succeed())
				waitForCompatibilityRequirementInWebhookManagerCache(ctx, tighterCompatibilityRequirement)

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl,
						webhookConfig,
						tighterCompatibilityRequirement,
					)).To(Succeed())
				})
			}, defaultNodeTimeout)

			It("should reject objects missing newly required fields through API server", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				objMissingField := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					// Missing testField which is now required in tighter compatibility requirement
					Build()

				expectInvalidCreateEventually(ctx, cl, objMissingField, "testField: Required value, optionalNumber: Required value")
			}, defaultNodeTimeout)

			It("should allow objects with all required fields through API server", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				completeObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					WithField("optionalNumber", int64(42)).
					Build()

				// This should succeed with tighter validation
				Expect(cl.Create(ctx, completeObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, completeObj)).To(Succeed())
				})

				Eventually(kWithCtx(ctx).Get(completeObj)).WithContext(ctx).Should(Succeed())
			}, defaultNodeTimeout)
		})
	})

	Describe("Looser Validation Scenarios", func() {
		Context("when live CRD has stricter validation than compatibility requirement", func() {
			var (
				looserCRD                      *apiextensionsv1.CustomResourceDefinition
				looserCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
			)

			BeforeEach(func(ctx context.Context) {
				// Create a CRD with looser validation (fewer required fields)
				looserCRD = test.FromCRD(compatibilityCRD.DeepCopy()).
					RemoveRequiredField("requiredField").
					Build()

				looserCompatibilityRequirement = test.GenerateTestCompatibilityRequirement(looserCRD)
				Expect(cl.Create(ctx, looserCompatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration to enable end-to-end testing
				webhookConfig := createValidatingWebhookConfig(looserCompatibilityRequirement, looserCRD)
				Expect(cl.Create(ctx, webhookConfig)).To(Succeed())
				waitForCompatibilityRequirementInWebhookManagerCache(ctx, looserCompatibilityRequirement)

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl,
						webhookConfig,
						looserCompatibilityRequirement,
					)).To(Succeed())
				})
			}, defaultNodeTimeout)

			It("should allow objects matching tighter validation through API server", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				objWithExtraProperty := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					Build()

				Expect(cl.Create(ctx, objWithExtraProperty)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, objWithExtraProperty)).To(Succeed())
				})

				Eventually(kWithCtx(ctx).Get(objWithExtraProperty)).WithContext(ctx).Should(Succeed())
			}, defaultNodeTimeout)

			It("should not allow objects without required fields through API server", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				objMissingFormerlyRequired := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("testField", "value").
					// Missing requiredField which is no longer required in looser compatibility requirement
					Build()

				expectInvalidCreateEventually(ctx, cl, objMissingFormerlyRequired, "requiredField: Required value")
			}, defaultNodeTimeout)
		})
	})

	Describe("Update Operations - Schema Compatibility Testing", func() {
		var existingObj *unstructured.Unstructured

		BeforeEach(func(ctx context.Context) {
			gvk := schema.GroupVersionKind{
				Group:   compatibilityCRD.Spec.Group,
				Version: compatibilityCRD.Spec.Versions[0].Name,
				Kind:    compatibilityCRD.Spec.Names.Kind,
			}

			existingObj = test.NewTestObject(gvk).
				WithNamespace(namespace).
				WithField("requiredField", "initial-value").
				WithField("testField", "initial-test").
				Build()

			Expect(kWithCtx(ctx).ObjectList(&admissionv1.ValidatingWebhookConfigurationList{})()).Should(HaveField("Items", HaveLen(0)))
			Expect(kWithCtx(ctx).ObjectList(&admissionv1.ValidatingAdmissionPolicyList{})()).Should(HaveField("Items", HaveLen(0)))

			By("Creating object", func() {
				Expect(cl.Create(ctx, existingObj)).To(Succeed())
			})

			By("Waiting for object to be created", func() {
				// Wait for object to be created before proceeding
				Eventually(kWithCtx(ctx).Get(existingObj)).WithContext(ctx).Should(Succeed())
			})

			DeferCleanup(func(ctx context.Context) {
				Expect(test.CleanupAndWait(ctx, cl, existingObj)).To(Succeed())
			}, defaultNodeTimeout)
		})

		Context("tighter validation on updates (CompatibilityRequirement stricter than live CRD)", func() {
			var (
				tighterCRD                      *apiextensionsv1.CustomResourceDefinition
				tighterCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
				tighterWebhookConfig            *admissionv1.ValidatingWebhookConfiguration
			)

			BeforeEach(func(ctx context.Context) {
				// Create a CRD with tighter validation (more required fields)
				tighterCRD = test.FromCRD(compatibilityCRD.DeepCopy()).
					WithRequiredField("testField").
					WithRequiredField("optionalNumber").
					Build()

				By("Creating tighter compatibility requirement", func() {
					tighterCompatibilityRequirement = test.GenerateTestCompatibilityRequirement(tighterCRD)
					tighterCompatibilityRequirement.Name = fmt.Sprintf("tighter-%s", compatibilityCRD.Name)

					Expect(cl.Create(ctx, tighterCompatibilityRequirement)).To(Succeed())
				})

				// Create separate webhook config for tighter validation
				By("Creating tighter webhook config", func() {
					tighterWebhookConfig = createValidatingWebhookConfig(tighterCompatibilityRequirement, compatibilityCRD)
					tighterWebhookConfig.Name = fmt.Sprintf("test-tighter-validation-%s", tighterCompatibilityRequirement.Name)

					Expect(cl.Create(ctx, tighterWebhookConfig)).To(Succeed())
				})

				waitForCompatibilityRequirementInWebhookManagerCache(ctx, tighterCompatibilityRequirement)

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, tighterWebhookConfig, tighterCompatibilityRequirement)).To(Succeed())
				})
			}, defaultNodeTimeout)

			It("should reject updates that remove newly required fields", func(ctx context.Context) {
				Eventually(func(g Gomega) {
					g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(existingObj), existingObj)).To(Succeed())
					updateMissingField := existingObj.DeepCopy()
					delete(updateMissingField.Object, "testField") // Remove field required by tighter validation

					err := cl.Update(ctx, updateMissingField)
					g.Expect(err).To(HaveOccurred())
					g.Expect(apierrors.IsInvalid(err)).To(BeTrue())
					g.Expect(err).To(MatchError(ContainSubstring("testField: Required value, optionalNumber: Required value")))
				}).WithContext(ctx).WithTimeout(defaultEventuallyTimeout).Should(Succeed())
			}, defaultNodeTimeout)

			It("should allow updates that include all newly required fields", func(ctx context.Context) {
				// Update with all fields required by tighter validation
				updateWithAllFields := existingObj.DeepCopy()
				updateWithAllFields.Object["testField"] = "updated-test"
				updateWithAllFields.Object["optionalNumber"] = int64(42)

				Expect(cl.Update(ctx, updateWithAllFields)).To(Succeed())

				// Verify update was applied
				Eventually(kWithCtx(ctx).Object(existingObj)).WithContext(ctx).Should(
					HaveField("Object", HaveKeyWithValue("optionalNumber", int64(42))),
				)
			}, defaultNodeTimeout)
		})
	})

	Describe("Delete Operations", func() {
		It("should allow deletion through API server", func(ctx context.Context) {
			gvk := schema.GroupVersionKind{
				Group:   compatibilityCRD.Spec.Group,
				Version: compatibilityCRD.Spec.Versions[0].Name,
				Kind:    compatibilityCRD.Spec.Names.Kind,
			}

			objToDelete := test.NewTestObject(gvk).
				WithNamespace(namespace).
				WithField("requiredField", "value").
				Build()

			Expect(cl.Create(ctx, objToDelete)).To(Succeed())

			// Wait for object to be created
			Eventually(kWithCtx(ctx).Get(objToDelete)).WithContext(ctx).Should(Succeed())

			// Delete should always succeed (no validation on delete)
			Expect(cl.Delete(ctx, objToDelete)).To(Succeed())

			Eventually(kWithCtx(ctx).Get(objToDelete)).WithContext(ctx).Should(MatchError(ContainSubstring("not found")))
		}, defaultNodeTimeout)
	})

	Describe("Status Subresource Validation", func() {
		Context("when status subresource validation is enabled", func() {
			var (
				statusCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
			)

			BeforeEach(func(ctx context.Context) {
				liveCRD := compatibilityCRD.DeepCopy()

				// Capture the existing scale subresource, remove it for the duration of this test,
				// and then restore it to put the live CRD back to its original state.
				var existingScaleSubresource *apiextensionsv1.CustomResourceSubresourceScale

				Eventually(kWithCtx(ctx).Update(liveCRD, func() {
					existingScaleSubresource = liveCRD.Spec.Versions[0].Subresources.Scale
					liveCRD.Spec.Versions[0].Subresources.Scale = nil
				})).WithContext(ctx).Should(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Eventually(kWithCtx(ctx).Update(liveCRD, func() {
						liveCRD.Spec.Versions[0].Subresources.Scale = existingScaleSubresource
					})).WithContext(ctx).Should(Succeed())
				}, defaultNodeTimeout)

				statusCRD := compatibilityCRD.DeepCopy()
				// Disable the scale subresource for these test cases
				statusCRD.Spec.Versions[0].Subresources.Scale = nil

				// The baseCRD already has status subresource, so we can create a compatibility requirement directly
				statusCompatibilityRequirement = test.GenerateTestCompatibilityRequirement(statusCRD)
				statusCompatibilityRequirement.Name = fmt.Sprintf("status-%s", compatibilityCRD.Name)
				Expect(cl.Create(ctx, statusCompatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration for the compatibility requirement
				statusWebhookConfig := createValidatingWebhookConfig(statusCompatibilityRequirement, compatibilityCRD)
				statusWebhookConfig.Name = fmt.Sprintf("test-status-validation-%s", statusCompatibilityRequirement.Name)
				Expect(cl.Create(ctx, statusWebhookConfig)).To(Succeed())
				waitForCompatibilityRequirementInWebhookManagerCache(ctx, statusCompatibilityRequirement)

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, statusWebhookConfig, statusCompatibilityRequirement)).To(Succeed())
				})
			}, defaultNodeTimeout)

			It("should allow valid status updates when status validation is enabled", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				// First create the object without status
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					Build()

				Expect(cl.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				})

				// Wait for object to be created
				Eventually(kWithCtx(ctx).Get(baseObj)).WithContext(ctx).Should(Succeed())

				// Now update status with valid data
				statusUpdate := baseObj.DeepCopy()
				statusUpdate.Object["status"] = map[string]interface{}{
					"phase":         "Ready",
					"readyReplicas": int64(3),
					"conditions": []interface{}{
						map[string]interface{}{
							"type":   "Available",
							"status": "True",
						},
					},
				}

				Expect(cl.Status().Update(ctx, statusUpdate)).To(Succeed())

				// Verify status was updated
				Eventually(kWithCtx(ctx).Object(baseObj)).WithContext(ctx).Should(
					HaveField("Object", HaveKeyWithValue("status", HaveKeyWithValue("phase", "Ready"))),
				)
			}, defaultNodeTimeout)

			It("should reject status updates with invalid enum values when status validation is enabled", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				// First create the object without status
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					Build()

				Expect(cl.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				})

				expectStatusUpdateMatchErrorEventually(ctx, baseObj, map[string]interface{}{
					"phase": "InvalidPhase", // Not in allowed enum values
				}, "\"test-object\" is invalid: status.phase: Unsupported value: \"InvalidPhase\": supported values: \"Ready\", \"Pending\", \"Failed\"")
			}, defaultNodeTimeout)

			It("should reject status updates with invalid nested structure when status validation is enabled", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				// First create the object without status
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					Build()

				Expect(cl.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				})

				expectStatusUpdateMatchErrorEventually(ctx, baseObj, map[string]interface{}{
					"phase": "Ready",
					"conditions": []interface{}{
						map[string]interface{}{
							"type": "Available",
							// Missing required "status" field in condition
						},
					},
				}, "\"test-object\" is invalid: status.conditions[0].status: Required value")
			}, defaultNodeTimeout)

			It("should reject status updates with negative readyReplicas when status validation is enabled", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				// First create the object without status
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					Build()

				Expect(cl.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				})

				expectStatusUpdateMatchErrorEventually(ctx, baseObj, map[string]interface{}{
					"phase":         "Ready",
					"readyReplicas": int64(-1), // Below minimum value
				}, "\"test-object\" is invalid: status.readyReplicas: Invalid value: -1: status.readyReplicas in body should be greater than or equal to 0")
			}, defaultNodeTimeout)
		})
	})

	Describe("Scale Subresource Validation", func() {
		Context("when scale subresource validation is enabled", func() {
			var (
				scaleCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
			)

			BeforeEach(func(ctx context.Context) {
				liveCRD := compatibilityCRD.DeepCopy()

				// Capture the existing scale subresource, remove it for the duration of this test,
				// and then restore it to put the live CRD back to its original state.
				var existingScaleSubresource *apiextensionsv1.CustomResourceSubresourceScale

				Eventually(kWithCtx(ctx).Update(liveCRD, func() {
					existingScaleSubresource = liveCRD.Spec.Versions[0].Subresources.Scale
					// Disable the scale subresource for these test cases
					// This means the scale validation is being implemented by the compatibility requirement,
					// rather than the live CRD.
					liveCRD.Spec.Versions[0].Subresources.Scale = nil
				})).WithContext(ctx).Should(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Eventually(kWithCtx(ctx).Update(liveCRD, func() {
						liveCRD.Spec.Versions[0].Subresources.Scale = existingScaleSubresource
					})).WithContext(ctx).Should(Succeed())
				}, defaultNodeTimeout)

				scaleCRD := compatibilityCRD.DeepCopy()
				// Disable the status subresource for these test cases
				scaleCRD.Spec.Versions[0].Subresources.Status = nil

				// The baseCRD already has scale subresource, so we can create a compatibility requirement directly
				scaleCompatibilityRequirement = test.GenerateTestCompatibilityRequirement(scaleCRD)
				scaleCompatibilityRequirement.Name = fmt.Sprintf("scale-%s", compatibilityCRD.Name)
				Expect(cl.Create(ctx, scaleCompatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration for the compatibility requirement
				scaleWebhookConfig := createValidatingWebhookConfig(scaleCompatibilityRequirement, compatibilityCRD)
				scaleWebhookConfig.Name = fmt.Sprintf("test-scale-validation-%s", scaleCompatibilityRequirement.Name)
				Expect(cl.Create(ctx, scaleWebhookConfig)).To(Succeed())
				waitForCompatibilityRequirementInWebhookManagerCache(ctx, scaleCompatibilityRequirement)

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, scaleWebhookConfig, scaleCompatibilityRequirement)).To(Succeed())
				})
			}, defaultNodeTimeout)

			It("should allow objects with valid scale-related fields when scale validation is enabled", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				validScaledObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					WithField("spec", map[string]interface{}{
						"replicas": int64(5),
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"app": "test-app",
							},
						},
					}).
					Build()

				Expect(cl.Create(ctx, validScaledObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, validScaledObj)).To(Succeed())
				})

				Eventually(kWithCtx(ctx).Get(validScaledObj)).WithContext(ctx).Should(Succeed())

				// Update status with valid readyReplicas using status subclient
				statusUpdate := validScaledObj.DeepCopy()
				statusUpdate.Object["status"] = map[string]interface{}{
					"readyReplicas": int64(3),
				}

				Expect(cl.Status().Update(ctx, statusUpdate)).To(Succeed())

				// Verify status was updated
				Eventually(kWithCtx(ctx).Object(validScaledObj)).WithContext(ctx).Should(
					HaveField("Object", HaveKeyWithValue("status", HaveKeyWithValue("readyReplicas", int64(3)))),
				)
			}, defaultNodeTimeout)

			It("should reject objects with replica count above maximum when scale validation is enabled", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				objWithTooManyReplicas := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					WithField("spec", map[string]interface{}{
						"replicas": int64(150), // Above maximum of 100
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"app": "test-app",
							},
						},
					}).
					Build()

				expectInvalidCreateEventually(ctx, cl, objWithTooManyReplicas, "\"test-object\" is invalid: spec.replicas: Invalid value: 150: spec.replicas in body should be less than or equal to 100")
			}, defaultNodeTimeout)

			It("should reject objects with negative replica count when scale validation is enabled", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				objWithNegativeReplicas := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					WithField("spec", map[string]interface{}{
						"replicas": int64(-1), // Below minimum of 0
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"app": "test-app",
							},
						},
					}).
					Build()

				expectInvalidCreateEventually(ctx, cl, objWithNegativeReplicas, "\"test-object\" is invalid: [spec.replicas: Invalid value: -1: spec.replicas in body should be greater than or equal to 0, .spec.replicas: Invalid value: -1: should be a non-negative integer]")
			}, defaultNodeTimeout)

			It("should reject status updates with negative readyReplicas when scale validation is enabled", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				// First create the object with valid spec
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					WithField("spec", map[string]interface{}{
						"replicas": int64(3),
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"app": "test-app",
							},
						},
					}).
					Build()

				Expect(cl.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				})

				expectStatusUpdateMatchErrorEventually(ctx, baseObj, map[string]interface{}{
					"readyReplicas": int64(-1), // Below minimum of 0
				}, "\"test-object\" is invalid: [status.readyReplicas: Invalid value: -1: status.readyReplicas in body should be greater than or equal to 0, .status.readyReplicas: Invalid value: -1: should be a non-negative integer]")
			}, defaultNodeTimeout)
		})
	})

	Describe("Warning Mode Validation", func() {
		var (
			warningHandler *test.WarningHandler
			warningClient  client.Client
		)

		BeforeEach(func() {
			// Create a new client that collects warnings in the test warning handler.
			var err error

			warningHandler = test.NewTestWarningHandler()
			warningConfig := *cfg
			warningConfig.WarningHandlerWithContext = warningHandler
			warningClient, err = client.New(&warningConfig, client.Options{})
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when ObjectSchemaValidation Action is set to Warn", func() {
			var (
				warningCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
			)

			BeforeEach(func(ctx context.Context) {
				// Create a CompatibilityRequirement with Warn action using utility function
				warningCompatibilityRequirement = createWarningCompatibilityRequirement(compatibilityCRD.DeepCopy())

				Expect(warningClient.Create(ctx, warningCompatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration for the warning requirement
				warningWebhookConfig := createValidatingWebhookConfig(warningCompatibilityRequirement, compatibilityCRD)
				warningWebhookConfig.Name = fmt.Sprintf("test-warning-validation-%s", warningCompatibilityRequirement.Name)
				Expect(warningClient.Create(ctx, warningWebhookConfig)).To(Succeed())
				waitForCompatibilityRequirementInWebhookManagerCache(ctx, warningCompatibilityRequirement)

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, warningClient, warningWebhookConfig, warningCompatibilityRequirement)).To(Succeed())
				})
			}, defaultNodeTimeout)

			It("should allow objects with which violate numeric bounds but generate warnings", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				invalidObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithNestedField("spec.replicas", int64(150)). // Above maximum of 100
					Build()

				// This should succeed despite validation failure, with warnings
				err := warningClient.Create(ctx, invalidObj)
				Expect(err).ToNot(HaveOccurred())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, warningClient, invalidObj)).To(Succeed())
				})

				// Verify object was created successfully
				Eventually(kWithCtx(ctx).Get(invalidObj)).WithContext(ctx).Should(Succeed())

				Expect(warningHandler.Messages()).To(ConsistOf(ContainSubstring("Warning: spec.replicas: Invalid value: 150: spec.replicas in body should be less than or equal to 100")))
			}, defaultNodeTimeout)

			It("should allow updates changing a field to be invalid but generate warnings", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				// First create a valid object
				validObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithNestedField("spec.replicas", int64(100)). // At maximum of 100
					Build()

				Expect(warningClient.Create(ctx, validObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, warningClient, validObj)).To(Succeed())
				})

				// Wait for object to be created
				Eventually(kWithCtx(ctx).Get(validObj)).WithContext(ctx).Should(Succeed())
				Expect(warningHandler.Messages()).To(BeEmpty())

				// Update to remove required field - should generate warning but succeed
				invalidUpdate := validObj.DeepCopy()
				Expect(unstructured.SetNestedField(invalidUpdate.Object, int64(150), "spec", "replicas")).To(Succeed())

				Expect(warningClient.Update(ctx, invalidUpdate)).To(Succeed())

				// Verify update was applied
				Eventually(kWithCtx(ctx).Object(validObj)).WithContext(ctx).Should(
					HaveField("Object", HaveKeyWithValue("spec", HaveKeyWithValue("replicas", int64(150)))),
				)
				Expect(warningHandler.Messages()).To(ConsistOf(ContainSubstring("Warning: spec.replicas: Invalid value: 150: spec.replicas in body should be less than or equal to 100")))
			}, defaultNodeTimeout)
		})

		Context("when ObjectSchemaValidation Action is Warn for status subresource", func() {
			var (
				warningStatusCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
			)

			BeforeEach(func(ctx context.Context) {
				liveCRD := compatibilityCRD.DeepCopy()

				// Capture the existing scale subresource, remove it for the duration of this test,
				// and then restore it to put the live CRD back to its original state.
				var existingScaleSubresource *apiextensionsv1.CustomResourceSubresourceScale

				Eventually(kWithCtx(ctx).Update(liveCRD, func() {
					existingScaleSubresource = liveCRD.Spec.Versions[0].Subresources.Scale
					liveCRD.Spec.Versions[0].Subresources.Scale = nil
				})).Should(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Eventually(kWithCtx(ctx).Update(liveCRD, func() {
						liveCRD.Spec.Versions[0].Subresources.Scale = existingScaleSubresource
					})).WithContext(ctx).Should(Succeed())
				}, defaultNodeTimeout)

				statusCRD := compatibilityCRD.DeepCopy()
				// Disable the scale subresource for these test cases
				statusCRD.Spec.Versions[0].Subresources.Scale = nil

				// Create a CompatibilityRequirement with Warn action using utility function
				warningStatusCompatibilityRequirement = createWarningCompatibilityRequirement(statusCRD)
				Expect(warningClient.Create(ctx, warningStatusCompatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration for the warning requirement
				warningStatusWebhookConfig := createValidatingWebhookConfig(warningStatusCompatibilityRequirement, compatibilityCRD)
				warningStatusWebhookConfig.Name = fmt.Sprintf("test-warning-status-validation-%s", warningStatusCompatibilityRequirement.Name)
				Expect(warningClient.Create(ctx, warningStatusWebhookConfig)).To(Succeed())
				waitForCompatibilityRequirementInWebhookManagerCache(ctx, warningStatusCompatibilityRequirement)

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, warningClient, warningStatusWebhookConfig, warningStatusCompatibilityRequirement)).To(Succeed())
				})
			}, defaultNodeTimeout)

			It("should allow status updates with invalid enum values but generate warnings", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				// First create the object without status
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					Build()

				Expect(cl.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				}, defaultNodeTimeout)

				// Wait for object to be created
				Eventually(kWithCtx(ctx).Get(baseObj)).WithContext(ctx).Should(Succeed())

				// Update status with invalid enum value - should generate warning but succeed
				statusUpdate := baseObj.DeepCopy()
				statusUpdate.Object["status"] = map[string]interface{}{
					"phase": "InvalidPhase", // Not in allowed enum values
				}

				err := warningClient.Status().Update(ctx, statusUpdate)
				Expect(err).ToNot(HaveOccurred()) // Should succeed despite validation failure

				// Verify status was updated despite being invalid
				Eventually(kWithCtx(ctx).Object(baseObj)).WithContext(ctx).Should(
					HaveField("Object", HaveKeyWithValue("status", HaveKeyWithValue("phase", "InvalidPhase"))),
				)
				Expect(warningHandler.Messages()).To(ConsistOf(ContainSubstring("Warning: status.phase: Unsupported value: \"InvalidPhase\": supported values: \"Ready\", \"Pending\", \"Failed\"")))
			}, defaultNodeTimeout)

			It("should allow status updates with invalid nested structures but generate warnings", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				// First create the object without status
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					Build()

				Expect(cl.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				}, defaultNodeTimeout)

				// Wait for object to be created
				Eventually(kWithCtx(ctx).Get(baseObj)).WithContext(ctx).Should(Succeed())

				// Update status with invalid nested structure - should generate warning but succeed
				statusUpdate := baseObj.DeepCopy()
				statusUpdate.Object["status"] = map[string]interface{}{
					"phase": "Ready",
					"conditions": []interface{}{
						map[string]interface{}{
							"type": "Available",
							// Missing required "status" field in condition
						},
					},
				}

				err := warningClient.Status().Update(ctx, statusUpdate)
				Expect(err).ToNot(HaveOccurred()) // Should succeed despite validation failure

				// Verify status was updated despite being invalid
				Eventually(kWithCtx(ctx).Object(baseObj)).WithContext(ctx).Should(
					HaveField("Object", HaveKeyWithValue("status", HaveKeyWithValue("phase", "Ready"))),
				)
				Expect(warningHandler.Messages()).To(ConsistOf(ContainSubstring("Warning: status.conditions[0].status: Required value")))
			}, defaultNodeTimeout)

			It("should allow status updates with negative readyReplicas but generate warnings", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				// First create the object without status
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					Build()

				Expect(warningClient.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, warningClient, baseObj)).To(Succeed())
				})

				// Wait for object to be created
				Eventually(kWithCtx(ctx).Get(baseObj)).WithContext(ctx).Should(Succeed())

				// Update status with negative readyReplicas - should generate warning but succeed
				statusUpdate := baseObj.DeepCopy()
				statusUpdate.Object["status"] = map[string]interface{}{
					"phase":         "Ready",
					"readyReplicas": int64(-1), // Below minimum value
				}

				err := warningClient.Status().Update(ctx, statusUpdate)
				Expect(err).ToNot(HaveOccurred()) // Should succeed despite validation failure

				// Verify status was updated despite being invalid
				Eventually(kWithCtx(ctx).Object(baseObj)).WithContext(ctx).Should(
					HaveField("Object", HaveKeyWithValue("status", HaveKeyWithValue("readyReplicas", int64(-1)))),
				)
				Expect(warningHandler.Messages()).To(ConsistOf(ContainSubstring("Warning: status.readyReplicas: Invalid value: -1: status.readyReplicas in body should be greater than or equal to 0")))
			}, defaultNodeTimeout)
		})

		Context("when ObjectSchemaValidation Action is Warn for scale subresource", func() {
			var (
				warningScaleCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
			)

			BeforeEach(func(ctx context.Context) {
				liveCRD := compatibilityCRD.DeepCopy()

				// Capture the existing scale subresource, remove it for the duration of this test,
				// and then restore it to put the live CRD back to its original state.
				var existingScaleSubresource *apiextensionsv1.CustomResourceSubresourceScale

				Eventually(kWithCtx(ctx).Update(liveCRD, func() {
					existingScaleSubresource = liveCRD.Spec.Versions[0].Subresources.Scale
					// Disable the live CRD scale subresource else the objects will be rejected
					// and we won't be able to check the warnings.
					liveCRD.Spec.Versions[0].Subresources.Scale = nil
				})).WithContext(ctx).Should(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Eventually(kWithCtx(ctx).Update(liveCRD, func() {
						liveCRD.Spec.Versions[0].Subresources.Scale = existingScaleSubresource
					})).WithContext(ctx).Should(Succeed())
				}, defaultNodeTimeout)

				scaleCRD := compatibilityCRD.DeepCopy()

				// Create a CompatibilityRequirement with Warn action using utility function
				warningScaleCompatibilityRequirement = createWarningCompatibilityRequirement(scaleCRD)
				Expect(warningClient.Create(ctx, warningScaleCompatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration for the warning requirement
				warningScaleWebhookConfig := createValidatingWebhookConfig(warningScaleCompatibilityRequirement, compatibilityCRD)
				warningScaleWebhookConfig.Name = fmt.Sprintf("test-warning-scale-validation-%s", warningScaleCompatibilityRequirement.Name)
				Expect(warningClient.Create(ctx, warningScaleWebhookConfig)).To(Succeed())
				waitForCompatibilityRequirementInWebhookManagerCache(ctx, warningScaleCompatibilityRequirement)

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl, warningScaleWebhookConfig, warningScaleCompatibilityRequirement)).To(Succeed())
				})
			}, defaultNodeTimeout)

			It("should allow objects with replica count above maximum but generate warnings", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				objWithTooManyReplicas := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					WithField("spec", map[string]interface{}{
						"replicas": int64(150), // Above maximum of 100
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"app": "test-app",
							},
						},
					}).
					Build()

				err := warningClient.Create(ctx, objWithTooManyReplicas)
				Expect(err).ToNot(HaveOccurred()) // Should succeed despite validation failure

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, warningClient, objWithTooManyReplicas)).To(Succeed())
				})

				// Verify object was created despite being invalid
				Eventually(kWithCtx(ctx).Get(objWithTooManyReplicas)).WithContext(ctx).Should(Succeed())
				Expect(warningHandler.Messages()).To(ConsistOf(ContainSubstring("Warning: spec.replicas: Invalid value: 150: spec.replicas in body should be less than or equal to 100")))
			}, defaultNodeTimeout)

			It("should allow objects with negative replica count but generate warnings", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				objWithNegativeReplicas := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					WithField("spec", map[string]interface{}{
						"replicas": int64(-1), // Below minimum of 0
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"app": "test-app",
							},
						},
					}).
					Build()

				err := warningClient.Create(ctx, objWithNegativeReplicas)
				Expect(err).ToNot(HaveOccurred()) // Should succeed despite validation failure

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, warningClient, objWithNegativeReplicas)).To(Succeed())
				})

				// Verify object was created despite being invalid
				Eventually(kWithCtx(ctx).Get(objWithNegativeReplicas)).WithContext(ctx).Should(Succeed())
				Expect(warningHandler.Messages()).To(ConsistOf(
					ContainSubstring("Warning: spec.replicas: Invalid value: -1: spec.replicas in body should be greater than or equal to 0"), // Minimum validation
					ContainSubstring("Warning: .spec.replicas: Invalid value: -1: should be a non-negative integer"),                          // Scale subresource validation
				))
			}, defaultNodeTimeout)

			It("should allow status updates with negative readyReplicas but generate warnings", func(ctx context.Context) {
				gvk := schema.GroupVersionKind{
					Group:   compatibilityCRD.Spec.Group,
					Version: compatibilityCRD.Spec.Versions[0].Name,
					Kind:    compatibilityCRD.Spec.Names.Kind,
				}

				// First create the object with valid spec
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					WithField("spec", map[string]interface{}{
						"replicas": int64(3),
						"selector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"app": "test-app",
							},
						},
					}).
					Build()

				Expect(warningClient.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, warningClient, baseObj)).To(Succeed())
				})

				// Wait for object to be created
				Eventually(kWithCtx(ctx).Get(baseObj)).WithContext(ctx).Should(Succeed())

				// Update status with negative readyReplicas - should generate warning but succeed
				statusUpdate := baseObj.DeepCopy()
				statusUpdate.Object["status"] = map[string]interface{}{
					"readyReplicas": int64(-1), // Below minimum of 0
				}

				err := warningClient.Status().Update(ctx, statusUpdate)
				Expect(err).ToNot(HaveOccurred()) // Should succeed despite validation failure

				// Verify status was updated despite being invalid
				Eventually(kWithCtx(ctx).Object(baseObj)).WithContext(ctx).Should(
					HaveField("Object", HaveKeyWithValue("status", HaveKeyWithValue("readyReplicas", int64(-1)))),
				)
				Expect(warningHandler.Messages()).To(ConsistOf(
					ContainSubstring("Warning: status.readyReplicas: Invalid value: -1: status.readyReplicas in body should be greater than or equal to 0"), // Minimum validation
					ContainSubstring("Warning: .status.readyReplicas: Invalid value: -1: should be a non-negative integer"),                                 // Scale subresource validation
				))
			}, defaultNodeTimeout)
		})
	})
})

// expectInvalidCreateEventually is like expectCreateMatchErrorEventually but requires apierrors.IsInvalid.
func expectInvalidCreateEventually(ctx context.Context, c client.Client, obj *unstructured.Unstructured, substr string) {
	GinkgoHelper()

	Eventually(func(g Gomega) error {
		g.Expect(client.IgnoreNotFound(c.Delete(ctx, obj))).To(Succeed())

		err := c.Create(ctx, obj)
		if err == nil {
			g.Expect(c.Delete(ctx, obj)).To(Succeed())
		}

		return err
	}).WithContext(ctx).WithTimeout(defaultEventuallyTimeout).
		Should(SatisfyAll(
			MatchError(apierrors.IsInvalid, "IsInvalid"),
			MatchError(ContainSubstring(substr)),
		))
}

// expectStatusUpdateMatchErrorEventually retries status updates until the apiserver returns an error whose message contains substr.
func expectStatusUpdateMatchErrorEventually(ctx context.Context, baseObj *unstructured.Unstructured, status map[string]interface{}, substr string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(cl.Get(ctx, client.ObjectKeyFromObject(baseObj), baseObj)).To(Succeed())
		statusUpdate := baseObj.DeepCopy()
		statusUpdate.Object["status"] = status
		err := cl.Status().Update(ctx, statusUpdate)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err).To(MatchError(ContainSubstring(substr)))
	}).WithContext(ctx).WithTimeout(defaultEventuallyTimeout).Should(Succeed())
}

func TestCompatibilityRequirementContext(t *testing.T) {
	ctx := t.Context()

	g := NewWithT(t)
	testPath := fmt.Sprintf("%s%s", webhookPrefix, "test-requirement")
	req := &http.Request{}
	req.URL = &url.URL{Path: testPath}

	ctxWithName := compatibilityRequrementIntoContext(ctx, req)
	extractedName := compatibilityRequrementFromContext(ctxWithName)

	g.Expect(extractedName).To(Equal("test-requirement"))
}

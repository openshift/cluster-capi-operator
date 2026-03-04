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

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

// createValidatingWebhookConfig creates a ValidatingWebhookConfiguration for end-to-end testing.
func createValidatingWebhookConfig(crd *apiextensionsv1.CustomResourceDefinition, compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement) *admissionv1.ValidatingWebhookConfiguration {
	webhookPath := fmt.Sprintf("%s%s", WebhookPrefix, compatibilityRequirement.Name)

	// Get webhook server configuration from test environment
	hostPort := fmt.Sprintf("%s:%d", testEnv.WebhookInstallOptions.LocalServingHost, testEnv.WebhookInstallOptions.LocalServingPort)
	webhookURL := fmt.Sprintf("https://%s%s", hostPort, webhookPath)

	return &admissionv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("test-object-validation-%s", compatibilityRequirement.Name),
		},
		Webhooks: []admissionv1.ValidatingWebhook{
			{
				Name: fmt.Sprintf("object-validation.%s.test", compatibilityRequirement.Name),
				ClientConfig: admissionv1.WebhookClientConfig{
					URL:      ptr.To(webhookURL),
					CABundle: testEnv.WebhookInstallOptions.LocalServingCAData,
				},
				Rules: []admissionv1.RuleWithOperations{
					{
						Operations: []admissionv1.OperationType{
							admissionv1.Create,
							admissionv1.Update,
						},
						Rule: admissionv1.Rule{
							APIGroups:   []string{crd.Spec.Group},
							APIVersions: []string{crd.Spec.Versions[0].Name},
							Resources:   []string{crd.Spec.Names.Plural, crd.Spec.Names.Plural + "/status", crd.Spec.Names.Plural + "/scale"},
						},
					},
				},
				AdmissionReviewVersions: []string{"v1", "v1beta1"},
				SideEffects:             ptr.To(admissionv1.SideEffectClassNone),
				FailurePolicy:           ptr.To(admissionv1.Fail),
			},
		},
	}
}

var _ = Describe("End-to-End Admission Webhook Integration", Ordered, ContinueOnFailure, func() {
	var (
		ctx                      context.Context
		namespace                string
		baseCRD                  *apiextensionsv1.CustomResourceDefinition
		compatibilityCRD         *apiextensionsv1.CustomResourceDefinition
		compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
		startWebhookServer       func()
	)

	BeforeAll(func() {
		// Initialize validator and webhook server for all tests
		_, startWebhookServer = InitValidator(context.Background())
		startWebhookServer()
	})

	BeforeEach(func() {
		ctx = context.Background()
		namespace = "default"

		// Create base CRD with test fields and subresources and install it
		specReplicasPath := ".spec.replicas"
		statusReplicasPath := ".status.readyReplicas"
		labelSelectorPath := ".status.selector"

		// Define status properties using schema builder pattern
		statusProperties := map[string]apiextensionsv1.JSONSchemaProps{
			"phase": *test.NewStringSchema().
				WithStringEnum("Ready", "Pending", "Failed").
				Build(),
			"readyReplicas": *test.NewIntegerSchema().
				WithMinimum(0).
				Build(),
			"selector": *test.NewStringSchema().
				Build(),
			"conditions": *test.NewArraySchema().
				WithObjectItems(
					test.NewObjectSchema().
						WithRequiredStringProperty("type").
						WithRequiredStringProperty("status"),
				).
				Build(),
		}

		// Define spec properties using schema builder pattern
		specProperties := map[string]apiextensionsv1.JSONSchemaProps{
			"replicas": *test.NewIntegerSchema().
				WithMinimum(0).
				WithMaximum(100).
				Build(),
			"selector": *test.NewObjectSchema().
				WithObjectProperty("matchLabels",
					test.NewObjectSchema().
						WithAdditionalPropertiesSchema(test.NewStringSchema()),
				).
				Build(),
		}

		compatibilityCRD = test.NewCRDSchemaBuilder().
			WithStringProperty("testField").
			WithRequiredStringProperty("requiredField").
			WithIntegerProperty("optionalNumber").
			WithStatusSubresource(statusProperties).
			WithScaleSubresource(&specReplicasPath, &statusReplicasPath, &labelSelectorPath).
			WithObjectProperty("spec", specProperties).
			WithObjectProperty("status", statusProperties).
			Build()

		// Deepcopy here as when we use the baseCRD for create/read it wipes the type meta.
		// Set spec and status to empty schemas with preserve unknown fields so that the only validation applied is the compatibility requirement.
		baseCRD = compatibilityCRD.DeepCopy()
		baseCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = *test.NewObjectSchema().WithXPreserveUnknownFields(true).Build()
		baseCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"] = *test.NewObjectSchema().WithXPreserveUnknownFields(true).Build()

		// Install the CRD in the test environment
		Expect(cl.Create(ctx, baseCRD.DeepCopy())).To(Succeed())

		DeferCleanup(func() {
			Expect(test.CleanupAndWait(ctx, cl, baseCRD)).To(Succeed())
		})

		// Wait for CRD to be established
		Eventually(komega.Object(baseCRD)).Should(HaveField("Status.Conditions", test.HaveCondition("Established").WithStatus(apiextensionsv1.ConditionTrue)))
	})

	Describe("Schema Matching Scenarios", func() {
		Context("when schemas match exactly", func() {

			BeforeEach(func() {
				// Create and store the compatibility requirement
				compatibilityRequirement = test.GenerateTestCompatibilityRequirement(compatibilityCRD.DeepCopy())
				Expect(cl.Create(ctx, compatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration to enable end-to-end testing
				webhookConfig := createValidatingWebhookConfig(baseCRD, compatibilityRequirement)
				Expect(cl.Create(ctx, webhookConfig)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl,
						webhookConfig,
						compatibilityRequirement,
					)).To(Succeed())
				})
			})

			It("should allow valid objects through API server", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
				}

				validObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "valid-value").
					WithField("testField", "test-value").
					WithField("optionalNumber", int64(42)).
					Build()

				// This should succeed - object conforms to schema
				Expect(cl.Create(ctx, validObj)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, validObj)).To(Succeed())
				})

				// Verify object was created successfully
				Eventually(komega.Get(validObj)).Should(Succeed())
			})

			It("should reject invalid objects through API server", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
				}

				invalidObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("testField", "value").
					// Missing requiredField - should be rejected
					Build()

				// This should fail due to validation webhook
				err := cl.Create(ctx, invalidObj)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("requiredField: Required value")))
			})
		})
	})

	Describe("Tighter Validation Scenarios", func() {
		Context("when compatibility requirement has stricter validation than live CRD", func() {
			var (
				tighterCRD                      *apiextensionsv1.CustomResourceDefinition
				tighterCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
			)

			BeforeEach(func() {
				// Create a CRD with tighter validation (more required fields)
				tighterCRD = test.FromCRD(compatibilityCRD.DeepCopy()).
					WithRequiredField("testField").      // Make optional field required
					WithRequiredField("optionalNumber"). // Make optional field required
					Build()

				tighterCompatibilityRequirement = test.GenerateTestCompatibilityRequirement(tighterCRD)
				Expect(cl.Create(ctx, tighterCompatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration to enable end-to-end testing
				webhookConfig := createValidatingWebhookConfig(tighterCRD, tighterCompatibilityRequirement)
				Expect(cl.Create(ctx, webhookConfig)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl,
						webhookConfig,
						tighterCompatibilityRequirement,
					)).To(Succeed())
				})
			})

			It("should reject objects missing newly required fields through API server", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
				}

				objMissingField := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					// Missing testField which is now required in tighter compatibility requirement
					Build()

				// Configure webhook to use the tighter compatibility requirement
				err := cl.Create(ctx, objMissingField)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("testField: Required value, optionalNumber: Required value")))
			})

			It("should allow objects with all required fields through API server", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
				}

				completeObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					WithField("optionalNumber", int64(42)).
					Build()

				// This should succeed with tighter validation
				Expect(cl.Create(ctx, completeObj)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, completeObj)).To(Succeed())
				})

				Eventually(komega.Get(completeObj)).Should(Succeed())
			})
		})
	})

	Describe("Looser Validation Scenarios", func() {
		Context("when live CRD has stricter validation than compatibility requirement", func() {
			var (
				looserCRD                      *apiextensionsv1.CustomResourceDefinition
				looserCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
			)

			BeforeEach(func() {
				// Create a CRD with looser validation (fewer required fields)
				looserCRD = test.FromCRD(compatibilityCRD.DeepCopy()).
					RemoveRequiredField("requiredField").
					Build()

				looserCompatibilityRequirement = test.GenerateTestCompatibilityRequirement(looserCRD)
				Expect(cl.Create(ctx, looserCompatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration to enable end-to-end testing
				webhookConfig := createValidatingWebhookConfig(looserCRD, looserCompatibilityRequirement)
				Expect(cl.Create(ctx, webhookConfig)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl,
						webhookConfig,
						looserCompatibilityRequirement,
					)).To(Succeed())
				})
			})

			It("should allow objects matching tighter validation through API server", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
				}

				objWithExtraProperty := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					Build()

				Expect(cl.Create(ctx, objWithExtraProperty)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, objWithExtraProperty)).To(Succeed())
				})

				Eventually(komega.Get(objWithExtraProperty)).Should(Succeed())
			})

			It("should not allow objects without required fields through API server", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
				}

				objMissingFormerlyRequired := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("testField", "value").
					// Missing requiredField which is no longer required in looser compatibility requirement
					Build()

				// This should fail as the field is still required in the live CRD.
				err := cl.Create(ctx, objMissingFormerlyRequired)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("requiredField: Required value")))
			})
		})
	})

	Describe("Update Operations - Schema Compatibility Testing", func() {
		var existingObj *unstructured.Unstructured

		BeforeEach(func() {
			gvk := schema.GroupVersionKind{
				Group:   baseCRD.Spec.Group,
				Version: baseCRD.Spec.Versions[0].Name,
				Kind:    baseCRD.Spec.Names.Kind,
			}

			existingObj = test.NewTestObject(gvk).
				WithNamespace(namespace).
				WithField("requiredField", "initial-value").
				WithField("testField", "initial-test").
				Build()

			Expect(cl.Create(ctx, existingObj)).To(Succeed())

			// Wait for object to be created before proceeding
			Eventually(komega.Get(existingObj)).Should(Succeed())

			DeferCleanup(func() {
				Expect(test.CleanupAndWait(ctx, cl, existingObj)).To(Succeed())
			})
		})

		Context("basic update validation", func() {
			It("should allow valid updates through API server", func() {
				// Update with valid changes
				updatedObj := existingObj.DeepCopy()
				updatedObj.Object["testField"] = "updated-test"

				Expect(cl.Update(ctx, updatedObj)).To(Succeed())

				// Verify update was applied
				Eventually(komega.Object(existingObj)).Should(
					HaveField("Object", HaveKeyWithValue("testField", "updated-test")),
				)
			})

			It("should reject invalid updates through API server", func() {
				// Update to remove required field
				invalidUpdate := existingObj.DeepCopy()
				delete(invalidUpdate.Object, "requiredField") // Remove required field

				err := cl.Update(ctx, invalidUpdate)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err.Error()).To(ContainSubstring("required"))
			})
		})

		Context("tighter validation on updates (CompatibilityRequirement stricter than live CRD)", func() {
			var (
				tighterCRD                      *apiextensionsv1.CustomResourceDefinition
				tighterCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
				tighterWebhookConfig            *admissionv1.ValidatingWebhookConfiguration
			)

			BeforeEach(func() {
				// Create a CRD with tighter validation (more required fields)
				tighterCRD = test.FromCRD(compatibilityCRD.DeepCopy()).
					WithRequiredField("testField").
					WithRequiredField("optionalNumber").
					Build()

				tighterCompatibilityRequirement = test.GenerateTestCompatibilityRequirement(tighterCRD)
				tighterCompatibilityRequirement.Name = fmt.Sprintf("tighter-%s", baseCRD.Name)
				Expect(cl.Create(ctx, tighterCompatibilityRequirement)).To(Succeed())

				// Create separate webhook config for tighter validation
				tighterWebhookConfig = createValidatingWebhookConfig(baseCRD, tighterCompatibilityRequirement)
				tighterWebhookConfig.ObjectMeta.Name = fmt.Sprintf("test-tighter-validation-%s", tighterCompatibilityRequirement.Name)
				Expect(cl.Create(ctx, tighterWebhookConfig)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, tighterWebhookConfig, tighterCompatibilityRequirement)).To(Succeed())
				})
			})

			It("should reject updates that remove newly required fields", func() {
				// Try to update by removing a field that's required in the tighter compatibility requirement
				updateMissingField := existingObj.DeepCopy()
				delete(updateMissingField.Object, "testField") // Remove field required by tighter validation
				// Optional number was also changed to required but wasn't present originally, so will flag.

				err := cl.Update(ctx, updateMissingField)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("testField: Required value, optionalNumber: Required value")))
			})

			It("should allow updates that include all newly required fields", func() {
				// Update with all fields required by tighter validation
				updateWithAllFields := existingObj.DeepCopy()
				updateWithAllFields.Object["testField"] = "updated-test"
				updateWithAllFields.Object["optionalNumber"] = int64(42)

				Expect(cl.Update(ctx, updateWithAllFields)).To(Succeed())

				// Verify update was applied
				Eventually(komega.Object(existingObj)).Should(
					HaveField("Object", HaveKeyWithValue("optionalNumber", int64(42))),
				)
			})
		})
	})

	Describe("Delete Operations", func() {
		It("should allow deletion through API server", func() {
			gvk := schema.GroupVersionKind{
				Group:   baseCRD.Spec.Group,
				Version: baseCRD.Spec.Versions[0].Name,
				Kind:    baseCRD.Spec.Names.Kind,
			}

			objToDelete := test.NewTestObject(gvk).
				WithNamespace(namespace).
				WithField("requiredField", "value").
				Build()

			Expect(cl.Create(ctx, objToDelete)).To(Succeed())

			// Wait for object to be created
			Eventually(komega.Get(objToDelete)).Should(Succeed())

			// Delete should always succeed (no validation on delete)
			Expect(cl.Delete(ctx, objToDelete)).To(Succeed())

			// Verify deletion
			objKey := client.ObjectKey{
				Namespace: objToDelete.GetNamespace(),
				Name:      objToDelete.GetName(),
			}
			Eventually(func() error {
				return cl.Get(ctx, objKey, objToDelete)
			}).Should(MatchError(ContainSubstring("not found")))
		})
	})

	Describe("Status Subresource Validation", func() {
		Context("when status subresource validation is enabled", func() {
			var (
				statusCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
			)

			BeforeEach(func() {
				statusCRD := compatibilityCRD.DeepCopy()
				// Disable the scale subresource for these test cases
				statusCRD.Spec.Versions[0].Subresources.Scale = nil

				// The baseCRD already has status subresource, so we can create a compatibility requirement directly
				statusCompatibilityRequirement = test.GenerateTestCompatibilityRequirement(statusCRD)
				statusCompatibilityRequirement.Name = fmt.Sprintf("status-%s", baseCRD.Name)
				Expect(cl.Create(ctx, statusCompatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration for the compatibility requirement
				statusWebhookConfig := createValidatingWebhookConfig(baseCRD, statusCompatibilityRequirement)
				statusWebhookConfig.ObjectMeta.Name = fmt.Sprintf("test-status-validation-%s", statusCompatibilityRequirement.Name)
				Expect(cl.Create(ctx, statusWebhookConfig)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, statusWebhookConfig, statusCompatibilityRequirement)).To(Succeed())
				})
			})

			It("should allow valid status updates when status validation is enabled", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
				}

				// First create the object without status
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					Build()

				Expect(cl.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				})

				// Wait for object to be created
				Eventually(komega.Get(baseObj)).Should(Succeed())

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
				Eventually(komega.Object(baseObj)).Should(
					HaveField("Object", HaveKeyWithValue("status", HaveKeyWithValue("phase", "Ready"))),
				)
			})

			It("should reject status updates with invalid enum values when status validation is enabled", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
				}

				// First create the object without status
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					Build()

				Expect(cl.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				})

				// Wait for object to be created
				Eventually(komega.Get(baseObj)).Should(Succeed())

				// Now try to update status with invalid enum value
				statusUpdate := baseObj.DeepCopy()
				statusUpdate.Object["status"] = map[string]interface{}{
					"phase": "InvalidPhase", // Not in allowed enum values
				}

				err := cl.Status().Update(ctx, statusUpdate)
				Expect(err).To(MatchError(ContainSubstring("\"test-object\" is invalid: status.phase: Unsupported value: \"InvalidPhase\": supported values: \"Ready\", \"Pending\", \"Failed\"")))
			})

			It("should reject status updates with invalid nested structure when status validation is enabled", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
				}

				// First create the object without status
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					Build()

				Expect(cl.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				})

				// Wait for object to be created
				Eventually(komega.Get(baseObj)).Should(Succeed())

				// Now try to update status with invalid nested structure
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

				err := cl.Status().Update(ctx, statusUpdate)
				Expect(err).To(MatchError(ContainSubstring("\"test-object\" is invalid: status.conditions[0].status: Required value")))
			})

			It("should reject status updates with negative readyReplicas when status validation is enabled", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
				}

				// First create the object without status
				baseObj := test.NewTestObject(gvk).
					WithNamespace(namespace).
					WithField("requiredField", "value").
					WithField("testField", "test-value").
					Build()

				Expect(cl.Create(ctx, baseObj)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				})

				// Wait for object to be created
				Eventually(komega.Get(baseObj)).Should(Succeed())

				// Now try to update status with negative readyReplicas
				statusUpdate := baseObj.DeepCopy()
				statusUpdate.Object["status"] = map[string]interface{}{
					"phase":         "Ready",
					"readyReplicas": int64(-1), // Below minimum value
				}

				err := cl.Status().Update(ctx, statusUpdate)
				Expect(err).To(MatchError(ContainSubstring("\"test-object\" is invalid: .status.readyReplicas: Invalid value: -1: should be a non-negative integer")))
			})
		})
	})

	Describe("Scale Subresource Validation", func() {
		Context("when scale subresource validation is enabled", func() {
			var (
				scaleCompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
			)

			BeforeEach(func() {
				scaleCRD := compatibilityCRD.DeepCopy()
				// Disable the status subresource for these test cases
				scaleCRD.Spec.Versions[0].Subresources.Status = nil

				// The baseCRD already has scale subresource, so we can create a compatibility requirement directly
				scaleCompatibilityRequirement = test.GenerateTestCompatibilityRequirement(scaleCRD)
				scaleCompatibilityRequirement.Name = fmt.Sprintf("scale-%s", baseCRD.Name)
				Expect(cl.Create(ctx, scaleCompatibilityRequirement)).To(Succeed())

				// Create ValidatingWebhookConfiguration for the compatibility requirement
				scaleWebhookConfig := createValidatingWebhookConfig(baseCRD, scaleCompatibilityRequirement)
				scaleWebhookConfig.ObjectMeta.Name = fmt.Sprintf("test-scale-validation-%s", scaleCompatibilityRequirement.Name)
				Expect(cl.Create(ctx, scaleWebhookConfig)).To(Succeed())

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, scaleWebhookConfig, scaleCompatibilityRequirement)).To(Succeed())
				})
			})

			It("should allow objects with valid scale-related fields when scale validation is enabled", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
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

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, validScaledObj)).To(Succeed())
				})

				Eventually(komega.Get(validScaledObj)).Should(Succeed())

				// Update status with valid readyReplicas using status subclient
				statusUpdate := validScaledObj.DeepCopy()
				statusUpdate.Object["status"] = map[string]interface{}{
					"readyReplicas": int64(3),
				}

				Expect(cl.Status().Update(ctx, statusUpdate)).To(Succeed())

				// Verify status was updated
				Eventually(komega.Object(validScaledObj)).Should(
					HaveField("Object", HaveKeyWithValue("status", HaveKeyWithValue("readyReplicas", int64(3)))),
				)
			})

			It("should reject objects with replica count above maximum when scale validation is enabled", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
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

				err := cl.Create(ctx, objWithTooManyReplicas)
				Expect(err).To(MatchError(ContainSubstring("\"test-object\" is invalid: spec.replicas: Invalid value: 150: spec.replicas in body should be less than or equal to 100")))
			})

			It("should reject objects with negative replica count when scale validation is enabled", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
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

				err := cl.Create(ctx, objWithNegativeReplicas)
				Expect(err).To(MatchError(ContainSubstring("\"test-object\" is invalid: .spec.replicas: Invalid value: -1: should be a non-negative integer")))
			})

			It("should reject status updates with negative readyReplicas when scale validation is enabled", func() {
				gvk := schema.GroupVersionKind{
					Group:   baseCRD.Spec.Group,
					Version: baseCRD.Spec.Versions[0].Name,
					Kind:    baseCRD.Spec.Names.Kind,
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

				DeferCleanup(func() {
					Expect(test.CleanupAndWait(ctx, cl, baseObj)).To(Succeed())
				})

				// Wait for object to be created
				Eventually(komega.Get(baseObj)).Should(Succeed())

				// Now try to update status with negative readyReplicas
				statusUpdate := baseObj.DeepCopy()
				statusUpdate.Object["status"] = map[string]interface{}{
					"readyReplicas": int64(-1), // Below minimum of 0
				}

				err := cl.Status().Update(ctx, statusUpdate)
				Expect(err).To(MatchError(ContainSubstring("\"test-object\" is invalid: .status.readyReplicas: Invalid value: -1: should be a non-negative integer")))
			})
		})
	})
})

func TestCompatibilityRequirementContext(t *testing.T) {
	ctx := t.Context()

	g := NewWithT(t)
	testPath := fmt.Sprintf("%s%s", WebhookPrefix, "test-requirement")
	req := &http.Request{}
	req.URL = &url.URL{Path: testPath}

	ctxWithName := compatibilityRequrementIntoContext(ctx, req)
	extractedName := compatibilityRequrementFromContext(ctxWithName)

	g.Expect(extractedName).To(Equal("test-requirement"))
}

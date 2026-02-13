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

// createValidatingWebhookConfig creates a ValidatingWebhookConfiguration for end-to-end testing
func createValidatingWebhookConfig(crd *apiextensionsv1.CustomResourceDefinition, compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement) *admissionv1.ValidatingWebhookConfiguration {
	webhookPath := fmt.Sprintf("%s%s", webhookPrefix, compatibilityRequirement.Name)

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
							Resources:   []string{crd.Spec.Names.Plural},
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

		// Create base CRD with test fields and install it
		baseCRD = test.NewCRDSchemaBuilder().
			WithStringProperty("testField").
			WithRequiredStringProperty("requiredField").
			WithIntegerProperty("optionalNumber").
			Build()

		// Deepcopy here as when we use the baseCRD for create/read it wipes the type meta.
		compatibilityCRD = baseCRD.DeepCopy()

		// Install the CRD in the test environment
		Expect(cl.Create(ctx, baseCRD.DeepCopy())).To(Succeed())

		// Wait for CRD to be established
		Eventually(komega.Object(baseCRD)).Should(HaveField("Status.Conditions", ContainElement(
			HaveField("Type", BeEquivalentTo("Established")),
		)))

		DeferCleanup(func() {
			test.CleanupAndWait(ctx, cl, baseCRD)
		})
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
					test.CleanupAndWait(ctx, cl,
						webhookConfig,
						compatibilityRequirement,
					)
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
					test.CleanupAndWait(ctx, cl, validObj)
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
					test.CleanupAndWait(ctx, cl,
						webhookConfig,
						tighterCompatibilityRequirement,
					)
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
					test.CleanupAndWait(ctx, cl, completeObj)
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
					test.CleanupAndWait(ctx, cl,
						webhookConfig,
						looserCompatibilityRequirement,
					)
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
					test.CleanupAndWait(ctx, cl, objWithExtraProperty)
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

				// This should succeed with looser validation
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
				test.CleanupAndWait(ctx, cl, existingObj)
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
					test.CleanupAndWait(ctx, cl, tighterWebhookConfig, tighterCompatibilityRequirement)
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

	Describe("Webhook Configuration and Context Management", func() {
		It("should properly extract CompatibilityRequirement name from webhook path", func() {
			// This test verifies the webhook URL path processing works correctly
			// The webhook is configured with path patterns that include the CompatibilityRequirement name

			testPath := fmt.Sprintf("%s%s", webhookPrefix, compatibilityRequirement.Name)
			req := &http.Request{}
			req.URL = &url.URL{Path: testPath}

			ctxWithName := compatibilityRequrementIntoContext(ctx, req)
			extractedName := compatibilityRequrementFromContext(ctxWithName)

			Expect(extractedName).To(Equal(compatibilityRequirement.Name))
		})
	})
})

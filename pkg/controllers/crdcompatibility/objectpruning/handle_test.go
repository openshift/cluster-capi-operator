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

package objectpruning

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	k8syaml "sigs.k8s.io/yaml"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

// pruningTestScenario defines a test case for object pruning through the webhook.
type pruningTestScenario struct {
	CompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
	InputObject              map[string]interface{}
	ExpectedObject           map[string]interface{} // Fields that should remain after pruning
}

var _ = Describe("Object Pruning Integration", func() {
	var (
		namespace string
		seed      int64
		liveCRD   *apiextensionsv1.CustomResourceDefinition
	)

	BeforeEach(func(ctx context.Context) {
		seed = GinkgoRandomSeed()
		namespaceTmpl := fmt.Sprintf("test-ns-%d", seed)

		// Create test namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: namespaceTmpl,
			},
		}
		Expect(cl.Create(ctx, ns)).To(Succeed())
		namespace = ns.GetName()
	}, defaultNodeTimeout)

	Context("admission pruning scenarios", func() {
		BeforeEach(func(ctx context.Context) {
			By("Creating the live CRD with permissive schema")

			liveCRD = createPermissivePropertiesCRDSchema()
			Expect(cl.Create(ctx, liveCRD)).To(Succeed())

			DeferCleanup(func(ctx context.Context) {
				Expect(test.CleanupAndWait(ctx, cl, liveCRD)).To(Succeed())
			}, defaultNodeTimeout)

			By("Waiting for CRD to be established")
			Eventually(kWithCtx(ctx).Object(liveCRD)).WithContext(ctx).Should(
				HaveField("Status.Conditions", test.HaveCondition("Established").WithStatus(apiextensionsv1.ConditionTrue)),
			)
		}, defaultNodeTimeout)

		DescribeTable("object pruning scenarios through API server",
			func(ctx context.Context, scenario pruningTestScenario) {
				By("Creating the CompatibilityRequirement")

				scenario.CompatibilityRequirement.Name = fmt.Sprintf("test-compat-req-%d", seed)
				Expect(cl.Create(ctx, scenario.CompatibilityRequirement)).To(Succeed())

				By("Creating MutatingWebhookConfiguration")

				webhookConfig := createMutatingWebhookConfig(scenario.CompatibilityRequirement, liveCRD)
				Expect(cl.Create(ctx, webhookConfig)).To(Succeed())

				By("Creating object through API server (should be pruned by webhook)")
				// Set the namespace and ensure object matches the CRD GVK
				gvk := liveCRD.Spec.Versions[0].Name
				inputObject := (&unstructured.Unstructured{
					Object: scenario.InputObject,
				}).DeepCopy()
				inputObject.SetAPIVersion(fmt.Sprintf("%s/%s", liveCRD.Spec.Group, gvk))
				inputObject.SetKind(liveCRD.Spec.Names.Kind)
				inputObject.SetNamespace(namespace)
				inputObject.SetName(fmt.Sprintf("test-pruning-%d", GinkgoRandomSeed()))

				// Status must be handled separately because it's a subresource.
				status, hasStatus, _ := unstructured.NestedMap(inputObject.Object, "status")
				if hasStatus {
					delete(inputObject.Object, "status")
				}

				DeferCleanup(func(ctx context.Context) {
					Expect(test.CleanupAndWait(ctx, cl,
						inputObject,
						webhookConfig,
						scenario.CompatibilityRequirement,
					)).To(Succeed())
				})

				// Create object through API server - webhook should prune it
				Expect(cl.Create(ctx, inputObject)).To(Succeed())

				// Write the status through the status subresource.
				if hasStatus {
					inputObject.Object["status"] = status
					Expect(cl.Status().Update(ctx, inputObject)).To(Succeed())
				}

				By("Verifying the object was pruned correctly", func() {
					retrievedObj := &unstructured.Unstructured{}
					retrievedObj.SetGroupVersionKind(inputObject.GroupVersionKind())
					retrievedObj.SetName(inputObject.GetName())
					retrievedObj.SetNamespace(inputObject.GetNamespace())

					Eventually(kWithCtx(ctx).Get(retrievedObj)).WithContext(ctx).Should(Succeed())

					Expect(retrievedObj.Object).To(test.IgnoreFields([]string{"apiVersion", "kind", "metadata"}, Equal(scenario.ExpectedObject)), "Expected object to be pruned correctly")
				})

				By("Attempting to update the object, should prune the object again", func() {
					inputObject.Object["spec"] = scenario.InputObject["spec"]
					Expect(cl.Update(ctx, inputObject)).To(Succeed())
				})

				// Write the status through the status subresource.
				if hasStatus {
					inputObject.Object["status"] = status
					Expect(cl.Status().Update(ctx, inputObject)).To(Succeed())
				}

				By("Verifying the object was pruned correctly", func() {
					retrievedObj := &unstructured.Unstructured{}
					retrievedObj.SetGroupVersionKind(inputObject.GroupVersionKind())
					retrievedObj.SetName(inputObject.GetName())
					retrievedObj.SetNamespace(inputObject.GetNamespace())

					Eventually(kWithCtx(ctx).Get(retrievedObj)).WithContext(ctx).Should(Succeed())

					Expect(retrievedObj.Object).To(test.IgnoreFields([]string{"apiVersion", "kind", "metadata"}, Equal(scenario.ExpectedObject)), "Expected object to be pruned correctly")
				})

				By("Updating the compatibility requirement to warn action")
				Eventually(kWithCtx(ctx).Update(scenario.CompatibilityRequirement, func() {
					scenario.CompatibilityRequirement.Spec.ObjectSchemaValidation.Action = apiextensionsv1alpha1.CRDAdmitActionWarn
				})).WithContext(ctx).Should(Succeed())

				By("Updating the object again, should not be pruned", func() {
					inputObject.Object["spec"] = scenario.InputObject["spec"]
					Expect(cl.Update(ctx, inputObject)).To(Succeed())
				})

				// Write the status through the status subresource.
				if hasStatus {
					inputObject.Object["status"] = status
					Expect(cl.Status().Update(ctx, inputObject)).To(Succeed())
				}

				By("Verifying the object was not pruned", func() {
					retrievedObj := &unstructured.Unstructured{}
					retrievedObj.SetGroupVersionKind(inputObject.GroupVersionKind())
					retrievedObj.SetName(inputObject.GetName())
					retrievedObj.SetNamespace(inputObject.GetNamespace())

					Eventually(kWithCtx(ctx).Get(retrievedObj)).WithContext(ctx).Should(Succeed())

					Expect(retrievedObj.Object).To(test.IgnoreFields([]string{"apiVersion", "kind", "metadata"}, Equal(scenario.InputObject)), "Expected object to be not pruned")
				})
			},

			Entry("object with unknown fields pruned by strict compatibility requirement", pruningTestScenario{
				CompatibilityRequirement: createCompatibilityRequirement(createStrictCRDSchema()),
				InputObject: map[string]interface{}{
					"spec": map[string]interface{}{
						"allowedField": "keepThis",
						"unknownField": "removeThis",
						"extraField":   "alsoRemove",
					},
					"status": map[string]interface{}{
						"phase":         "Running",
						"unknownStatus": "removeThis",
					},
				},
				ExpectedObject: map[string]interface{}{
					"spec": map[string]interface{}{
						"allowedField": "keepThis",
					},
					"status": map[string]interface{}{
						"phase": "Running",
					},
				},
			}, defaultNodeTimeout),

			Entry("object with unknown fields preserved by permissive compatibility requirement", pruningTestScenario{
				CompatibilityRequirement: createCompatibilityRequirement(createPermissiveCRDSchema()),
				InputObject: map[string]interface{}{
					"spec": map[string]interface{}{
						"allowedField": "keepThis",
						"unknownField": "alsoKeepThis",
						"extraField":   "keepThisToo",
					},
				},
				ExpectedObject: map[string]interface{}{
					"spec": map[string]interface{}{
						"allowedField": "keepThis",
						"unknownField": "alsoKeepThis",
						"extraField":   "keepThisToo",
					},
				},
			}, defaultNodeTimeout),

			Entry("nested object with mixed field preservation", pruningTestScenario{
				CompatibilityRequirement: createCompatibilityRequirement(createNestedCRDSchema()),
				InputObject: map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{
								"name": "template-name",
								"labels": map[string]interface{}{
									"app":     "myapp",
									"version": "v1.0",
									"custom":  "customValue",
								},
								"annotations": "removeThis",
							},
							"spec": map[string]interface{}{
								"replicas":    int64(3),
								"unknownSpec": "removeThis", // Should be removed
							},
						},
						"unknownRoot": "removeThis", // Should be removed
					},
				},
				ExpectedObject: map[string]interface{}{
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{
								"name": "template-name",
								"labels": map[string]interface{}{
									"app":     "myapp",
									"version": "v1.0",
									"custom":  "customValue",
								},
							},
							"spec": map[string]interface{}{
								"replicas": int64(3),
							},
						},
					},
				},
			}, defaultNodeTimeout),

			Entry("object with array containing objects to be pruned", pruningTestScenario{
				CompatibilityRequirement: createCompatibilityRequirement(createArrayCRDSchema()),
				InputObject: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":         "nginx",
								"image":        "nginx:latest",
								"unknownProp1": "removeThis",
							},
							map[string]interface{}{
								"name":         "sidecar",
								"image":        "sidecar:v1",
								"unknownProp2": "alsoRemove",
							},
						},
						"unknownTop": "removeThis",
					},
				},
				ExpectedObject: map[string]interface{}{
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{
								"name":  "nginx",
								"image": "nginx:latest",
							},
							map[string]interface{}{
								"name":  "sidecar",
								"image": "sidecar:v1",
							},
						},
					},
				},
			}, defaultNodeTimeout),

			Entry("object with no properties defined in schema removes all non-standard fields", pruningTestScenario{
				CompatibilityRequirement: createCompatibilityRequirement(createEmptyPropertiesCRDSchema()),
				InputObject: map[string]interface{}{
					"spec": map[string]interface{}{
						"field1": "removeThis",
						"field2": "alsoRemove",
						"field3": int64(42),
					},
					"status": map[string]interface{}{
						"phase": "Running",
					},
				},
				ExpectedObject: map[string]interface{}{
					"spec":   map[string]interface{}{},
					"status": map[string]interface{}{},
				},
			}, defaultNodeTimeout),
		)
	})

	Context("error scenarios", func() {
		BeforeEach(func(ctx context.Context) {
			By("Creating a live CRD with permissive schema")

			liveCRD = createEmptyPropertiesCRDSchema()
			Expect(cl.Create(ctx, liveCRD)).To(Succeed())

			By("Waiting for CRD to be established")
			Eventually(kWithCtx(ctx).Object(liveCRD)).WithContext(ctx).Should(
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo("Established")),
					HaveField("Status", BeEquivalentTo(metav1.ConditionTrue)),
				))))

			DeferCleanup(func(ctx context.Context) {
				Expect(test.CleanupAndWait(ctx, cl, liveCRD)).To(Succeed())
			})
		}, defaultNodeTimeout)

		It("should handle webhook when CompatibilityRequirement does not exist", func(ctx context.Context) {
			By("Creating MutatingWebhookConfiguration with non-existent CompatibilityRequirement")

			webhookConfig := createMutatingWebhookConfig(&apiextensionsv1alpha1.CompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{Name: "non-existent-compat-req"},
			}, liveCRD)
			Expect(cl.Create(ctx, webhookConfig)).To(Succeed())

			DeferCleanup(func(ctx context.Context) {
				Expect(test.CleanupAndWait(ctx, cl, webhookConfig)).To(Succeed())
			}, defaultNodeTimeout)

			By("Attempting to create object through API server")

			testObj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": fmt.Sprintf("%s/%s", liveCRD.Spec.Group, liveCRD.Spec.Versions[0].Name),
					"kind":       liveCRD.Spec.Names.Kind,
					"metadata": map[string]interface{}{
						"name":      "test-error-object",
						"namespace": namespace,
					},
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
			}

			By("Verifying error response")

			err := cl.Create(ctx, testObj)
			Expect(err).To(MatchError(ContainSubstring("CompatibilityRequirement.apiextensions.openshift.io \"non-existent-compat-req\" not found")))
		}, defaultNodeTimeout)
	})
})

// createMutatingWebhookConfig creates a MutatingWebhookConfiguration for testing.
func createMutatingWebhookConfig(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, crd *apiextensionsv1.CustomResourceDefinition) *admissionregistrationv1.MutatingWebhookConfiguration {
	webhookPath := fmt.Sprintf("%s%s", webhookPrefix, compatibilityRequirement.Name)
	hostPort := fmt.Sprintf("%s:%d", testEnv.WebhookInstallOptions.LocalServingHost, testEnv.WebhookInstallOptions.LocalServingPort)
	webhookURL := fmt.Sprintf("https://%s%s", hostPort, webhookPath)

	mutatingWebhookConfig := MutatingWebhookConfigurationFor(compatibilityRequirement, crd)
	mutatingWebhookConfig.Webhooks[0].ClientConfig = admissionregistrationv1.WebhookClientConfig{
		URL:      &webhookURL,
		CABundle: testEnv.WebhookInstallOptions.LocalServingCAData,
	}

	return mutatingWebhookConfig
}

// Helper functions to create different CRD schemas for testing

func createStrictCRDSchema() *apiextensionsv1.CustomResourceDefinition {
	gvk := schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	}

	crd := test.GenerateCRD(gvk)
	crd.Labels = map[string]string{"test-crd": "true"}

	// Add strict schema with specific properties
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties = map[string]apiextensionsv1.JSONSchemaProps{
		"spec": {
			Type: "object",
			Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"allowedField": {Type: "string"},
			},
		},
		"status": {
			Type: "object",
			Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"phase": {Type: "string"},
			},
		},
	}

	return crd
}

func createPermissiveCRDSchema() *apiextensionsv1.CustomResourceDefinition {
	gvk := schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	}

	crd := test.GenerateCRD(gvk)
	crd.Labels = map[string]string{"test-crd": "true"}

	// Add permissive schema that preserves unknown fields
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties = map[string]apiextensionsv1.JSONSchemaProps{
		"spec": {
			Type: "object",
			Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"allowedField": {Type: "string"},
			},
			XPreserveUnknownFields: ptr.To(true), // Allow unknown fields
		},
	}
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.XPreserveUnknownFields = ptr.To(true)

	return crd
}

func createNestedCRDSchema() *apiextensionsv1.CustomResourceDefinition {
	gvk := schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	}

	crd := test.GenerateCRD(gvk)
	crd.Labels = map[string]string{"test-crd": "true"}

	// Add nested schema with mixed preservation rules
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties = map[string]apiextensionsv1.JSONSchemaProps{
		"spec": {
			Type: "object",
			Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"template": {
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"metadata": {
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"name": {Type: "string"},
								"labels": {
									Type:                   "object",
									XPreserveUnknownFields: ptr.To(true), // Labels preserve unknown fields
								},
							},
						},
						"spec": {
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"replicas": {Type: "integer"},
							},
						},
					},
				},
			},
		},
	}

	return crd
}

func createArrayCRDSchema() *apiextensionsv1.CustomResourceDefinition {
	gvk := schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	}

	crd := test.GenerateCRD(gvk)
	crd.Labels = map[string]string{"test-crd": "true"}

	// Add schema with array containing objects
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties = map[string]apiextensionsv1.JSONSchemaProps{
		"spec": {
			Type: "object",
			Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"containers": {
					Type: "array",
					Items: &apiextensionsv1.JSONSchemaPropsOrArray{
						Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"name":  {Type: "string"},
								"image": {Type: "string"},
							},
						},
					},
				},
			},
		},
	}

	return crd
}

func createEmptyPropertiesCRDSchema() *apiextensionsv1.CustomResourceDefinition {
	gvk := schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	}

	crd := test.GenerateCRD(gvk)
	crd.Labels = map[string]string{"test-crd": "true"}

	return crd
}

func createPermissivePropertiesCRDSchema() *apiextensionsv1.CustomResourceDefinition {
	gvk := schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	}

	crd := test.GenerateCRD(gvk)
	crd.Labels = map[string]string{"test-crd": "true"}

	// Remove the schema and set XPreserveUnknownFields to true to allow unknown fields.
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.XPreserveUnknownFields = ptr.To(true)
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties = map[string]apiextensionsv1.JSONSchemaProps{}

	return crd
}

func createCompatibilityRequirement(crd *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1alpha1.CompatibilityRequirement {
	crdBytes, err := k8syaml.Marshal(crd)
	Expect(err).NotTo(HaveOccurred())

	return &apiextensionsv1alpha1.CompatibilityRequirement{
		ObjectMeta: metav1.ObjectMeta{
			// Name will be set by the test
		},
		Spec: apiextensionsv1alpha1.CompatibilityRequirementSpec{
			CompatibilitySchema: apiextensionsv1alpha1.CompatibilitySchema{
				CustomResourceDefinition: apiextensionsv1alpha1.CRDData{
					Type: apiextensionsv1alpha1.CRDDataTypeYAML,
					Data: string(crdBytes),
				},
				RequiredVersions: apiextensionsv1alpha1.APIVersions{
					DefaultSelection: apiextensionsv1alpha1.APIVersionSetTypeAllServed,
				},
			},
			ObjectSchemaValidation: apiextensionsv1alpha1.ObjectSchemaValidation{
				Action: apiextensionsv1alpha1.CRDAdmitActionDeny,
			},
		},
	}
}

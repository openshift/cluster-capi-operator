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
	"go.yaml.in/yaml/v2"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	"github.com/openshift/cluster-capi-operator/pkg/test"
)

// pruningTestScenario defines a test case for object pruning through the webhook
type pruningTestScenario struct {
	CompatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
	InputObject              map[string]interface{}
	ExpectedObject           map[string]interface{} // Fields that should remain after pruning
}

var _ = Describe("Object Pruning Integration", func() {
	var (
		ctx                context.Context
		startWebhookServer func()
		namespace          string
		compatReqName      string
		liveCRD            *apiextensionsv1.CustomResourceDefinition
	)

	Context("admission pruning scenarios", func() {

		BeforeEach(func() {
			ctx = context.Background()
			namespaceTmpl := fmt.Sprintf("test-ns-%d", GinkgoRandomSeed())
			compatReqName = fmt.Sprintf("test-compat-req-%d", GinkgoRandomSeed())

			// Create test namespace
			ns := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Namespace",
					"metadata": map[string]interface{}{
						"generateName": namespaceTmpl,
					},
				},
			}
			Expect(cl.Create(ctx, ns)).To(Succeed())
			namespace = ns.GetName()

			// Initialize validator and webhook server
			_, startWebhookServer = InitValidator(ctx)
			startWebhookServer()

			By("Creating the live CRD with permissive schema")
			liveCRD = createPermissivePropertiesCRDSchema()
			Expect(cl.Create(ctx, liveCRD)).To(Succeed())

			By("Waiting for CRD to be established")
			Eventually(komega.Object(liveCRD)).Should(
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo("Established")),
					HaveField("Status", BeEquivalentTo(metav1.ConditionTrue)),
				))),
			)

			DeferCleanup(func() {
				test.CleanupAndWait(ctx, cl, liveCRD)
			})
		})

		DescribeTable("object pruning scenarios through API server",
			func(scenario pruningTestScenario) {
				By("Creating the CompatibilityRequirement")
				scenario.CompatibilityRequirement.Name = compatReqName
				Expect(cl.Create(ctx, scenario.CompatibilityRequirement)).To(Succeed())

				By("Creating MutatingWebhookConfiguration")
				webhookConfig := createMutatingWebhookConfig(liveCRD, scenario.CompatibilityRequirement)
				Expect(cl.Create(ctx, webhookConfig)).To(Succeed())

				By("Creating object through API server (should be pruned by webhook)")
				// Set the namespace and ensure object matches the CRD GVK
				gvk := liveCRD.Spec.Versions[0].Name
				inputObject := &unstructured.Unstructured{
					Object: scenario.InputObject,
				}
				inputObject.SetAPIVersion(fmt.Sprintf("%s/%s", liveCRD.Spec.Group, gvk))
				inputObject.SetKind(liveCRD.Spec.Names.Kind)
				inputObject.SetNamespace(namespace)
				inputObject.SetName(fmt.Sprintf("test-pruning-%d", GinkgoRandomSeed()))

				// Status must be handled separately because it's a subresource.
				status, hasStatus, _ := unstructured.NestedMap(inputObject.Object, "status")
				if hasStatus {
					delete(inputObject.Object, "status")
				}

				DeferCleanup(func() {
					test.CleanupAndWait(ctx, cl,
						inputObject,
						webhookConfig,
						scenario.CompatibilityRequirement,
					)
				})

				// Create object through API server - webhook should prune it
				Expect(cl.Create(ctx, inputObject)).To(Succeed())

				// Write the status through the status subresource.
				if hasStatus {
					inputObject.Object["status"] = status
					Expect(cl.Status().Update(ctx, inputObject)).To(Succeed())
				}

				By("Verifying the object was pruned correctly")
				retrievedObj := &unstructured.Unstructured{}
				retrievedObj.SetGroupVersionKind(inputObject.GroupVersionKind())
				retrievedObj.SetName(inputObject.GetName())
				retrievedObj.SetNamespace(inputObject.GetNamespace())

				Eventually(komega.Get(retrievedObj)).Should(Succeed())

				Expect(retrievedObj.Object).To(test.IgnoreFields([]string{"apiVersion", "kind", "metadata"}, Equal(scenario.ExpectedObject)))
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
			}),

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
			}),

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
			}),

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
			}),

			Entry("object with no properties defined in schema removes all non-standard fields", pruningTestScenario{
				CompatibilityRequirement: createCompatibilityRequirement(createEmptyPropertiesCRDSchema()),
				InputObject: map[string]interface{}{
					"spec": map[string]interface{}{
						"field1": "removeThis",
						"field2": "alsoRemove",
						"field3": 42,
					},
					"status": map[string]interface{}{
						"phase": "Running",
					},
				},
				ExpectedObject: map[string]interface{}{
					"spec":   map[string]interface{}{},
					"status": map[string]interface{}{},
				},
			}),
		)
	})

	Context("error scenarios", func() {
		It("should handle webhook when CompatibilityRequirement does not exist", func() {
			By("Creating a live CRD with permissive schema")
			liveCRD := createEmptyPropertiesCRDSchema()
			Expect(cl.Create(ctx, liveCRD)).To(Succeed())

			By("Waiting for CRD to be established")
			Eventually(komega.Object(liveCRD)).Should(
				HaveField("Status.Conditions", ContainElement(And(
					HaveField("Type", BeEquivalentTo("Established")),
					HaveField("Status", BeEquivalentTo(metav1.ConditionTrue)),
				))))

			By("Creating MutatingWebhookConfiguration with non-existent CompatibilityRequirement")
			webhookConfig := createMutatingWebhookConfig(liveCRD, &apiextensionsv1alpha1.CompatibilityRequirement{
				ObjectMeta: metav1.ObjectMeta{Name: "non-existent-compat-req"},
			})
			Expect(cl.Create(ctx, webhookConfig)).To(Succeed())

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
			Expect(err).To(HaveOccurred())
			Expect(apierrors.IsInternalError(err) || apierrors.IsBadRequest(err)).To(BeTrue())
		})
	})
})

// createMutatingWebhookConfig creates a MutatingWebhookConfiguration for testing
func createMutatingWebhookConfig(crd *apiextensionsv1.CustomResourceDefinition, compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement) *admissionregistrationv1.MutatingWebhookConfiguration {
	webhookPath := fmt.Sprintf("%s%s", WebhookPrefix, compatibilityRequirement.Name)
	hostPort := fmt.Sprintf("%s:%d", testEnv.WebhookInstallOptions.LocalServingHost, testEnv.WebhookInstallOptions.LocalServingPort)
	webhookURL := fmt.Sprintf("https://%s%s", hostPort, webhookPath)

	return &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-object-pruning-",
			Labels: map[string]string{
				"test-webhook": "true",
			},
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: fmt.Sprintf("object-pruning.test.%s", crd.Spec.Group),
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					URL:      &webhookURL,
					CABundle: testEnv.WebhookInstallOptions.LocalServingCAData,
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Create,
							admissionregistrationv1.Update,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{crd.Spec.Group},
							APIVersions: []string{crd.Spec.Versions[0].Name},
							Resources:   []string{crd.Spec.Names.Plural, crd.Spec.Names.Plural + "/status"},
						},
					},
				},
				FailurePolicy:           ptr.To(admissionregistrationv1.Fail),
				SideEffects:             ptr.To(admissionregistrationv1.SideEffectClassNone),
				AdmissionReviewVersions: []string{"v1", "v1beta1"},
				TimeoutSeconds:          ptr.To(int32(10)),
				ReinvocationPolicy:      ptr.To(admissionregistrationv1.NeverReinvocationPolicy),
				MatchPolicy:             ptr.To(admissionregistrationv1.Equivalent),
				ObjectSelector:          &metav1.LabelSelector{},
				NamespaceSelector:       &metav1.LabelSelector{},
			},
		},
	}
}

// Helper functions to create different CRD schemas for testing

func createStrictCRDSchema() *apiextensionsv1.CustomResourceDefinition {
	gvk := schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	}

	crd := test.GenerateCRD(gvk)
	crd.ObjectMeta.Labels = map[string]string{"test-crd": "true"}

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
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.XPreserveUnknownFields = ptr.To(false)

	return crd
}

func createPermissiveCRDSchema() *apiextensionsv1.CustomResourceDefinition {
	gvk := schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	}

	crd := test.GenerateCRD(gvk)
	crd.ObjectMeta.Labels = map[string]string{"test-crd": "true"}

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
	crd.ObjectMeta.Labels = map[string]string{"test-crd": "true"}

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
	crd.ObjectMeta.Labels = map[string]string{"test-crd": "true"}

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
	crd.ObjectMeta.Labels = map[string]string{"test-crd": "true"}

	return crd
}

func createPermissivePropertiesCRDSchema() *apiextensionsv1.CustomResourceDefinition {
	gvk := schema.GroupVersionKind{
		Group:   "test.example.com",
		Version: "v1",
		Kind:    "TestResource",
	}

	crd := test.GenerateCRD(gvk)
	crd.ObjectMeta.Labels = map[string]string{"test-crd": "true"}

	// Remove the schema and set XPreserveUnknownFields to true to allow unknown fields.
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.XPreserveUnknownFields = ptr.To(true)
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties = map[string]apiextensionsv1.JSONSchemaProps{}

	return crd
}

func createCompatibilityRequirement(crd *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1alpha1.CompatibilityRequirement {
	crdBytes, err := yaml.Marshal(crd)
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
		},
	}
}

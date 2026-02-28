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

package crdcompatibility

import (
	"testing"

	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
)

func TestCRDRequirementValidator_ValidateCreate(t *testing.T) {
	RegisterTestingT(t)

	validator := &crdRequirementValidator{}
	ctx := t.Context()

	testCRD := test.NewCRDSchemaBuilder().
		WithStringProperty("testField").
		WithRequiredStringProperty("requiredField").
		WithIntegerProperty("optionalNumber").
		Build()

	tests := []struct {
		name         string
		obj          runtime.Object
		wantWarnings []string
		wantErr      OmegaMatcher
	}{
		{
			name:    "should validate a valid CompatibilityRequirement",
			obj:     test.GenerateTestCompatibilityRequirement(testCRD),
			wantErr: BeNil(),
		},
		{
			name:    "should reject non-CompatibilityRequirement objects",
			obj:     &apiextensionsv1.CustomResourceDefinition{},
			wantErr: MatchError(ContainSubstring("expected a CompatibilityRequirement")),
		},
		{
			name: "should reject CompatibilityRequirement with invalid YAML",
			obj: func() *apiextensionsv1alpha1.CompatibilityRequirement {
				req := test.GenerateTestCompatibilityRequirement(testCRD)
				req.Spec.CompatibilitySchema.CustomResourceDefinition.Data = "invalid: yaml: [unclosed"

				return req
			}(),
			wantErr: MatchError(ContainSubstring("expected a valid CustomResourceDefinition in YAML format")),
		},
		{
			name: "should reject CompatibilityRequirement with wrong APIVersion",
			obj: func() *apiextensionsv1alpha1.CompatibilityRequirement {
				req := test.GenerateTestCompatibilityRequirement(testCRD)
				invalidCRD := testCRD.DeepCopy()
				invalidCRD.APIVersion = "v1"
				yamlData, _ := yaml.Marshal(invalidCRD)
				req.Spec.CompatibilitySchema.CustomResourceDefinition.Data = string(yamlData)

				return req
			}(),
			wantErr: MatchError(ContainSubstring("expected APIVersion to be apiextensions.k8s.io/v1 and Kind to be CustomResourceDefinition")),
		},
		{
			name: "should reject CompatibilityRequirement with wrong Kind",
			obj: func() *apiextensionsv1alpha1.CompatibilityRequirement {
				req := test.GenerateTestCompatibilityRequirement(testCRD)
				invalidCRD := testCRD.DeepCopy()
				invalidCRD.Kind = "SomethingElse"
				yamlData, _ := yaml.Marshal(invalidCRD)
				req.Spec.CompatibilitySchema.CustomResourceDefinition.Data = string(yamlData)

				return req
			}(),
			wantErr: MatchError(ContainSubstring("expected APIVersion to be apiextensions.k8s.io/v1 and Kind to be CustomResourceDefinition")),
		},
		{
			name: "should reject CompatibilityRequirement with invalid CRD schema",
			obj: func() *apiextensionsv1alpha1.CompatibilityRequirement {
				req := test.GenerateTestCompatibilityRequirement(testCRD)
				invalidCRD := testCRD.DeepCopy()
				// Remove required fields to make CRD invalid
				invalidCRD.Spec = apiextensionsv1.CustomResourceDefinitionSpec{}
				yamlData, _ := yaml.Marshal(invalidCRD)
				req.Spec.CompatibilitySchema.CustomResourceDefinition.Data = string(yamlData)

				return req
			}(),
			wantErr: MatchError(ContainSubstring("spec.group: Required value, spec.scope: Required value, spec.versions: Invalid value: null: must have exactly one version marked as storage version, spec.names.plural: Required value, spec.names.singular: Required value, spec.names.kind: Required value, spec.names.listKind: Required value, status.storedVersions: Invalid value: null: must have at least one stored version")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			compatibilityRequirement := tt.obj.(metav1.Object)
			compatibilityRequirement.SetName("test-requirement")

			gotWarnings, gotErr := validator.ValidateCreate(ctx, compatibilityRequirement.(runtime.Object))

			if tt.wantErr != nil {
				g.Expect(gotErr).To(tt.wantErr)
			} else {
				g.Expect(gotErr).ToNot(HaveOccurred())
			}

			if len(tt.wantWarnings) > 0 {
				g.Expect(gotWarnings).To(ConsistOf(tt.wantWarnings))
			} else {
				g.Expect(gotWarnings).To(BeNil())
			}
		})
	}
}

func TestCRDRequirementValidator_ValidateUpdate(t *testing.T) {
	RegisterTestingT(t)

	validator := &crdRequirementValidator{}
	ctx := t.Context()

	testCRD := test.NewCRDSchemaBuilder().
		WithStringProperty("testField").
		WithRequiredStringProperty("requiredField").
		Build()

	validReq := test.GenerateTestCompatibilityRequirement(testCRD)

	tests := []struct {
		name         string
		oldObj       runtime.Object
		newObj       runtime.Object
		wantWarnings []string
		wantErr      OmegaMatcher
	}{
		{
			name:    "should validate update from valid to valid CompatibilityRequirement",
			oldObj:  validReq,
			newObj:  validReq.DeepCopy(),
			wantErr: BeNil(),
		},
		{
			name:    "should reject update to invalid CompatibilityRequirement",
			oldObj:  validReq,
			newObj:  &apiextensionsv1.CustomResourceDefinition{},
			wantErr: MatchError(ContainSubstring("expected a CompatibilityRequirement")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			oldObj := tt.oldObj.(metav1.Object)
			oldObj.SetName("test-requirement")

			newObj := tt.newObj.(metav1.Object)
			newObj.SetName("test-requirement")

			gotWarnings, gotErr := validator.ValidateUpdate(ctx, oldObj.(runtime.Object), newObj.(runtime.Object))

			if tt.wantErr != nil {
				g.Expect(gotErr).To(tt.wantErr)
			} else {
				g.Expect(gotErr).ToNot(HaveOccurred())
			}

			if len(tt.wantWarnings) > 0 {
				g.Expect(gotWarnings).To(ConsistOf(tt.wantWarnings))
			} else {
				g.Expect(gotWarnings).To(BeNil())
			}
		})
	}
}

func TestExcludedFieldsValidation(t *testing.T) {
	RegisterTestingT(t)

	validator := &crdRequirementValidator{}
	ctx := t.Context()

	// Create a complex schema for testing
	complexSchema := test.NewObjectSchema().
		WithObjectProperty("spec", test.NewObjectSchema().
			WithStringProperty("name").
			WithIntegerProperty("replicas").
			WithObjectProperty("template", test.NewObjectSchema().
				WithObjectProperty("spec", test.NewObjectSchema().
					WithStringProperty("image").
					WithArrayProperty("ports", test.SimpleArraySchema(
						test.NewObjectSchema().
							WithStringProperty("name").
							WithIntegerProperty("port"),
						nil, nil, false)))).
			WithArrayProperty("volumes", test.SimpleArraySchema(
				test.NewObjectSchema().
					WithStringProperty("name").
					WithStringProperty("mountPath"),
				nil, nil, false))).
		WithObjectProperty("status", test.NewObjectSchema().
			WithStringProperty("phase"))

	testCRD := test.NewCRDSchemaBuilder().
		WithSchema(complexSchema.Build()).
		Build()

	// Add a second version for multi-version testing
	v2Version := testCRD.Spec.Versions[0].DeepCopy()
	v2Version.Name = "v2"
	v2Version.Storage = false
	testCRD.Spec.Versions = append(testCRD.Spec.Versions, *v2Version)

	tests := []struct {
		name           string
		excludedFields []apiextensionsv1alpha1.APIExcludedField
		wantErr        OmegaMatcher
	}{
		{
			name: "should accept valid simple field path",
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{
					Path:     "spec.name",
					Versions: []apiextensionsv1alpha1.APIVersionString{"v1"},
				},
			},
			wantErr: BeNil(),
		},
		{
			name: "should accept valid nested field path",
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{
					Path:     "spec.template.spec.image",
					Versions: []apiextensionsv1alpha1.APIVersionString{"v1"},
				},
			},
			wantErr: BeNil(),
		},
		{
			name: "should accept valid array field path",
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{
					Path:     "spec.volumes.name",
					Versions: []apiextensionsv1alpha1.APIVersionString{"v1"},
				},
			},
			wantErr: BeNil(),
		},
		{
			name: "should reject non-existent field path",
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{
					Path:     "spec.nonExistentField",
					Versions: []apiextensionsv1alpha1.APIVersionString{"v1"},
				},
			},
			wantErr: MatchError("spec.compatibilitySchema.excludedFields[0].path: Invalid value: \"spec.nonExistentField\": path not found in schema: desired path ^.spec.nonExistentField, path ^.spec is missing child nonExistentField"),
		},
		{
			name: "should reject non-existent version",
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{
					Path:     "spec.name",
					Versions: []apiextensionsv1alpha1.APIVersionString{"v3"},
				},
			},
			wantErr: MatchError("spec.compatibilitySchema.excludedFields[0].versions[0]: Invalid value: \"v3\": version v3 not found in compatibility schema"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			req := test.GenerateTestCompatibilityRequirement(testCRD)
			req.SetName("test-requirement")
			req.Spec.CompatibilitySchema.ExcludedFields = tt.excludedFields

			_, gotErr := validator.ValidateCreate(ctx, req)

			if tt.wantErr != nil {
				g.Expect(gotErr).To(tt.wantErr)
			} else {
				g.Expect(gotErr).ToNot(HaveOccurred())
			}
		})
	}
}

func TestRequiredVersionsValidation(t *testing.T) {
	RegisterTestingT(t)

	validator := &crdRequirementValidator{}
	ctx := t.Context()

	// Create a multi-version CRD for testing
	multiVersionCRD := test.NewCRDSchemaBuilder().
		WithStringProperty("testField").
		Build()

	// Add multiple versions
	v1beta1Version := multiVersionCRD.Spec.Versions[0].DeepCopy()
	v1beta1Version.Name = "v1beta1"
	v1beta1Version.Storage = false
	v1beta1Version.Served = true

	v2alpha1Version := multiVersionCRD.Spec.Versions[0].DeepCopy()
	v2alpha1Version.Name = "v2alpha1"
	v2alpha1Version.Storage = false
	v2alpha1Version.Served = false

	multiVersionCRD.Spec.Versions = append(multiVersionCRD.Spec.Versions, *v1beta1Version, *v2alpha1Version)

	tests := []struct {
		name             string
		requiredVersions apiextensionsv1alpha1.APIVersions
		wantErr          OmegaMatcher
	}{
		{
			name: "should accept valid additional versions",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection:   apiextensionsv1alpha1.APIVersionSetTypeStorageOnly,
				AdditionalVersions: []apiextensionsv1alpha1.APIVersionString{"v1beta1", "v2alpha1"},
			},
			wantErr: BeNil(),
		},
		{
			name: "should accept empty additional versions",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection: apiextensionsv1alpha1.APIVersionSetTypeAllServed,
			},
			wantErr: BeNil(),
		},
		{
			name: "should reject non-existent additional versions",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection:   apiextensionsv1alpha1.APIVersionSetTypeStorageOnly,
				AdditionalVersions: []apiextensionsv1alpha1.APIVersionString{"v1beta1", "v3alpha1"},
			},
			wantErr: MatchError(ContainSubstring("spec.compatibilitySchema.requiredVersions.additionalVersions[1]: Invalid value: \"v3alpha1\": version v3alpha1 not found in compatibility schema")),
		},
		{
			name: "should reject when only non-existent versions are specified",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection:   apiextensionsv1alpha1.APIVersionSetTypeStorageOnly,
				AdditionalVersions: []apiextensionsv1alpha1.APIVersionString{"v3", "v4"},
			},
			wantErr: MatchError(And(
				ContainSubstring("version v3 not found in compatibility schema"),
				ContainSubstring("version v4 not found in compatibility schema"),
			)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			req := test.GenerateTestCompatibilityRequirement(multiVersionCRD)
			req.SetName("test-requirement")
			req.Spec.CompatibilitySchema.RequiredVersions = tt.requiredVersions

			_, gotErr := validator.ValidateCreate(ctx, req)

			if tt.wantErr != nil {
				g.Expect(gotErr).To(tt.wantErr)
			} else {
				g.Expect(gotErr).ToNot(HaveOccurred())
			}
		})
	}
}

func TestExcludedFieldsWithVersionPruning(t *testing.T) {
	RegisterTestingT(t)

	validator := &crdRequirementValidator{}
	ctx := t.Context()

	// Create multi-version CRD with different serving statuses
	complexSchema := test.NewObjectSchema().
		WithObjectProperty("spec", test.NewObjectSchema().
			WithStringProperty("name").
			WithIntegerProperty("replicas")).
		WithObjectProperty("status", test.NewObjectSchema().
			WithStringProperty("phase"))

	multiVersionCRD := test.NewCRDSchemaBuilder().
		WithSchema(complexSchema.Build()).
		Build()

	// Add versions with different serving/storage states
	v1beta1Version := multiVersionCRD.Spec.Versions[0].DeepCopy()
	v1beta1Version.Name = "v1beta1"
	v1beta1Version.Storage = false
	v1beta1Version.Served = true

	v2alpha1Version := multiVersionCRD.Spec.Versions[0].DeepCopy()
	v2alpha1Version.Name = "v2alpha1"
	v2alpha1Version.Storage = false
	v2alpha1Version.Served = false // This should be pruned for AllServed

	multiVersionCRD.Spec.Versions = append(multiVersionCRD.Spec.Versions, *v1beta1Version, *v2alpha1Version)

	tests := []struct {
		name             string
		requiredVersions apiextensionsv1alpha1.APIVersions
		excludedFields   []apiextensionsv1alpha1.APIExcludedField
		wantErr          OmegaMatcher
	}{
		{
			name: "should accept excluded field version that exists in pruned versions (StorageOnly)",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection: apiextensionsv1alpha1.APIVersionSetTypeStorageOnly,
			},
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{
					Path:     "spec.name",
					Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}, // storage version, should be included
				},
			},
			wantErr: BeNil(),
		},
		{
			name: "should accept excluded field version that exists in pruned versions (AllServed)",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection: apiextensionsv1alpha1.APIVersionSetTypeAllServed,
			},
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{
					Path:     "spec.name",
					Versions: []apiextensionsv1alpha1.APIVersionString{"v1beta1"}, // served version, should be included
				},
			},
			wantErr: BeNil(),
		},
		{
			name: "should reject excluded field version that is pruned (StorageOnly)",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection: apiextensionsv1alpha1.APIVersionSetTypeStorageOnly,
			},
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{
					Path:     "spec.name",
					Versions: []apiextensionsv1alpha1.APIVersionString{"v1beta1"}, // not storage, should be pruned
				},
			},
			wantErr: MatchError(ContainSubstring("version v1beta1 is pruned from the compatibility schema, should not be specified in excludedFields")),
		},
		{
			name: "should reject excluded field version that is pruned (AllServed)",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection: apiextensionsv1alpha1.APIVersionSetTypeAllServed,
			},
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{
					Path:     "spec.name",
					Versions: []apiextensionsv1alpha1.APIVersionString{"v2alpha1"}, // not served, should be pruned
				},
			},
			wantErr: MatchError(ContainSubstring("version v2alpha1 is pruned from the compatibility schema, should not be specified in excludedFields")),
		},
		{
			name: "should accept excluded field with additional versions",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection:   apiextensionsv1alpha1.APIVersionSetTypeStorageOnly,
				AdditionalVersions: []apiextensionsv1alpha1.APIVersionString{"v1beta1"},
			},
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{
					Path:     "spec.name",
					Versions: []apiextensionsv1alpha1.APIVersionString{"v1beta1"}, // included via additional versions
				},
			},
			wantErr: BeNil(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			req := test.GenerateTestCompatibilityRequirement(multiVersionCRD)
			req.SetName("test-requirement")
			req.Spec.CompatibilitySchema.RequiredVersions = tt.requiredVersions
			req.Spec.CompatibilitySchema.ExcludedFields = tt.excludedFields

			_, gotErr := validator.ValidateCreate(ctx, req)

			if tt.wantErr != nil {
				g.Expect(gotErr).To(tt.wantErr)
			} else {
				g.Expect(gotErr).ToNot(HaveOccurred())
			}
		})
	}
}

func TestValidatingWebhookConfigurationValidation(t *testing.T) {
	RegisterTestingT(t)

	validator := &crdRequirementValidator{}
	ctx := t.Context()

	testCRD := test.NewCRDSchemaBuilder().
		WithStringProperty("testField").
		Build()

	tests := []struct {
		name    string
		req     *apiextensionsv1alpha1.CompatibilityRequirement
		wantErr OmegaMatcher
	}{
		{
			name:    "should accept valid webhook configuration",
			req:     test.GenerateTestCompatibilityRequirement(testCRD),
			wantErr: BeNil(),
		},
		{
			name: "should accept webhook configuration with match conditions",
			req: func() *apiextensionsv1alpha1.CompatibilityRequirement {
				req := test.GenerateTestCompatibilityRequirement(testCRD)
				req.Spec.ObjectSchemaValidation.MatchConditions = []admissionregistrationv1.MatchCondition{
					{
						Name:       "test-condition",
						Expression: "object.metadata.name == 'test'",
					},
				}

				return req
			}(),
			wantErr: BeNil(),
		},
		{
			name: "should accept webhook configuration with namespace selector",
			req: func() *apiextensionsv1alpha1.CompatibilityRequirement {
				req := test.GenerateTestCompatibilityRequirement(testCRD)
				req.Spec.ObjectSchemaValidation.NamespaceSelector = metav1.LabelSelector{
					MatchLabels: map[string]string{
						"environment": "production",
					},
				}

				return req
			}(),
			wantErr: BeNil(),
		},
		{
			name: "should accept webhook configuration with object selector",
			req: func() *apiextensionsv1alpha1.CompatibilityRequirement {
				req := test.GenerateTestCompatibilityRequirement(testCRD)
				req.Spec.ObjectSchemaValidation.ObjectSelector = metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app": "test-app",
					},
				}

				return req
			}(),
			wantErr: BeNil(),
		},
		{
			name: "should reject webhook configuration with invalid match condition",
			req: func() *apiextensionsv1alpha1.CompatibilityRequirement {
				req := test.GenerateTestCompatibilityRequirement(testCRD)
				req.Spec.ObjectSchemaValidation.MatchConditions = []admissionregistrationv1.MatchCondition{
					{
						Name:       "", // Invalid: empty name
						Expression: "object.metadata.name == 'test'",
					},
				}

				return req
			}(),
			wantErr: MatchError(ContainSubstring("desiredValidatingWebhookConfiguration is not valid")),
		},
		{
			name: "should reject webhook configuration with invalid namespace selector",
			req: func() *apiextensionsv1alpha1.CompatibilityRequirement {
				req := test.GenerateTestCompatibilityRequirement(testCRD)
				req.Spec.ObjectSchemaValidation.NamespaceSelector = metav1.LabelSelector{
					MatchLabels: map[string]string{
						"": "invalid-empty-key", // Invalid: empty key
					},
				}

				return req
			}(),
			wantErr: MatchError(ContainSubstring("desiredValidatingWebhookConfiguration is not valid")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			tt.req.SetName("test-requirement")

			_, gotErr := validator.ValidateCreate(ctx, tt.req)

			if tt.wantErr != nil {
				g.Expect(gotErr).To(tt.wantErr)
			} else {
				g.Expect(gotErr).ToNot(HaveOccurred())
			}
		})
	}
}

func TestPruneSchemaVersions(t *testing.T) {
	RegisterTestingT(t)

	// Create multi-version CRD for testing
	multiVersionCRD := test.NewCRDSchemaBuilder().
		WithStringProperty("testField").
		Build()

	// Add versions with different serving/storage states
	v1beta1Version := multiVersionCRD.Spec.Versions[0].DeepCopy()
	v1beta1Version.Name = "v1beta1"
	v1beta1Version.Storage = false
	v1beta1Version.Served = true

	v2alpha1Version := multiVersionCRD.Spec.Versions[0].DeepCopy()
	v2alpha1Version.Name = "v2alpha1"
	v2alpha1Version.Storage = false
	v2alpha1Version.Served = false

	v2beta1Version := multiVersionCRD.Spec.Versions[0].DeepCopy()
	v2beta1Version.Name = "v2beta1"
	v2beta1Version.Storage = false
	v2beta1Version.Served = true

	multiVersionCRD.Spec.Versions = append(multiVersionCRD.Spec.Versions, *v1beta1Version, *v2alpha1Version, *v2beta1Version)

	tests := []struct {
		name             string
		requiredVersions apiextensionsv1alpha1.APIVersions
		expectedVersions []string
	}{
		{
			name: "StorageOnly should include only storage version",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection: apiextensionsv1alpha1.APIVersionSetTypeStorageOnly,
			},
			expectedVersions: []string{"v1"}, // only storage version
		},
		{
			name: "AllServed should include all served versions",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection: apiextensionsv1alpha1.APIVersionSetTypeAllServed,
			},
			expectedVersions: []string{"v1", "v1beta1", "v2beta1"}, // all served versions
		},
		{
			name: "StorageOnly with additional versions should include storage + additional",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection:   apiextensionsv1alpha1.APIVersionSetTypeStorageOnly,
				AdditionalVersions: []apiextensionsv1alpha1.APIVersionString{"v1beta1", "v2alpha1"},
			},
			expectedVersions: []string{"v1", "v1beta1", "v2alpha1"},
		},
		{
			name: "AllServed with additional versions should include served + additional",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection:   apiextensionsv1alpha1.APIVersionSetTypeAllServed,
				AdditionalVersions: []apiextensionsv1alpha1.APIVersionString{"v2alpha1"},
			},
			expectedVersions: []string{"v1", "v1beta1", "v2beta1", "v2alpha1"},
		},
		{
			name: "Additional versions should not duplicate existing versions",
			requiredVersions: apiextensionsv1alpha1.APIVersions{
				DefaultSelection:   apiextensionsv1alpha1.APIVersionSetTypeAllServed,
				AdditionalVersions: []apiextensionsv1alpha1.APIVersionString{"v1", "v1beta1"}, // duplicates
			},
			expectedVersions: []string{"v1", "v1beta1", "v2beta1"}, // no duplicates
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			result := pruneSchemaVersions(multiVersionCRD, tt.requiredVersions)

			g.Expect(result.UnsortedList()).To(ContainElements(tt.expectedVersions))
		})
	}
}

func TestValidatePathExists(t *testing.T) {
	RegisterTestingT(t)

	// Create complex schema for comprehensive path testing
	schema := &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"spec": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"name":     {Type: "string"},
					"replicas": {Type: "integer"},
					"template": {
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"spec": {
								Type: "object",
								Properties: map[string]apiextensionsv1.JSONSchemaProps{
									"image": {Type: "string"},
									"ports": {
										Type: "array",
										Items: &apiextensionsv1.JSONSchemaPropsOrArray{
											Schema: &apiextensionsv1.JSONSchemaProps{
												Type: "object",
												Properties: map[string]apiextensionsv1.JSONSchemaProps{
													"name":     {Type: "string"},
													"port":     {Type: "integer"},
													"protocol": {Type: "string"},
												},
											},
										},
									},
								},
							},
						},
					},
					"volumes": {
						Type: "array",
						Items: &apiextensionsv1.JSONSchemaPropsOrArray{
							Schema: &apiextensionsv1.JSONSchemaProps{
								Type: "object",
								Properties: map[string]apiextensionsv1.JSONSchemaProps{
									"name":      {Type: "string"},
									"mountPath": {Type: "string"},
									"readOnly":  {Type: "boolean"},
								},
							},
						},
					},
					"invalidArray": {
						Type: "array",
						// No Items - this will cause validation errors
					},
				},
			},
			"status": {
				Type: "object",
				Properties: map[string]apiextensionsv1.JSONSchemaProps{
					"phase": {Type: "string"},
					"conditions": {
						Type: "array",
						Items: &apiextensionsv1.JSONSchemaPropsOrArray{
							Schema: &apiextensionsv1.JSONSchemaProps{
								Type: "object",
								Properties: map[string]apiextensionsv1.JSONSchemaProps{
									"type":   {Type: "string"},
									"status": {Type: "string"},
									"reason": {Type: "string"},
								},
							},
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name     string
		path     []string
		wantErr  bool
		errMatch string
	}{
		{
			name:    "valid simple path",
			path:    []string{"spec", "name"},
			wantErr: false,
		},
		{
			name:    "valid nested object path",
			path:    []string{"spec", "template", "spec", "image"},
			wantErr: false,
		},
		{
			name:    "valid array item path",
			path:    []string{"spec", "volumes", "name"},
			wantErr: false,
		},
		{
			name:    "valid nested array path",
			path:    []string{"spec", "template", "spec", "ports", "protocol"},
			wantErr: false,
		},
		{
			name:    "valid deep array path",
			path:    []string{"status", "conditions", "type"},
			wantErr: false,
		},
		{
			name:     "invalid - missing intermediate property",
			path:     []string{"spec", "nonExistent", "field"},
			wantErr:  true,
			errMatch: "path ^.spec is missing child nonExistent",
		},
		{
			name:     "invalid - path through scalar",
			path:     []string{"spec", "name", "nonExistent"},
			wantErr:  true,
			errMatch: "path ^.spec.name is not an object",
		},
		{
			name:     "invalid - array without items schema",
			path:     []string{"spec", "invalidArray", "field"},
			wantErr:  true,
			errMatch: "path ^.spec.invalidArray is an array but does not have an items schema",
		},
		{
			name:     "invalid - missing child in array items",
			path:     []string{"spec", "volumes", "nonExistentField"},
			wantErr:  true,
			errMatch: "path ^.spec.volumes is missing child nonExistentField",
		},
		{
			name:     "invalid - completely missing root path",
			path:     []string{"nonExistentRoot"},
			wantErr:  true,
			errMatch: "path ^ is missing child nonExistentRoot",
		},
		{
			name:    "valid - single level path",
			path:    []string{"spec"},
			wantErr: false,
		},
		{
			name:    "valid - status path",
			path:    []string{"status", "phase"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			err := validatePathExists(schema, tt.path)

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.errMatch != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errMatch))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

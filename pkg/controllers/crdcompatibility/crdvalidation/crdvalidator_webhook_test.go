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

package crdvalidation

import (
	"testing"

	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/index"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func Test_crdValidator_validateCreateOrUpdate(t *testing.T) {
	RegisterTestingT(t)

	ctx := t.Context()

	testCRDWorking := test.GenerateTestCRD()
	testCRDWorking.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["foo1"] = apiextensionsv1.JSONSchemaProps{
		Type: "string",
	}
	testCRDWorking.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["foo2"] = apiextensionsv1.JSONSchemaProps{
		Type: "string",
	}

	incompatibleCRD1 := testCRDWorking.DeepCopy()
	delete(incompatibleCRD1.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties, "foo1")

	incompatibleCRD2 := incompatibleCRD1.DeepCopy()
	delete(incompatibleCRD2.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties, "foo2")

	tests := []struct {
		name         string
		obj          runtime.Object
		requirements []client.Object
		wantWarnings OmegaMatcher
		wantErr      OmegaMatcher
	}{
		{
			name:         "Should permit a valid CRD",
			obj:          testCRDWorking.DeepCopy(),
			requirements: []client.Object{test.GenerateTestCompatibilityRequirement(testCRDWorking.DeepCopy())},
			wantWarnings: BeNil(),
		},
		{
			name:         "Should reject an incompatible CRD",
			obj:          incompatibleCRD1.DeepCopy(),
			requirements: []client.Object{test.GenerateTestCompatibilityRequirement(testCRDWorking.DeepCopy())},
			wantWarnings: BeNil(),
			wantErr:      MatchError("CRD is not compatible with CompatibilityRequirements: This requirement was added by CompatibilityRequirement : removed field : v1.^.foo1"),
		},
		{
			name: "Should allow an incompatible CRD with excluded fields",
			obj:  testCRDWorking.DeepCopy(),
			requirements: []client.Object{
				func() *apiextensionsv1alpha1.CompatibilityRequirement {
					r := test.GenerateTestCompatibilityRequirement(testCRDWorking.DeepCopy())
					r.Spec.CompatibilitySchema.ExcludedFields = []apiextensionsv1alpha1.APIExcludedField{
						{Path: "foo1", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
					}

					return r
				}(),
			},
			wantWarnings: BeNil(),
		},
		{
			name:         "Should reject an incompatible CRD with multiple removed fields",
			obj:          incompatibleCRD2.DeepCopy(),
			requirements: []client.Object{test.GenerateTestCompatibilityRequirement(testCRDWorking.DeepCopy())},
			wantWarnings: BeNil(),
			wantErr: MatchError(
				SatisfyAll(
					ContainSubstring("CRD is not compatible with CompatibilityRequirements: "),
					ContainSubstring("This requirement was added by CompatibilityRequirement : removed field : v1.^.foo1"),
					ContainSubstring("This requirement was added by CompatibilityRequirement : removed field : v1.^.foo2"),
				),
			),
		},
		{
			name: "Should allow an incompatible CRD with multiple excluded fields",
			obj:  testCRDWorking.DeepCopy(),
			requirements: []client.Object{
				func() *apiextensionsv1alpha1.CompatibilityRequirement {
					r := test.GenerateTestCompatibilityRequirement(testCRDWorking.DeepCopy())
					r.Spec.CompatibilitySchema.ExcludedFields = []apiextensionsv1alpha1.APIExcludedField{
						{Path: "foo1", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
						{Path: "foo2", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
					}

					return r
				}(),
			},
			wantWarnings: BeNil(),
		},
		{
			name: "Should permit a CRD with no CustomResourceDefinitionSchemaValidation",
			obj:  testCRDWorking.DeepCopy(),
			requirements: []client.Object{
				func() *apiextensionsv1alpha1.CompatibilityRequirement {
					r := test.GenerateTestCompatibilityRequirement(testCRDWorking.DeepCopy())
					r.Spec.CustomResourceDefinitionSchemaValidation = (apiextensionsv1alpha1.CustomResourceDefinitionSchemaValidation{})

					return r
				}(),
			},
			wantWarnings: BeNil(),
		},
		{
			name: "Should permit an incompatible CRD with warnings for Action set to CRDAdmitActionWarn",
			obj:  incompatibleCRD1.DeepCopy(),
			requirements: []client.Object{
				func() *apiextensionsv1alpha1.CompatibilityRequirement {
					r := test.GenerateTestCompatibilityRequirement(testCRDWorking.DeepCopy())
					r.Spec.CustomResourceDefinitionSchemaValidation.Action = apiextensionsv1alpha1.CRDAdmitActionWarn

					r.Name = "test-warn"

					return r
				}(),
			},
			wantWarnings: ConsistOf("This requirement was added by CompatibilityRequirement test-warn: removed field : v1.^.foo1"),
		},
		{
			name: "Should permit an incompatible CRD with excluded fields and warnings for Action set to CRDAdmitActionWarn",
			obj:  testCRDWorking.DeepCopy(),
			requirements: []client.Object{
				func() *apiextensionsv1alpha1.CompatibilityRequirement {
					r := test.GenerateTestCompatibilityRequirement(testCRDWorking.DeepCopy())
					r.Spec.CompatibilitySchema.ExcludedFields = []apiextensionsv1alpha1.APIExcludedField{
						{Path: "foo1", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
					}
					r.Spec.CustomResourceDefinitionSchemaValidation.Action = apiextensionsv1alpha1.CRDAdmitActionWarn

					r.Name = "test-warn-excluded"

					return r
				}(),
			},
			wantWarnings: BeNil(),
		},
		{
			name: "Should permit an incompatible CRD with multiple warnings for Action set to CRDAdmitActionWarn",
			obj:  incompatibleCRD2.DeepCopy(),
			requirements: []client.Object{
				func() *apiextensionsv1alpha1.CompatibilityRequirement {
					r := test.GenerateTestCompatibilityRequirement(testCRDWorking.DeepCopy())
					r.Spec.CustomResourceDefinitionSchemaValidation.Action = apiextensionsv1alpha1.CRDAdmitActionWarn
					r.Name = "test-warn-multiple"

					return r
				}(),
			},
			wantWarnings: ConsistOf(
				"This requirement was added by CompatibilityRequirement test-warn-multiple: removed field : v1.^.foo1",
				"This requirement was added by CompatibilityRequirement test-warn-multiple: removed field : v1.^.foo2",
			),
		},
		{
			name: "Should permit an incompatible CRD with multiple excluded fields and warnings for Action set to CRDAdmitActionWarn",
			obj:  testCRDWorking.DeepCopy(),
			requirements: []client.Object{
				func() *apiextensionsv1alpha1.CompatibilityRequirement {
					r := test.GenerateTestCompatibilityRequirement(testCRDWorking.DeepCopy())
					r.Spec.CompatibilitySchema.ExcludedFields = []apiextensionsv1alpha1.APIExcludedField{
						{Path: "foo1", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
						{Path: "foo2", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
					}
					r.Spec.CustomResourceDefinitionSchemaValidation.Action = apiextensionsv1alpha1.CRDAdmitActionWarn

					r.Name = "test-warn-multiple-excluded"

					return r
				}(),
			},
			wantWarnings: BeNil(),
		},
		{
			name: "Should reject a CRD when a CompatibilityRequirement has an invalid Action",
			obj:  testCRDWorking.DeepCopy(),
			requirements: []client.Object{
				func() *apiextensionsv1alpha1.CompatibilityRequirement {
					r := test.GenerateTestCompatibilityRequirement(testCRDWorking.DeepCopy())
					r.Spec.CustomResourceDefinitionSchemaValidation.Action = "Invalid"

					r.Name = "test-invalid-action"

					return r
				}(),
			},
			wantWarnings: BeNil(),
			wantErr:      MatchError("unknown value for CompatibilityRequirement.spec.customResourceDefinitionSchemaValidation.action: \"Invalid\" for requirement test-invalid-action"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			v := Validator{
				client: fake.NewClientBuilder().WithObjects(tt.requirements...).WithIndex(&apiextensionsv1alpha1.CompatibilityRequirement{}, index.FieldCRDByName, index.CRDByName).Build(),
			}

			gotWarnings, gotErr := v.validateCreateOrUpdate(ctx, tt.obj)
			if tt.wantErr != nil {
				g.Expect(gotErr).To(tt.wantErr)
			} else {
				g.Expect(gotErr).ToNot(HaveOccurred())
			}

			g.Expect(gotWarnings).To(tt.wantWarnings)
		})
	}
}

func Test_pruneExludedFields(t *testing.T) {
	RegisterTestingT(t)

	tests := []struct {
		name           string
		crd            *apiextensionsv1.CustomResourceDefinition
		excludedFields []apiextensionsv1alpha1.APIExcludedField
		wantCRD        *apiextensionsv1.CustomResourceDefinition
		wantErr        OmegaMatcher
	}{
		{
			name: "Should prune an excluded field",
			crd:  complexCRDSchema(),
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{Path: "spec.name", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
			},
			wantCRD: func() *apiextensionsv1.CustomResourceDefinition {
				crd := complexCRDSchema()
				specSchema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
				delete(specSchema.Properties, "name")
				specSchema.Required = []string{"metadata"}
				crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = specSchema

				return crd
			}(),
		},
		{
			name: "Should prune an excluded field in an array",
			crd:  complexCRDSchema(),
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{Path: "spec.tags.key", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
			},
			wantCRD: func() *apiextensionsv1.CustomResourceDefinition {
				crd := complexCRDSchema()
				tagsSchema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["tags"].Items.Schema
				delete(tagsSchema.Properties, "key")
				tagsSchema.Required = []string{"value"}
				crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["tags"].Items.Schema = tagsSchema

				return crd
			}(),
		},
		{
			name: "Should prune an excluded field in an object",
			crd:  complexCRDSchema(),
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{Path: "spec.metadata.region", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
			},
			wantCRD: func() *apiextensionsv1.CustomResourceDefinition {
				crd := complexCRDSchema()
				metadataSchema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["metadata"]
				delete(metadataSchema.Properties, "region")
				metadataSchema.Required = []string{}
				crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["metadata"] = metadataSchema

				return crd
			}(),
		},
		{
			name: "With multiple schema versions, should prune only from the prescribed version",
			crd: func() *apiextensionsv1.CustomResourceDefinition {
				crd := complexCRDSchema()
				crd.Spec.Versions = append(crd.Spec.Versions, *crd.Spec.Versions[0].DeepCopy())
				crd.Spec.Versions[1].Name = "v2"

				return crd
			}(),
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{Path: "spec.name", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
			},
			wantCRD: func() *apiextensionsv1.CustomResourceDefinition {
				crd := complexCRDSchema()
				crd.Spec.Versions = append(crd.Spec.Versions, *crd.Spec.Versions[0].DeepCopy())
				crd.Spec.Versions[1].Name = "v2"

				// Only the v1 schema should be pruned for the name field.
				specSchema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
				delete(specSchema.Properties, "name")
				specSchema.Required = []string{"metadata"}
				crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = specSchema

				return crd
			}(),
		},
		{
			name: "With multiple schema versions, should prune from multiple prescribed versions",
			crd: func() *apiextensionsv1.CustomResourceDefinition {
				crd := complexCRDSchema()
				crd.Spec.Versions = append(crd.Spec.Versions, *crd.Spec.Versions[0].DeepCopy())
				crd.Spec.Versions[1].Name = "v2"

				return crd
			}(),
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{Path: "spec.name", Versions: []apiextensionsv1alpha1.APIVersionString{"v1", "v2"}},
			},
			wantCRD: func() *apiextensionsv1.CustomResourceDefinition {
				crd := complexCRDSchema()
				crd.Spec.Versions = append(crd.Spec.Versions, *crd.Spec.Versions[0].DeepCopy())
				crd.Spec.Versions[1].Name = "v2"

				specSchema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
				delete(specSchema.Properties, "name")
				specSchema.Required = []string{"metadata"}
				crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = specSchema

				specSchema = crd.Spec.Versions[1].Schema.OpenAPIV3Schema.Properties["spec"]
				delete(specSchema.Properties, "name")
				specSchema.Required = []string{"metadata"}
				crd.Spec.Versions[1].Schema.OpenAPIV3Schema.Properties["spec"] = specSchema

				return crd
			}(),
		},
		{
			name: "Should return an error if the excluded field is not found",
			crd:  complexCRDSchema(),
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{Path: "spec.not-found", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
			},
			wantErr: MatchError("failed to prune excluded fields: path not found in schema: desired path ^.spec.not-found, path ^.spec is missing child not-found"),
		},
		{
			name: "Should return an error if the excluded field is not found in an array",
			crd:  complexCRDSchema(),
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{Path: "spec.tags.not-found", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
			},
			wantErr: MatchError("failed to prune excluded fields: path not found in schema: desired path ^.spec.tags.not-found, path ^.spec.tags is missing child not-found"),
		},
		{
			name: "Should return an error if the excluded field is a child of a scalar",
			crd:  complexCRDSchema(),
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{Path: "spec.name.not-found", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
			},
			wantErr: MatchError("failed to prune excluded fields: path not found in schema: desired path ^.spec.name.not-found, path ^.spec.name is not an object"),
		},
		{
			name: "Should return an error if the excluded field is in a malformed array",
			crd: func() *apiextensionsv1.CustomResourceDefinition {
				crd := complexCRDSchema()
				crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["tags"] = apiextensionsv1.JSONSchemaProps{
					Type: "array",
				}

				return crd
			}(),
			excludedFields: []apiextensionsv1alpha1.APIExcludedField{
				{Path: "spec.tags.not-found", Versions: []apiextensionsv1alpha1.APIVersionString{"v1"}},
			},
			wantErr: MatchError("failed to prune excluded fields: path not found in schema: desired path ^.spec.tags.not-found, path ^.spec.tags is an array but does not have an items schema"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			gotCRD, gotErr := pruneExcludedFields(tt.crd, tt.excludedFields)
			if tt.wantErr != nil {
				g.Expect(gotErr).To(tt.wantErr)
				return
			} else {
				g.Expect(gotErr).ToNot(HaveOccurred())
			}

			g.Expect(gotCRD.Spec.Versions).To(test.MatchViaDiff(tt.wantCRD.Spec.Versions))
		})
	}
}

// complexCRDSchema demonstrates how to use JSONSchemaPropsBuilder
// with the CRDSchemaBuilder to create complex schemas.
func complexCRDSchema() *apiextensionsv1.CustomResourceDefinition {
	// Build a complex schema using the new JSONSchemaPropsBuilder
	specSchema := test.NewObjectSchema().
		WithStringProperty("name").
		WithStringProperty("description").
		WithObjectProperty("metadata", test.NewObjectSchema().
			WithStringProperty("region").
			WithStringProperty("zone").
			WithRequiredField("region")).
		WithArrayProperty("tags", test.SimpleArraySchema(
			test.NewObjectSchema().
				WithStringProperty("key").
				WithStringProperty("value").
				WithRequiredFields("key", "value"),
			nil, nil, false)).
		WithRequiredFields("name", "metadata")

	statusSchema := test.NewObjectSchema().
		WithProperty("phase", *test.NewStringSchema().WithEnum("Pending", "Running", "Succeeded", "Failed").Build()).
		WithProperty("replicas", *test.NewIntegerSchema().Build()).
		WithArrayProperty("conditions", test.SimpleArraySchema(
			test.NewObjectSchema().
				WithStringProperty("type").
				WithStringProperty("status").
				WithStringProperty("reason").
				WithStringProperty("message").
				WithRequiredFields("type", "status"),
			nil, nil, false))

	// Create the root schema that combines spec and status
	rootSchema := test.NewObjectSchema().
		WithType("object").
		WithObjectProperty("spec", specSchema).
		WithObjectProperty("status", statusSchema).
		WithRequiredField("spec")

	// Use the complete schema with CRDSchemaBuilder
	return test.NewCRDSchemaBuilder().
		WithSchemaBuilder(rootSchema).
		Build()
}

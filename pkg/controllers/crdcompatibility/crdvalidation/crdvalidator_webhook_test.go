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

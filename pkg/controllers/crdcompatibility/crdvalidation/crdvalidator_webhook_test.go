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

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/index"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func Test_crdValidator_validateCreateOrUpdate(t *testing.T) { //nolint:funlen
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
			requirements: []client.Object{test.GenerateTestCRDCompatibilityRequirement(testCRDWorking.DeepCopy())},
			wantWarnings: BeNil(),
		},
		{
			name:         "Should reject an incompatible CRD",
			obj:          incompatibleCRD1.DeepCopy(),
			requirements: []client.Object{test.GenerateTestCRDCompatibilityRequirement(testCRDWorking.DeepCopy())},
			wantWarnings: BeNil(),
			wantErr:      MatchError("CRD is not compatible with CRDCompatibilityRequirements: This requirement was added by Test Creator: requirement : removed field : v1.^.foo1"),
		},
		{
			name:         "Should reject an incompatible CRD with multiple removed fields",
			obj:          incompatibleCRD2.DeepCopy(),
			requirements: []client.Object{test.GenerateTestCRDCompatibilityRequirement(testCRDWorking.DeepCopy())},
			wantWarnings: BeNil(),
			wantErr: MatchError(
				SatisfyAll(
					ContainSubstring("CRD is not compatible with CRDCompatibilityRequirements: "),
					ContainSubstring("This requirement was added by Test Creator: requirement : removed field : v1.^.foo1"),
					ContainSubstring("This requirement was added by Test Creator: requirement : removed field : v1.^.foo2"),
				),
			),
		},
		{
			name: "Should permit an incompatible CRD with warnings for CRDAdmitAction set to Warn",
			obj:  incompatibleCRD1.DeepCopy(),
			requirements: []client.Object{
				func() *operatorv1alpha1.CRDCompatibilityRequirement {
					r := test.GenerateTestCRDCompatibilityRequirement(testCRDWorking.DeepCopy())
					r.Spec.CRDAdmitAction = operatorv1alpha1.CRDAdmitActionWarn

					return r
				}(),
			},
			wantWarnings: ConsistOf("This requirement was added by Test Creator: requirement : removed field : v1.^.foo1"),
		},
		{
			name: "Should permit an incompatible CRD with multiple warnings for CRDAdmitAction set to Warn",
			obj:  incompatibleCRD2.DeepCopy(),
			requirements: []client.Object{
				func() *operatorv1alpha1.CRDCompatibilityRequirement {
					r := test.GenerateTestCRDCompatibilityRequirement(testCRDWorking.DeepCopy())
					r.Spec.CRDAdmitAction = operatorv1alpha1.CRDAdmitActionWarn

					return r
				}(),
			},
			wantWarnings: ConsistOf(
				"This requirement was added by Test Creator: requirement : removed field : v1.^.foo1",
				"This requirement was added by Test Creator: requirement : removed field : v1.^.foo2",
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			v := Validator{
				client: fake.NewClientBuilder().WithObjects(tt.requirements...).WithIndex(&operatorv1alpha1.CRDCompatibilityRequirement{}, index.FieldCRDByName, index.CRDByName).Build(),
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

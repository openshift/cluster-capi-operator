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

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func Test_crdValidator_validateCreateOrUpdate(t *testing.T) { //nolint:funlen
	RegisterTestingT(t)

	testCRDWorking := generateTestCRD()
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
		requirements []*operatorv1alpha1.CRDCompatibilityRequirement
		wantWarnings OmegaMatcher
		wantErr      string
	}{
		{
			name:         "Should permit a valid CRD",
			obj:          testCRDWorking.DeepCopy(),
			requirements: []*operatorv1alpha1.CRDCompatibilityRequirement{generateTestRequirement(testCRDWorking.DeepCopy())},
			wantWarnings: BeNil(),
			wantErr:      "",
		},
		{
			name:         "Should reject an incompatible CRD",
			obj:          incompatibleCRD1.DeepCopy(),
			requirements: []*operatorv1alpha1.CRDCompatibilityRequirement{generateTestRequirement(testCRDWorking.DeepCopy())},
			wantWarnings: BeNil(),
			wantErr:      "CRD is not compatible with CRDCompatibilityRequirements: This requirement was added by Test Creator: requirement : removed field : v1.^.foo1",
		},
		{
			name:         "Should reject an incompatible CRD with multiple removed fields",
			obj:          incompatibleCRD2.DeepCopy(),
			requirements: []*operatorv1alpha1.CRDCompatibilityRequirement{generateTestRequirement(testCRDWorking.DeepCopy())},
			wantWarnings: BeNil(),
			wantErr:      "CRD is not compatible with CRDCompatibilityRequirements: This requirement was added by Test Creator: requirement : removed field : v1.^.foo1\nThis requirement was added by Test Creator: requirement : removed field : v1.^.foo2",
		},
		{
			name: "Should permit an incompatible CRD with warnings for CRDAdmitAction set to Warn",
			obj:  incompatibleCRD1.DeepCopy(),
			requirements: []*operatorv1alpha1.CRDCompatibilityRequirement{
				func() *operatorv1alpha1.CRDCompatibilityRequirement {
					r := generateTestRequirement(testCRDWorking.DeepCopy())
					r.Spec.CRDAdmitAction = operatorv1alpha1.CRDAdmitActionWarn

					return r
				}(),
			},
			wantWarnings: ConsistOf("This requirement was added by Test Creator: requirement : removed field : v1.^.foo1"),
			wantErr:      "",
		},
		{
			name: "Should permit an incompatible CRD with multiple warnings for CRDAdmitAction set to Warn",
			obj:  incompatibleCRD2.DeepCopy(),
			requirements: []*operatorv1alpha1.CRDCompatibilityRequirement{
				func() *operatorv1alpha1.CRDCompatibilityRequirement {
					r := generateTestRequirement(testCRDWorking.DeepCopy())
					r.Spec.CRDAdmitAction = operatorv1alpha1.CRDAdmitActionWarn

					return r
				}(),
			},
			wantWarnings: ConsistOf(
				"This requirement was added by Test Creator: requirement : removed field : v1.^.foo1",
				"This requirement was added by Test Creator: requirement : removed field : v1.^.foo2",
			),
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			v := crdValidator{}

			for _, req := range tt.requirements {
				r := reconcileState{}
				g.Expect(r.parseCompatibilityCRD(req)).To(Succeed())
				v.setRequirement(req, r.compatibilityCRD)
			}

			gotWarnings, gotErr := v.validateCreateOrUpdate(tt.obj)
			if tt.wantErr != "" {
				g.Expect(gotErr).To(MatchError(tt.wantErr))
			} else {
				g.Expect(gotErr).ToNot(HaveOccurred())
			}

			g.Expect(gotWarnings).To(tt.wantWarnings)
		})
	}
}

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
			wantErr: MatchError(ContainSubstring("compatibilityCRD is not valid")),
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

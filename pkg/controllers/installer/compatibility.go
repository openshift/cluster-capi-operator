/*
Copyright 2026 Red Hat, Inc.

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

package installer

import (
	"fmt"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8syaml "sigs.k8s.io/yaml"
)

const (
	compatibilityRequirementNamePrefix = "ccapio-"
	capiNamespace                      = "openshift-cluster-api"
)

// buildCompatibilityRequirement constructs a CompatibilityRequirement for the
// given CRD and returns it as an unstructured object for inclusion in the
// compatibility phase.
func buildCompatibilityRequirement(crd *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	crdYAML, err := k8syaml.Marshal(crd.Object)
	if err != nil {
		return nil, fmt.Errorf("marshalling CRD %s to YAML: %w", crd.GetName(), err)
	}

	cr := &apiextensionsv1alpha1.CompatibilityRequirement{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apiextensionsv1alpha1.GroupVersion.String(),
			Kind:       "CompatibilityRequirement",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: compatibilityRequirementNamePrefix + crd.GetName(),
		},
		Spec: apiextensionsv1alpha1.CompatibilityRequirementSpec{
			CompatibilitySchema: apiextensionsv1alpha1.CompatibilitySchema{
				CustomResourceDefinition: apiextensionsv1alpha1.CRDData{
					Type: apiextensionsv1alpha1.CRDDataTypeYAML,
					Data: string(crdYAML),
				},
				RequiredVersions: apiextensionsv1alpha1.APIVersions{
					// additionalVersions deferred to a separate PR.
					// See https://github.com/openshift/cluster-capi-operator/pull/604#discussion_r3657934049
					DefaultSelection: apiextensionsv1alpha1.APIVersionSetTypeStorageOnly,
				},
			},
			CustomResourceDefinitionSchemaValidation: apiextensionsv1alpha1.CustomResourceDefinitionSchemaValidation{
				Action: apiextensionsv1alpha1.CRDAdmitActionDeny,
			},
			ObjectSchemaValidation: apiextensionsv1alpha1.ObjectSchemaValidation{
				Action: apiextensionsv1alpha1.CRDAdmitActionDeny,
				NamespaceSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"kubernetes.io/metadata.name": capiNamespace,
					},
				},
			},
		},
	}

	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cr)
	if err != nil {
		return nil, fmt.Errorf("converting CompatibilityRequirement to unstructured: %w", err)
	}

	return &unstructured.Unstructured{Object: data}, nil
}

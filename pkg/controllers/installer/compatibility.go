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
	"errors"
	"fmt"

	openshiftapiextensionsv1 "github.com/openshift/api/apiextensions/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8syaml "sigs.k8s.io/yaml"
)

const (
	compatibilityRequirementNamePrefix = "ccapio-"
	capiNamespace                      = "openshift-cluster-api"

	// maxNameLength is the RFC 1123 subdomain limit the API server enforces on
	// metadata.name. A CRD name may use all of it, so adding a prefix and suffix can overflow.
	maxNameLength = 253
)

// errNameTooLong is returned when naming a requirement pushes the generated
// CompatibilityRequirement name past the API server's name length limit.
var errNameTooLong = errors.New("generated CompatibilityRequirement name exceeds the maximum name length")

// buildCompatibilityRequirement constructs a CompatibilityRequirement for the
// given CRD and returns it as an unstructured object for inclusion in the
// compatibility phase.
func buildCompatibilityRequirement(crd *unstructured.Unstructured, revisionIndex int64) (*unstructured.Unstructured, error) {
	name := fmt.Sprintf("%s%s-%d", compatibilityRequirementNamePrefix, crd.GetName(), revisionIndex)
	if len(name) > maxNameLength {
		return nil, fmt.Errorf("%w: %q is %d characters, limit is %d", errNameTooLong, name, len(name), maxNameLength)
	}

	typedCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(crd.Object, typedCRD); err != nil {
		return nil, fmt.Errorf("converting CRD %s from unstructured: %w", crd.GetName(), err)
	}

	apiextensionsv1.SetObjectDefaults_CustomResourceDefinition(typedCRD)

	crdYAML, err := k8syaml.Marshal(typedCRD)
	if err != nil {
		return nil, fmt.Errorf("marshalling CRD %s to YAML: %w", crd.GetName(), err)
	}

	cr := &openshiftapiextensionsv1.CompatibilityRequirement{
		TypeMeta: metav1.TypeMeta{
			APIVersion: openshiftapiextensionsv1.GroupVersion.String(),
			Kind:       "CompatibilityRequirement",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: openshiftapiextensionsv1.CompatibilityRequirementSpec{
			CompatibilitySchema: openshiftapiextensionsv1.CompatibilitySchema{
				CustomResourceDefinition: openshiftapiextensionsv1.CRDData{
					Type: openshiftapiextensionsv1.CRDDataTypeYAML,
					Data: string(crdYAML),
				},
				RequiredVersions: openshiftapiextensionsv1.APIVersions{
					// additionalVersions deferred to a separate PR.
					// See https://github.com/openshift/cluster-capi-operator/pull/604#discussion_r3657934049
					DefaultSelection: openshiftapiextensionsv1.APIVersionSetTypeStorageOnly,
				},
			},
			CustomResourceDefinitionSchemaValidation: openshiftapiextensionsv1.CustomResourceDefinitionSchemaValidation{
				Action: openshiftapiextensionsv1.CRDAdmitActionDeny,
			},
			ObjectSchemaValidation: openshiftapiextensionsv1.ObjectSchemaValidation{
				Action: openshiftapiextensionsv1.CRDAdmitActionDeny,
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

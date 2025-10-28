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

package index

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
)

const (
	// FieldCRDByName contains the path to the CRDName field to index on.
	FieldCRDByName string = "status.crdName"
)

// CRDByName contains the logic to index CompatibilityRequirement by CRDRef.
func CRDByName(obj client.Object) []string {
	requirement, ok := obj.(*apiextensionsv1alpha1.CompatibilityRequirement)
	if !ok {
		panic(fmt.Sprintf("Expected a CompatibilityRequirement but got a %T", obj))
	}

	return []string{requirement.Status.CRDName}
}

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

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
)

const (
	FieldCRDByName string = "status.crdName"
)

// CRDByName contains the logic to index CRDCompatibilityRequirement by CRDRef.
func CRDByName(obj client.Object) []string {
	requirement, ok := obj.(*operatorv1alpha1.CRDCompatibilityRequirement)
	if !ok {
		panic(fmt.Sprintf("Expected a CRDCompatibilityRequirement but got a %T", obj))
	}

	return []string{requirement.Status.CRDName}
}

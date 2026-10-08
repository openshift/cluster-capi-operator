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
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// crdGroupKind returns a GroupKind for CustomResourceDefinitions.
func crdGroupKind() schema.GroupKind {
	return schema.GroupKind{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}
}

// deploymentGroupKind returns a GroupKind for Deployments.
func deploymentGroupKind() schema.GroupKind {
	return schema.GroupKind{Group: "apps", Kind: "Deployment"}
}

// compatibilityRequirementGroupKind returns a GroupKind for CompatibilityRequirements.
func compatibilityRequirementGroupKind() schema.GroupKind {
	return schema.GroupKind{Group: "apiextensions.openshift.io", Kind: "CompatibilityRequirement"}
}

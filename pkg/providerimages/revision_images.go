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

package providerimages

import (
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// ImageRefsFromRevisions extracts unique image references from ClusterAPI revisions
// and returns a set of deduplicated image references.
func ImageRefsFromRevisions(revisions []operatorv1alpha1.ClusterAPIInstallerRevision) sets.Set[string] {
	result := sets.New[string]()

	for _, revision := range revisions {
		for _, component := range revision.Components {
			if component.Type == operatorv1alpha1.InstallerComponentTypeImage {
				result.Insert(string(component.Image.Ref))
			}
		}
	}

	return result
}

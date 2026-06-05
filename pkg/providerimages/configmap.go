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
	"errors"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

var errConfigMapNil = errors.New("ConfigMap cannot be nil")

// ConfigMapName is the name of the ConfigMap containing current-release provider image references.
const ConfigMapName = "capi-installer-images"

// ImageRefsFromConfigMap extracts provider image references from a ConfigMap.
// The ConfigMap data values are image references; the keys are discarded.
// Returns an error if the ConfigMap is nil.
func ImageRefsFromConfigMap(cm *corev1.ConfigMap) (sets.Set[string], error) {
	if cm == nil {
		return nil, errConfigMapNil
	}

	result := sets.New[string]()
	for _, v := range cm.Data {
		result.Insert(v)
	}

	return result, nil
}

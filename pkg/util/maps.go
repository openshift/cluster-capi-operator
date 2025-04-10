/*
Copyright 2024 Red Hat, Inc.

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
package util

// MergeMaps merges two maps, with values from the second map taking precedence.
func MergeMaps(m1, m2 map[string]string) map[string]string {
	if len(m1) == 0 && len(m2) == 0 {
		return nil
	}

	result := make(map[string]string, len(m1))
	for k, v := range m1 {
		result[k] = v
	}

	for k, v := range m2 {
		result[k] = v
	}

	return result
}

// DeepCopyMapStringString creates a deep copy of a map[string]string.
func DeepCopyMapStringString(original map[string]string) map[string]string {
	if original == nil {
		return nil
	}

	copiedMap := make(map[string]string, len(original))

	for key, value := range original {
		copiedMap[key] = value
	}

	return copiedMap
}

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
package test

import (
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// BeK8SNotFound is a gomega matcher that checks if an error is a NotFound error
// returned by the Kubernetes API.
func BeK8SNotFound() types.GomegaMatcher {
	return gomega.WithTransform(apierrors.IsNotFound, gomega.BeTrue())
}

// IgnoreFields is a gomega matcher that ignores the specified fields in the object.
func IgnoreFields(fields []string, matcher types.GomegaMatcher) types.GomegaMatcher {
	return gomega.WithTransform(func(obj map[string]interface{}) map[string]interface{} {
		for _, field := range fields {
			delete(obj, field)
		}
		return obj
	}, matcher)
}

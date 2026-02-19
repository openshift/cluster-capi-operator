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
	"github.com/google/go-cmp/cmp"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// BeK8SNotFound is a gomega matcher that checks if an error is a NotFound error
// returned by the Kubernetes API.
func BeK8SNotFound() types.GomegaMatcher {
	return gomega.WithTransform(apierrors.IsNotFound, gomega.BeTrue())
}

// MatchViaDiff is a gomega matcher that checks if the actual object is equal to the expected object by using cmp.Diff.
// This is useful for complex objects where you want to focus on a small subset of the object when there is a difference.
func MatchViaDiff(expected any) types.GomegaMatcher {
	return gomega.WithTransform(func(actual any) string {
		return cmp.Diff(expected, actual)
	}, gomega.BeEmpty())
}

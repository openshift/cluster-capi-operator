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

package crdcompatibility

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// waitForAdmitted waits until a CompatibilityRequirement has the Admitted condition set to True.
func waitForAdmitted(ctx context.Context, requirement *apiextensionsv1alpha1.CompatibilityRequirement) {
	GinkgoHelper()
	By("Waiting for the CompatibilityRequirement to be admitted")
	Eventually(kWithCtx(ctx).Object(requirement)).WithContext(ctx).Should(
		HaveField("Status.Conditions", test.HaveCondition("Admitted").WithStatus(metav1.ConditionTrue)),
	)
}

// createTestObject creates a test object and defers its deletion.
func createTestObject(ctx context.Context, obj client.Object, desc string) {
	GinkgoHelper()
	By("Creating test " + desc)
	Eventually(func() error { return cl.Create(ctx, obj) }).WithContext(ctx).Should(Succeed())
	GinkgoWriter.Println("Created " + desc + " " + obj.GetName())

	deferCleanupTestObject(obj, desc)
}

// deferCleanupTestObject defers the deletion of a test object.
func deferCleanupTestObject(testObject client.Object, desc string) {
	DeferCleanup(func(ctx context.Context) {
		By("Deleting test " + desc + " " + testObject.GetName())
		Eventually(tryDelete(ctx, testObject)).WithContext(ctx).Should(test.BeK8SNotFound())
	})
}

func clientOpWrapper[T any](fn func(ctx context.Context, obj client.Object, opts ...T) error, ctx context.Context, obj client.Object) func() error {
	return func() error {
		return fn(ctx, obj)
	}
}

// tryCreate returns a function which attempts to create the given object.
// It is useful in Eventually calls to avoid flakiness.
func tryCreate(ctx context.Context, obj client.Object) func() error {
	return clientOpWrapper(cl.Create, ctx, obj)
}

// tryDelete returns a function which attempts to delete the given object.
// It is useful in Eventually calls to avoid flakiness.
func tryDelete(ctx context.Context, obj client.Object) func() error {
	return clientOpWrapper(cl.Delete, ctx, obj)
}

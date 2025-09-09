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
	"crypto/rand"
	"math/big"
	"unicode"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func baseRequirement(testCRD *apiextensionsv1.CustomResourceDefinition) *operatorv1alpha1.CRDCompatibilityRequirement {
	return &operatorv1alpha1.CRDCompatibilityRequirement{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-requirement-",
		},
		Spec: operatorv1alpha1.CRDCompatibilityRequirementSpec{
			CRDRef:             testCRD.Name,
			CompatibilityCRD:   toYAML(testCRD),
			CRDAdmitAction:     operatorv1alpha1.CRDAdmitActionEnforce,
			CreatorDescription: "Test Creator",
		},
	}
}

func generateTestCRD() *apiextensionsv1.CustomResourceDefinition {
	const validChars = "abcdefghijklmnopqrstuvwxyz"

	randBytes := make([]byte, 10)

	for i := range randBytes {
		randInt, err := rand.Int(rand.Reader, big.NewInt(int64(len(validChars))))
		Expect(err).To(Succeed())

		randBytes[i] = validChars[randInt.Int64()]
	}

	gvk := schema.GroupVersionKind{
		Group:   "example.com",
		Version: "v1",
		Kind:    string(unicode.ToUpper(rune(randBytes[0]))) + string(randBytes[1:]),
	}

	return test.GenerateCRD(gvk, "v1beta1")
}

func waitForAdmitted(ctx context.Context, requirement *operatorv1alpha1.CRDCompatibilityRequirement) {
	By("Waiting for the CRDCompatibilityRequirement to be admitted")
	Eventually(kWithCtx(ctx).Object(requirement)).Should(SatisfyAll(
		test.HaveCondition("Admitted", metav1.ConditionTrue),
	))
}

func createTestObject(ctx context.Context, obj client.Object, desc string) {
	By("Creating " + desc)
	Eventually(func() error { return cl.Create(ctx, obj) }).Should(Succeed())
	GinkgoWriter.Println("Created " + desc + " " + obj.GetName())

	deferCleanupTestObject(obj, desc)
}

func deferCleanupTestObject(testObject client.Object, desc string) {
	DeferCleanup(func(ctx context.Context) {
		By("Deleting " + desc + " " + testObject.GetName())
		Eventually(tryDelete(ctx, testObject)).Should(test.BeK8SNotFound())
	})
}

func eventuallyWrapper[T any](fn func(ctx context.Context, obj client.Object, opts ...T) error, ctx context.Context, obj client.Object) func() error {
	return func() error {
		return fn(ctx, obj)
	}
}

func tryCreate(ctx context.Context, obj client.Object) func() error {
	return eventuallyWrapper(cl.Create, ctx, obj)
}

func tryDelete(ctx context.Context, obj client.Object) func() error {
	return eventuallyWrapper(cl.Delete, ctx, obj)
}

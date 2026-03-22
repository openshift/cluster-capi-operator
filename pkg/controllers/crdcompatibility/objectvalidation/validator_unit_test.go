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

package objectvalidation

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/index"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("validator", func() {
	var (
		testCRD                  *apiextensionsv1.CustomResourceDefinition
		compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
	)

	BeforeEach(func() {
		// Create a test CRD with some properties for testing
		testCRD = test.NewCRDSchemaBuilder().
			WithStringProperty("testField").
			WithRequiredStringProperty("requiredField").
			WithIntegerProperty("optionalNumber").
			Build()

		// Create a corresponding compatibility requirement
		compatibilityRequirement = test.GenerateTestCompatibilityRequirement(testCRD)
	})

	Describe("getValidationStrategy", func() {
		Context("when CompatibilityRequirement exists", func() {
			var validator *validator

			BeforeEach(func() {
				validator = createValidatorWithFakeClient([]client.Object{compatibilityRequirement})
			})

			It("should return validation strategy", func() {
				strategy, err := validator.getValidationStrategy(compatibilityRequirement, "v1")

				Expect(err).NotTo(HaveOccurred())
				Expect(strategy).NotTo(BeNil())
			})

			It("should cache validation strategy", func() {
				// First call should create and cache the strategy
				_, err := validator.getValidationStrategy(compatibilityRequirement, "v1")
				Expect(err).NotTo(HaveOccurred())

				// Check that cache now contains an entry
				validator.validationStrategyCacheLock.RLock()
				cacheSize := len(validator.validationStrategyCache)
				validator.validationStrategyCacheLock.RUnlock()

				Expect(cacheSize).To(Equal(1))
			})

			It("should use cached strategy on subsequent calls", func() {
				// First call
				strategy1, err1 := validator.getValidationStrategy(compatibilityRequirement, "v1")
				Expect(err1).NotTo(HaveOccurred())

				// Second call should return the same strategy instance
				strategy2, err2 := validator.getValidationStrategy(compatibilityRequirement, "v1")
				Expect(err2).NotTo(HaveOccurred())

				// Should be the exact same object (cached)
				// This uses reflect.DeepEqual to compare the two strategies.
				Expect(strategy1).To(Equal(strategy2))
			})

			It("should invalidate cache when generation changes", func() {
				// First call
				strategy1, err1 := validator.getValidationStrategy(compatibilityRequirement, "v1")
				Expect(err1).NotTo(HaveOccurred())

				compatibilityRequirement.Generation++
				validator.client = createValidatorWithFakeClient([]client.Object{compatibilityRequirement}).client

				// Second call should return the same strategy instance
				strategy2, err2 := validator.getValidationStrategy(compatibilityRequirement, "v1")
				Expect(err2).NotTo(HaveOccurred())

				Expect(strategy1).NotTo(Equal(strategy2))
			})
		})

		Context("when CompatibilityRequirement has invalid CRD YAML", func() {
			It("should return error", func() {
				brokenCompatibilityRequirement := test.GenerateTestCompatibilityRequirement(testCRD)
				brokenCompatibilityRequirement.Spec.CompatibilitySchema.CustomResourceDefinition.Data = "invalid: yaml: content: ["

				validator := createValidatorWithFakeClient([]client.Object{brokenCompatibilityRequirement})

				_, err := validator.getValidationStrategy(brokenCompatibilityRequirement, "v1")

				Expect(err).To(MatchError("failed to create validation strategy: failed to decode compatibility schema data for CompatibilityRequirement \"\": yaml: mapping values are not allowed in this context"))
			})
		})
	})

	Describe("validation strategy caching", func() {
		Context("cache key generation", func() {
			It("should create cache key from UID, name, version, and generation", func() {
				key := getValidationStrategyCacheKey(compatibilityRequirement, "v1")

				Expect(key.compatibilityRequirementName).To(Equal(compatibilityRequirement.Name))
				Expect(key.version).To(Equal("v1"))
			})
		})
	})
})

// Helper function to create a validator with fake client for testing.
func createValidatorWithFakeClient(objects []client.Object) *validator {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(apiextensionsv1alpha1.SchemeGroupVersion, &apiextensionsv1alpha1.CompatibilityRequirement{})
	scheme.AddKnownTypes(apiextensionsv1.SchemeGroupVersion, &apiextensionsv1.CustomResourceDefinition{})

	return &validator{
		universalDeserializer: serializer.NewCodecFactory(scheme).UniversalDeserializer(),
		client: fake.NewClientBuilder().
			WithObjects(objects...).
			WithIndex(&apiextensionsv1alpha1.CompatibilityRequirement{},
				index.FieldCRDByName,
				index.CRDByName).
			Build(),
		validationStrategyCache: make(map[validationStrategyCacheKey]validationStrategyCacheValue),
	}
}

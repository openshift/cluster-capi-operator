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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/index"
	"github.com/openshift/cluster-capi-operator/pkg/test"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiserver/pkg/registry/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("validator", func() {
	var (
		ctx                      context.Context
		testCRD                  *apiextensionsv1.CustomResourceDefinition
		compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement
	)

	BeforeEach(func() {
		ctx = context.Background()

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
				strategy, err := validator.getValidationStrategy(ctx, compatibilityRequirement.Name, "v1")

				Expect(err).NotTo(HaveOccurred())
				Expect(strategy).NotTo(BeNil())
			})

			It("should cache validation strategy", func() {
				// First call should create and cache the strategy
				_, err := validator.getValidationStrategy(ctx, compatibilityRequirement.Name, "v1")
				Expect(err).NotTo(HaveOccurred())

				// Check that cache now contains an entry
				validator.validationStrategyCacheLock.RLock()
				cacheSize := len(validator.validationStrategyCache)
				validator.validationStrategyCacheLock.RUnlock()

				Expect(cacheSize).To(Equal(1))
			})

			It("should use cached strategy on subsequent calls", func() {
				// First call
				strategy1, err1 := validator.getValidationStrategy(ctx, compatibilityRequirement.Name, "v1")
				Expect(err1).NotTo(HaveOccurred())

				// Second call should return the same strategy instance
				strategy2, err2 := validator.getValidationStrategy(ctx, compatibilityRequirement.Name, "v1")
				Expect(err2).NotTo(HaveOccurred())

				// Should be the exact same object (cached)
				// This uses reflect.DeepEqual to compare the two strategies.
				Expect(strategy1).To(Equal(strategy2))
			})
		})

		Context("when CompatibilityRequirement does not exist", func() {
			It("should return error", func() {
				validator := createValidatorWithFakeClient([]client.Object{}) // No objects

				_, err := validator.getValidationStrategy(ctx, "non-existent", "v1")

				Expect(err).To(MatchError("failed to get CompatibilityRequirement \"non-existent\": compatibilityrequirements.apiextensions.openshift.io \"non-existent\" not found"))
			})
		})

		Context("when CompatibilityRequirement has invalid CRD YAML", func() {
			It("should return error", func() {
				brokenCompatibilityRequirement := test.GenerateTestCompatibilityRequirement(testCRD)
				brokenCompatibilityRequirement.Spec.CompatibilitySchema.CustomResourceDefinition.Data = "invalid: yaml: content: ["

				validator := createValidatorWithFakeClient([]client.Object{brokenCompatibilityRequirement})

				_, err := validator.getValidationStrategy(ctx, brokenCompatibilityRequirement.Name, "v1")

				Expect(err).To(MatchError("failed to get validation strategy: failed to parse compatibility schema data for CompatibilityRequirement \"\": error converting YAML to JSON: yaml: mapping values are not allowed in this context"))
			})
		})
	})

	Describe("validation strategy caching", func() {
		Context("cache key generation", func() {
			It("should create cache key from UID, name, version, and generation", func() {
				key := getValidationStrategyCacheKey(compatibilityRequirement, "v1")

				Expect(key.uid).To(Equal(compatibilityRequirement.UID))
				Expect(key.compatibilityRequirementName).To(Equal(compatibilityRequirement.Name))
				Expect(key.version).To(Equal("v1"))
				Expect(key.generation).To(Equal(compatibilityRequirement.Generation))
			})
		})

		Context("cache invalidation", func() {
			var validator *validator

			BeforeEach(func() {
				validator = createValidatorWithFakeClient([]client.Object{})
			})

			It("should prune old strategies when generation changes", func() {
				// Create a compatibility requirement with generation 1
				oldReq := compatibilityRequirement.DeepCopy()
				oldReq.Generation = 1

				// Create a newer version with generation 2
				newReq := compatibilityRequirement.DeepCopy()
				newReq.Generation = 2

				// Manually populate cache with old strategy
				oldKey := getValidationStrategyCacheKey(oldReq, "v1")
				newKey := getValidationStrategyCacheKey(newReq, "v1")

				validator.validationStrategyCacheLock.Lock()
				validator.validationStrategyCache[oldKey] = nil // Mock strategy
				validator.validationStrategyCacheLock.Unlock()

				// Call pruneOldValidationStrategies with the new requirement
				validator.validationStrategyCacheLock.Lock()
				validator.pruneOldValidationStrategies(newReq, "v1")
				validator.validationStrategyCacheLock.Unlock()

				// Old strategy should be removed from cache
				validator.validationStrategyCacheLock.RLock()
				_, oldExists := validator.validationStrategyCache[oldKey]
				_, newExists := validator.validationStrategyCache[newKey]
				validator.validationStrategyCacheLock.RUnlock()

				Expect(oldExists).To(BeFalse(), "Old strategy should be pruned")
				Expect(newExists).To(BeFalse(), "New strategy not added yet")
			})

			It("should not prune strategies for different requirements", func() {
				// Create two different compatibility requirements
				req1 := compatibilityRequirement.DeepCopy()
				req1.Name = "requirement-1"
				req1.UID = "uid-1"

				req2 := compatibilityRequirement.DeepCopy()
				req2.Name = "requirement-2"
				req2.UID = "uid-2"

				// Add strategies for both to cache
				key1 := getValidationStrategyCacheKey(req1, "v1")
				key2 := getValidationStrategyCacheKey(req2, "v1")

				validator.validationStrategyCacheLock.Lock()
				validator.validationStrategyCache[key1] = nil
				validator.validationStrategyCache[key2] = nil
				validator.validationStrategyCacheLock.Unlock()

				// Prune for req1 should not affect req2
				validator.validationStrategyCacheLock.Lock()
				validator.pruneOldValidationStrategies(req1, "v1")
				validator.validationStrategyCacheLock.Unlock()

				// Both should still exist (no pruning occurred since generations are same)
				validator.validationStrategyCacheLock.RLock()
				_, exists1 := validator.validationStrategyCache[key1]
				_, exists2 := validator.validationStrategyCache[key2]
				validator.validationStrategyCacheLock.RUnlock()

				Expect(exists1).To(BeTrue(), "Strategy for req1 should remain")
				Expect(exists2).To(BeTrue(), "Strategy for req2 should remain")
			})

			It("should not prune strategies for different versions", func() {
				req := compatibilityRequirement.DeepCopy()

				// Add strategies for different versions
				keyV1 := getValidationStrategyCacheKey(req, "v1")
				keyV2 := getValidationStrategyCacheKey(req, "v2")

				validator.validationStrategyCacheLock.Lock()
				validator.validationStrategyCache[keyV1] = nil
				validator.validationStrategyCache[keyV2] = nil
				validator.validationStrategyCacheLock.Unlock()

				// Prune for v1 should not affect v2
				validator.validationStrategyCacheLock.Lock()
				validator.pruneOldValidationStrategies(req, "v1")
				validator.validationStrategyCacheLock.Unlock()

				// Both should still exist
				validator.validationStrategyCacheLock.RLock()
				_, existsV1 := validator.validationStrategyCache[keyV1]
				_, existsV2 := validator.validationStrategyCache[keyV2]
				validator.validationStrategyCacheLock.RUnlock()

				Expect(existsV1).To(BeTrue(), "Strategy for v1 should remain")
				Expect(existsV2).To(BeTrue(), "Strategy for v2 should remain")
			})
		})
	})
})

// Helper function to create a validator with fake client for testing
func createValidatorWithFakeClient(objects []client.Object) *validator {
	return &validator{
		client: fake.NewClientBuilder().
			WithObjects(objects...).
			WithIndex(&apiextensionsv1alpha1.CompatibilityRequirement{},
				index.FieldCRDByName,
				index.CRDByName).
			Build(),
		validationStrategyCache: make(map[validationStrategyCacheKey]rest.RESTCreateUpdateStrategy),
	}
}

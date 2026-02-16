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

package objectpruning

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/index"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
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
		testCRD = createStrictCRDSchema()

		// Create a corresponding compatibility requirement
		compatibilityRequirement = createCompatibilityRequirement(testCRD)
	})

	Describe("getStructuralSchema", func() {
		Context("when CompatibilityRequirement exists", func() {
			var validator *validator

			BeforeEach(func() {
				validator = createValidatorWithFakeClient([]client.Object{compatibilityRequirement})
			})

			It("should return structural schema", func() {
				schema, err := validator.getStructuralSchema(ctx, compatibilityRequirement.Name, "v1")

				Expect(err).NotTo(HaveOccurred())
				Expect(schema).NotTo(BeNil())
			})

			It("should cache structural schema", func() {
				// First call should create and cache the schema
				_, err := validator.getStructuralSchema(ctx, compatibilityRequirement.Name, "v1")
				Expect(err).NotTo(HaveOccurred())

				// Check that cache now contains an entry
				validator.structuralSchemaCacheLock.RLock()
				cacheSize := len(validator.structuralSchemaCache)
				validator.structuralSchemaCacheLock.RUnlock()

				Expect(cacheSize).To(Equal(1))
			})

			It("should use cached schema on subsequent calls", func() {
				// First call
				schema1, err1 := validator.getStructuralSchema(ctx, compatibilityRequirement.Name, "v1")
				Expect(err1).NotTo(HaveOccurred())

				// Second call should return the same schema instance
				schema2, err2 := validator.getStructuralSchema(ctx, compatibilityRequirement.Name, "v1")
				Expect(err2).NotTo(HaveOccurred())

				// Should be the exact same object (cached)
				// This uses reflect.DeepEqual to compare the two schemas.
				Expect(schema1).To(Equal(schema2))
			})
		})

		Context("when CompatibilityRequirement does not exist", func() {
			It("should return error", func() {
				validator := createValidatorWithFakeClient([]client.Object{}) // No objects

				_, err := validator.getStructuralSchema(ctx, "non-existent", "v1")

				Expect(err).To(MatchError("failed to get CompatibilityRequirement \"non-existent\": compatibilityrequirements.apiextensions.openshift.io \"non-existent\" not found"))
			})
		})

		Context("when CompatibilityRequirement has invalid CRD YAML", func() {
			It("should return error", func() {
				brokenCompatibilityRequirement := createCompatibilityRequirement(testCRD)
				brokenCompatibilityRequirement.Spec.CompatibilitySchema.CustomResourceDefinition.Data = "invalid: yaml: content: ["

				validator := createValidatorWithFakeClient([]client.Object{brokenCompatibilityRequirement})

				_, err := validator.getStructuralSchema(ctx, brokenCompatibilityRequirement.Name, "v1")

				Expect(err).To(MatchError(ContainSubstring("failed to get structural schema: failed to parse compatibility schema data for CompatibilityRequirement")))
			})
		})
	})

	Describe("structural schema caching", func() {
		Context("cache key generation", func() {
			It("should create cache key from UID, name, version, and generation", func() {
				key := getStructuralSchemaCacheKey(compatibilityRequirement, "v1")

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

			It("should prune old schemas when generation changes", func() {
				// Create a compatibility requirement with generation 1
				oldReq := compatibilityRequirement.DeepCopy()
				oldReq.Generation = 1

				// Create a newer version with generation 2
				newReq := compatibilityRequirement.DeepCopy()
				newReq.Generation = 2

				// Manually populate cache with old schema
				oldKey := getStructuralSchemaCacheKey(oldReq, "v1")
				newKey := getStructuralSchemaCacheKey(newReq, "v1")

				validator.structuralSchemaCacheLock.Lock()
				validator.structuralSchemaCache[oldKey] = &structuralschema.Structural{} // Mock schema
				validator.structuralSchemaCacheLock.Unlock()

				// Call pruneOldStructuralSchemas with the new requirement
				validator.structuralSchemaCacheLock.Lock()
				validator.pruneOldStructuralSchemas(newReq, "v1")
				validator.structuralSchemaCacheLock.Unlock()

				// Old schema should be removed from cache
				validator.structuralSchemaCacheLock.RLock()
				_, oldExists := validator.structuralSchemaCache[oldKey]
				_, newExists := validator.structuralSchemaCache[newKey]
				validator.structuralSchemaCacheLock.RUnlock()

				Expect(oldExists).To(BeFalse(), "Old schema should be pruned")
				Expect(newExists).To(BeFalse(), "New schema not added yet")
			})

			It("should not prune schemas for different requirements", func() {
				// Create two different compatibility requirements
				req1 := compatibilityRequirement.DeepCopy()
				req1.Name = "requirement-1"
				req1.UID = "uid-1"

				req2 := compatibilityRequirement.DeepCopy()
				req2.Name = "requirement-2"
				req2.UID = "uid-2"

				// Add schemas for both to cache
				key1 := getStructuralSchemaCacheKey(req1, "v1")
				key2 := getStructuralSchemaCacheKey(req2, "v1")

				validator.structuralSchemaCacheLock.Lock()
				validator.structuralSchemaCache[key1] = &structuralschema.Structural{}
				validator.structuralSchemaCache[key2] = &structuralschema.Structural{}
				validator.structuralSchemaCacheLock.Unlock()

				// Prune for req1 should not affect req2
				validator.structuralSchemaCacheLock.Lock()
				validator.pruneOldStructuralSchemas(req1, "v1")
				validator.structuralSchemaCacheLock.Unlock()

				// Both should still exist (no pruning occurred since generations are same)
				validator.structuralSchemaCacheLock.RLock()
				_, exists1 := validator.structuralSchemaCache[key1]
				_, exists2 := validator.structuralSchemaCache[key2]
				validator.structuralSchemaCacheLock.RUnlock()

				Expect(exists1).To(BeTrue(), "Schema for req1 should remain")
				Expect(exists2).To(BeTrue(), "Schema for req2 should remain")
			})

			It("should not prune schemas for different versions", func() {
				req := compatibilityRequirement.DeepCopy()

				// Add schemas for different versions
				keyV1 := getStructuralSchemaCacheKey(req, "v1")
				keyV2 := getStructuralSchemaCacheKey(req, "v2")

				validator.structuralSchemaCacheLock.Lock()
				validator.structuralSchemaCache[keyV1] = &structuralschema.Structural{}
				validator.structuralSchemaCache[keyV2] = &structuralschema.Structural{}
				validator.structuralSchemaCacheLock.Unlock()

				// Prune for v1 should not affect v2
				validator.structuralSchemaCacheLock.Lock()
				validator.pruneOldStructuralSchemas(req, "v1")
				validator.structuralSchemaCacheLock.Unlock()

				// Both should still exist
				validator.structuralSchemaCacheLock.RLock()
				_, existsV1 := validator.structuralSchemaCache[keyV1]
				_, existsV2 := validator.structuralSchemaCache[keyV2]
				validator.structuralSchemaCacheLock.RUnlock()

				Expect(existsV1).To(BeTrue(), "Schema for v1 should remain")
				Expect(existsV2).To(BeTrue(), "Schema for v2 should remain")
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
		structuralSchemaCache: make(map[structuralSchemaCacheKey]*structuralschema.Structural),
	}
}

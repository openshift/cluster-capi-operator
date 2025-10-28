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

package index

import (
	"context"
	"fmt"
	"sync"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
)

const (
	// FieldCRDByName contains the path to the CRDName field to index on.
	FieldCRDByName string = "status.crdName"
)

// CRDByName contains the logic to index CompatibilityRequirement by CRDRef.
func CRDByName(obj client.Object) []string {
	requirement, ok := obj.(*apiextensionsv1alpha1.CompatibilityRequirement)
	if !ok {
		panic(fmt.Sprintf("Expected a CompatibilityRequirement but got a %T", obj))
	}

	return []string{requirement.Status.CRDName}
}

var (
	// Used to track indexes that have been added to the manager.
	indexesAdded = sync.Map{}
)

// Use a struct to pair the index name with the manager.
// Each manager can only have one index with the same name.
type indexToManagerKey struct {
	indexName string
	manager   ctrl.Manager
}

// AddIndexThreadSafe adds an index to the manager for the given object and index name.
// It uses a sync.Map to ensure that the index is added only once.
func AddIndexThreadSafe(ctx context.Context, mgr ctrl.Manager, obj client.Object, indexName string, indexFunc func(obj client.Object) []string) error {
	if _, ok := indexesAdded.LoadOrStore(indexToManagerKey{indexName: indexName, manager: mgr}, true); ok {
		// We previously entered this function for this index name so we don't need to add it again.
		return nil
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, obj, indexName, indexFunc); err != nil {
		return fmt.Errorf("failed to add index to CompatibilityRequirements: %w", err)
	}

	return nil
}

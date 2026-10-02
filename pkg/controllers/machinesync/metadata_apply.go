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

package machinesync

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// metadataFieldSet contains label and annotation keys from managed fields.
type metadataFieldSet struct {
	labels      map[string]struct{}
	annotations map[string]struct{}
}

// applyAuthoritativeCAPIMetadata reconciles Machine API-authoritative metadata with SSA.
// It preserves fields owned by other apply managers and removes remaining stale fields.
func (r *MachineSyncReconciler) applyAuthoritativeCAPIMetadata(ctx context.Context, existing, desired client.Object) (bool, error) {
	ownedBySyncController := metadataFieldsOwnedByManager(existing, capiMetadataFieldManager)
	preservedFields := metadataFieldsOwnedByOtherApplyManagers(existing, capiMetadataFieldManager)

	labelsCleanupPatch, labelsApplyRequired := metadataMapPatch(existing.GetLabels(), desired.GetLabels(), ownedBySyncController.labels, preservedFields.labels)

	annotationsCleanupPatch, annotationsApplyRequired := metadataMapPatch(existing.GetAnnotations(), desired.GetAnnotations(), ownedBySyncController.annotations, preservedFields.annotations)
	if !labelsApplyRequired && !annotationsApplyRequired && len(labelsCleanupPatch) == 0 && len(annotationsCleanupPatch) == 0 {
		return false, nil
	}

	if labelsApplyRequired || annotationsApplyRequired {
		gvk, err := apiutil.GVKForObject(desired, r.Scheme)
		if err != nil {
			return false, fmt.Errorf("failed to determine GroupVersionKind for %T: %w", desired, err)
		}

		if err := r.patchMetadata(ctx, existing, gvk.GroupVersion().String(), gvk.Kind, nonNilMap(desired.GetLabels()), nonNilMap(desired.GetAnnotations())); err != nil {
			return false, err
		}

		ownedBySyncController = metadataFieldsOwnedByManager(existing, capiMetadataFieldManager)
		preservedFields = metadataFieldsOwnedByOtherApplyManagers(existing, capiMetadataFieldManager)
		labelsCleanupPatch, _ = metadataMapPatch(existing.GetLabels(), desired.GetLabels(), ownedBySyncController.labels, preservedFields.labels)
		annotationsCleanupPatch, _ = metadataMapPatch(existing.GetAnnotations(), desired.GetAnnotations(), ownedBySyncController.annotations, preservedFields.annotations)
	}

	if len(labelsCleanupPatch) > 0 || len(annotationsCleanupPatch) > 0 {
		if err := r.patchStaleMetadata(ctx, existing, labelsCleanupPatch, annotationsCleanupPatch); err != nil {
			return false, err
		}
	}

	return true, nil
}

// patchMetadata applies the desired labels and annotations under the sync controller's field manager.
func (r *MachineSyncReconciler) patchMetadata(ctx context.Context, obj client.Object, apiVersion, kind string, labels, annotations map[string]string) error {
	patchData, err := json.Marshal(map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata": map[string]any{
			"name":        obj.GetName(),
			"namespace":   obj.GetNamespace(),
			"labels":      labels,
			"annotations": annotations,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal metadata apply patch for %T: %w", obj, err)
	}

	if err := r.Patch(ctx, obj, client.RawPatch(types.ApplyPatchType, patchData), &client.PatchOptions{
		Raw: &metav1.PatchOptions{
			FieldManager: capiMetadataFieldManager,
			Force:        ptr.To(true),
		},
	}); err != nil {
		return fmt.Errorf("failed to apply metadata to %T: %w", obj, err)
	}

	return nil
}

// patchStaleMetadata removes metadata keys that must not survive the authoritative sync.
func (r *MachineSyncReconciler) patchStaleMetadata(ctx context.Context, obj client.Object, labels, annotations map[string]any) error {
	patchData, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"resourceVersion": obj.GetResourceVersion(),
			"labels":          labels,
			"annotations":     annotations,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal stale metadata cleanup patch for %T: %w", obj, err)
	}

	if err := r.Patch(ctx, obj, client.RawPatch(types.MergePatchType, patchData)); err != nil {
		return fmt.Errorf("failed to remove stale metadata from %T: %w", obj, err)
	}

	return nil
}

// metadataMapPatch identifies stale fields for explicit deletion. Fields managed by other SSA
// actors are omitted so their ownership and values are preserved. The returned boolean reports
// whether desired fields must be applied to update a value or acquire SSA ownership.
func metadataMapPatch(existing, desired map[string]string, ownedBySyncController, preserved map[string]struct{}) (map[string]any, bool) {
	cleanupPatch := map[string]any{}
	applyRequired := false

	for key, value := range desired {
		_, owned := ownedBySyncController[key]
		if existing[key] != value || !owned {
			applyRequired = true
		}
	}

	for key := range existing {
		if _, wanted := desired[key]; wanted {
			continue
		}

		if _, preserve := preserved[key]; preserve {
			continue
		}

		cleanupPatch[key] = nil
	}

	return cleanupPatch, applyRequired
}

// nonNilMap returns a non-nil copy suitable for inclusion in an apply patch.
func nonNilMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}

	return result
}

// metadataFieldsOwnedByManager returns metadata keys owned by an apply manager.
func metadataFieldsOwnedByManager(obj client.Object, manager string) metadataFieldSet {
	return metadataFieldsMatching(obj, func(entry metav1.ManagedFieldsEntry) bool {
		return entry.Manager == manager && entry.Subresource == ""
	})
}

// metadataFieldsOwnedByOtherApplyManagers returns metadata keys owned by other apply managers.
func metadataFieldsOwnedByOtherApplyManagers(obj client.Object, manager string) metadataFieldSet {
	return metadataFieldsMatching(obj, func(entry metav1.ManagedFieldsEntry) bool {
		return entry.Manager != manager && entry.Operation == metav1.ManagedFieldsOperationApply && entry.Subresource == ""
	})
}

// metadataFieldsMatching extracts metadata keys from managed fields selected by include.
func metadataFieldsMatching(obj client.Object, include func(metav1.ManagedFieldsEntry) bool) metadataFieldSet {
	result := metadataFieldSet{
		labels:      map[string]struct{}{},
		annotations: map[string]struct{}{},
	}

	for _, entry := range obj.GetManagedFields() {
		if !include(entry) || entry.FieldsV1 == nil {
			continue
		}

		var fields map[string]any
		if err := json.Unmarshal(entry.FieldsV1.Raw, &fields); err != nil {
			continue
		}

		metadata, ok := fields["f:metadata"].(map[string]any)
		if !ok {
			continue
		}

		addManagedMapKeys(metadata["f:labels"], result.labels)
		addManagedMapKeys(metadata["f:annotations"], result.annotations)
	}

	return result
}

// addManagedMapKeys adds metadata map keys from a FieldsV1 map to fields.
func addManagedMapKeys(rawFields any, fields map[string]struct{}) {
	managedMap, ok := rawFields.(map[string]any)
	if !ok {
		return
	}

	for field := range managedMap {
		const fieldPrefix = "f:"
		if len(field) > len(fieldPrefix) && field[:len(fieldPrefix)] == fieldPrefix {
			fields[field[len(fieldPrefix):]] = struct{}{}
		}
	}
}

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

package migrationcommon

import (
	"context"
	"errors"
	"fmt"

	"github.com/openshift/cluster-capi-operator/pkg/util"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var errDeepCopyDoesNotImplementClientObject = errors.New("deep copy does not implement client.Object")

// AddPausedAnnotation adds the Cluster API paused annotation and patches the
// object with optimistic locking.
func AddPausedAnnotation(ctx context.Context, k8sClient client.Client, obj client.Object) (bool, error) {
	if annotations.HasPaused(obj) {
		return false, nil
	}

	before, err := deepCopyClientObject(obj)
	if err != nil {
		return false, err
	}

	annotations.AddAnnotations(obj, map[string]string{clusterv1.PausedAnnotation: ""})

	if err := k8sClient.Patch(ctx, obj, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return false, fmt.Errorf("failed to patch %T %s/%s: %w", obj, obj.GetNamespace(), obj.GetName(), err)
	}

	return true, nil
}

// RemovePausedAnnotation removes the Cluster API paused annotation and patches
// the object.
func RemovePausedAnnotation(ctx context.Context, k8sClient client.Client, obj client.Object) (bool, error) {
	if !annotations.HasPaused(obj) {
		return false, nil
	}

	before, err := deepCopyClientObject(obj)
	if err != nil {
		return false, err
	}

	util.RemoveAnnotation(obj, clusterv1.PausedAnnotation)

	if err := k8sClient.Patch(ctx, obj, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return false, fmt.Errorf("failed to patch %T %s/%s: %w", obj, obj.GetNamespace(), obj.GetName(), err)
	}

	return true, nil
}

func deepCopyClientObject(obj client.Object) (client.Object, error) {
	before, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return nil, fmt.Errorf("%w: %T", errDeepCopyDoesNotImplementClientObject, obj)
	}

	return before, nil
}

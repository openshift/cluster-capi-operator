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

package installer

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	configv1 "github.com/openshift/api/config/v1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

// staticRelatedObjects returns the hardcoded relatedObjects entries that match
// the CVO manifest (manifests/0000_30_cluster-api_12_clusteroperator.yaml).
// These are static entries that are always present regardless of which CAPI
// providers are installed.
func staticRelatedObjects() []configv1.ObjectReference {
	return []configv1.ObjectReference{
		{Group: "", Resource: "namespaces", Name: "openshift-cluster-api"},
		{Group: "", Resource: "namespaces", Name: "openshift-cluster-api-operator"},
		{Group: "", Resource: "namespaces", Name: "openshift-compatibility-requirements-operator"},
		{Group: "admissionregistration.k8s.io", Resource: "validatingadmissionpolicies"},
		{Group: "admissionregistration.k8s.io", Resource: "validatingadmissionpolicybindings"},
		{Group: "apiextensions.openshift.io", Resource: "compatibilityrequirements"},
		{Group: "operator.openshift.io", Resource: "clusterapis", Name: "cluster"},
	}
}

// mergeRelatedObjects merges static and dynamic relatedObjects. Static entries
// come first in their original order, then dynamic entries are deduped against
// static, sorted, and appended.
func mergeRelatedObjects(static, dynamic []configv1.ObjectReference) []configv1.ObjectReference {
	staticSet := make(map[configv1.ObjectReference]struct{}, len(static))

	for _, obj := range static {
		staticSet[obj] = struct{}{}
	}

	var deduped []configv1.ObjectReference

	for _, obj := range dynamic {
		if _, exists := staticSet[obj]; !exists {
			deduped = append(deduped, obj)
		}
	}

	slices.SortFunc(deduped, compareObjectReference)

	return append(slices.Clone(static), deduped...)
}

// writeRelatedObjects writes relatedObjects to the ClusterOperator status using
// a non-SSA merge patch. This avoids the SSA conditions write claiming ownership
// of the relatedObjects field.
func writeRelatedObjects(ctx context.Context, k8sClient client.Client, relatedObjects []configv1.ObjectReference) error {
	co := &configv1.ClusterOperator{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: operatorstatus.ClusterOperatorName}, co); err != nil {
		return fmt.Errorf("getting ClusterOperator: %w", err)
	}

	if equality.Semantic.DeepEqual(co.Status.RelatedObjects, relatedObjects) {
		return nil
	}

	// Build a raw merge patch from the apply configuration containing only
	// the relatedObjects field.
	objectRefs := util.SliceMap(relatedObjects, func(obj configv1.ObjectReference) *configv1apply.ObjectReferenceApplyConfiguration {
		ref := configv1apply.ObjectReference().
			WithGroup(obj.Group).
			WithResource(obj.Resource).
			WithName(obj.Name)
		if obj.Namespace != "" {
			ref.WithNamespace(obj.Namespace)
		}

		return ref
	})

	applyConfig := configv1apply.ClusterOperator(operatorstatus.ClusterOperatorName).
		WithUID(co.UID).
		WithStatus(configv1apply.ClusterOperatorStatus().
			WithRelatedObjects(objectRefs...),
		)

	patchData, err := json.Marshal(applyConfig)
	if err != nil {
		return fmt.Errorf("marshaling relatedObjects patch: %w", err)
	}

	if err := k8sClient.Status().Patch(ctx, co, client.RawPatch(types.MergePatchType, patchData)); err != nil {
		return fmt.Errorf("patching ClusterOperator relatedObjects: %w", err)
	}

	return nil
}

// compareObjectReference compares two ObjectReferences for stable sorting.
func compareObjectReference(a, b configv1.ObjectReference) int {
	if c := cmp.Compare(a.Namespace, b.Namespace); c != 0 {
		return c
	}

	if c := cmp.Compare(a.Group, b.Group); c != 0 {
		return c
	}

	if c := cmp.Compare(a.Resource, b.Resource); c != 0 {
		return c
	}

	return cmp.Compare(a.Name, b.Name)
}

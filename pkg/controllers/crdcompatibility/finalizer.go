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
	"fmt"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorapplyconfig "github.com/openshift/client-go/operator/applyconfigurations/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	finalizerName string = controllerName + "/finalizer"
)

func setFinalizer(ctx context.Context, cl client.Client, obj *operatorv1alpha1.CRDCompatibilityRequirement) error {
	return writeFinalizer(ctx, cl, obj, operatorapplyconfig.CRDCompatibilityRequirement(obj.GetName()).
		WithUID(obj.GetUID()).
		WithFinalizers(finalizerName))
}

func clearFinalizer(ctx context.Context, cl client.Client, obj *operatorv1alpha1.CRDCompatibilityRequirement) error {
	return writeFinalizer(ctx, cl, obj, operatorapplyconfig.CRDCompatibilityRequirement(obj.GetName()).
		WithUID(obj.GetUID()))
}

func writeFinalizer(ctx context.Context, cl client.Client, obj *operatorv1alpha1.CRDCompatibilityRequirement, applyConfig *operatorapplyconfig.CRDCompatibilityRequirementApplyConfiguration) error {
	if err := cl.Patch(ctx, obj, util.ApplyConfigPatch(applyConfig), client.ForceOwnership, client.FieldOwner(finalizerName)); err != nil {
		return fmt.Errorf("failed to write finalizer: %w", err)
	}
	return nil
}

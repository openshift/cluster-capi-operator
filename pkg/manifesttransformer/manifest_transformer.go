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

package manifesttransformer

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pkg.package-operator.run/boxcutter"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

// ManifestTransformer transforms objects at install time while objects are being
// collected into boxcutter phases.
type ManifestTransformer interface {
	// TransformObject returns a transformed copy of the object and any
	// boxcutter options that should apply to the object. If TransformObject
	// returns a nil object, it means the object should be skipped.
	TransformObject(ctx context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error)

	// Validate checks that the object is valid for this transformer. An error
	// prevents revision creation and is treated as non-retryable.
	Validate(obj *unstructured.Unstructured) error

	// WithRevision returns a new transformer that will be used for the given revision.
	WithRevision(ctx context.Context, revision revisiongenerator.ParsedRevision) ManifestTransformer

	// WithComponent returns a new transformer that will be used for the given component.
	WithComponent(ctx context.Context, component revisiongenerator.ParsedComponent) ManifestTransformer
}

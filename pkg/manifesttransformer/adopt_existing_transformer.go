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
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pkg.package-operator.run/boxcutter"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

// ErrInvalidAdoptExistingAnnotation is returned by Validate when an object
// carries an adopt-existing annotation with an unrecognised value.
var ErrInvalidAdoptExistingAnnotation = errors.New("invalid annotation value")

// AdoptExistingTransformer implements ManifestTransformer for the adopt-existing
// annotation. It strips the annotation from each object and, for objects
// annotated with "always", returns an object-level CollisionProtectionNone
// option so that boxcutter adopts pre-existing cluster resources instead of
// reporting a collision. It also validates that any annotation value is
// recognised, returning an error that wraps ErrInvalidAdoptExistingAnnotation.
type AdoptExistingTransformer struct{}

var _ ManifestTransformer = &AdoptExistingTransformer{}

// TransformObject returns a copy of obj with the adopt-existing annotation
// stripped, along with a CollisionProtectionNone option when the annotation
// value is "always". Objects without the annotation are returned unchanged.
func (a *AdoptExistingTransformer) TransformObject(_ context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
	annotations := obj.GetAnnotations()

	value, hasAnnotation := annotations[revisiongenerator.AdoptExistingAnnotation]
	if !hasAnnotation {
		return obj, nil, nil
	}

	// Strip the annotation from a copy before the object is applied to the cluster.
	obj = obj.DeepCopy()
	annotations = obj.GetAnnotations()
	delete(annotations, revisiongenerator.AdoptExistingAnnotation)
	obj.SetAnnotations(annotations)

	if value == revisiongenerator.AdoptExistingAlways {
		return obj, []boxcutter.ObjectReconcileOption{
			boxcutter.WithCollisionProtection(boxcutter.CollisionProtectionNone),
		}, nil
	}

	return obj, nil, nil
}

// Validate returns an error wrapping ErrInvalidAdoptExistingAnnotation when
// the object carries an adopt-existing annotation with an unrecognised value.
func (a *AdoptExistingTransformer) Validate(obj *unstructured.Unstructured) error {
	annotations := obj.GetAnnotations()
	if len(annotations) == 0 {
		return nil
	}

	value, exists := annotations[revisiongenerator.AdoptExistingAnnotation]
	if !exists {
		return nil
	}

	switch value {
	case revisiongenerator.AdoptExistingAlways, revisiongenerator.AdoptExistingNever:
		return nil
	default:
		return fmt.Errorf("%w: %s=%q on %s %s/%s",
			reconcile.TerminalError(ErrInvalidAdoptExistingAnnotation),
			revisiongenerator.AdoptExistingAnnotation,
			value,
			obj.GetObjectKind().GroupVersionKind().Kind,
			obj.GetNamespace(),
			obj.GetName(),
		)
	}
}

// WithRevision implements ManifestTransformer. AdoptExistingTransformer does
// not need revision context.
func (a *AdoptExistingTransformer) WithRevision(_ context.Context, _ revisiongenerator.ParsedRevision) ManifestTransformer {
	return a
}

// WithComponent implements ManifestTransformer. AdoptExistingTransformer does
// not need component context.
func (a *AdoptExistingTransformer) WithComponent(_ context.Context, _ revisiongenerator.ParsedComponent) ManifestTransformer {
	return a
}

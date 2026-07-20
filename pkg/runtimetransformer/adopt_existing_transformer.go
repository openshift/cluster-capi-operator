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

package runtimetransformer

import (
	"context"
	"errors"
	"fmt"

	"pkg.package-operator.run/boxcutter"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

// ErrInvalidAdoptExistingAnnotation is returned by Validate when an object
// carries an adopt-existing annotation with an unrecognised value.
var ErrInvalidAdoptExistingAnnotation = errors.New("invalid " + revisiongenerator.AdoptExistingAnnotation + " annotation value")

// AdoptExistingTransformer implements RuntimeTransformer for the adopt-existing
// annotation. It strips the annotation from each object and, for objects
// annotated with "always", returns a per-object CollisionProtectionNone option
// so that boxcutter adopts pre-existing cluster resources instead of
// reporting a collision. It also validates that any annotation value is
// recognised, returning an error that wraps ErrInvalidAdoptExistingAnnotation.
type AdoptExistingTransformer struct{}

var _ SimpleRuntimeTransformer = &AdoptExistingTransformer{}

// TransformObject strips the adopt-existing annotation from obj (mutating it
// in place) and returns a CollisionProtectionNone option when the annotation
// value is "always".
func (a *AdoptExistingTransformer) TransformObject(_ context.Context, obj client.Object) ([]boxcutter.PhaseReconcileOption, error) {
	annotations := obj.GetAnnotations()

	value, hasAnnotation := annotations[revisiongenerator.AdoptExistingAnnotation]
	if !hasAnnotation {
		return nil, nil
	}

	// Strip the annotation before the object is applied to the cluster.
	delete(annotations, revisiongenerator.AdoptExistingAnnotation)
	obj.SetAnnotations(annotations)

	if value == revisiongenerator.AdoptExistingAlways {
		return []boxcutter.PhaseReconcileOption{
			boxcutter.WithObjectReconcileOptions(obj,
				boxcutter.WithCollisionProtection(boxcutter.CollisionProtectionNone),
			),
		}, nil
	}

	return nil, nil
}

// Validate returns an error wrapping ErrInvalidAdoptExistingAnnotation when
// the object carries an adopt-existing annotation with an unrecognised value.
func (a *AdoptExistingTransformer) Validate(obj client.Object) error {
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
		return fmt.Errorf("%w: %q on %s %s/%s",
			ErrInvalidAdoptExistingAnnotation,
			value,
			obj.GetObjectKind().GroupVersionKind().Kind,
			obj.GetNamespace(),
			obj.GetName(),
		)
	}
}

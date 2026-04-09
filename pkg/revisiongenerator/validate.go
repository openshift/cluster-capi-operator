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

package revisiongenerator

import (
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	// AdoptExistingAnnotation controls whether collision protection is disabled
	// for a specific object, allowing us to adopt an object which already
	// exists on the cluster but is not managed by the CAPI operator.  Permitted
	// values are AdoptExistingAlways and AdoptExistingNever. The annotation is
	// stripped from the object before it is applied to the cluster, but is
	// included in the content hash so that adding or removing the annotation
	// triggers a new revision.
	AdoptExistingAnnotation = operatorstatus.CAPIOperatorIdentifierDomain + "/adopt-existing"

	// AdoptExistingAlways disables collision protection for the annotated
	// object. This allows us to adopt an object which was not created by the
	// CAPI operator.
	AdoptExistingAlways = "always"

	// AdoptExistingNever does not disable collision protection for the
	// annotated object. This is the default behavior and is equivalent to not
	// setting the annotation at all.
	AdoptExistingNever = "never"
)

// ErrInvalidAdoptExistingAnnotation is returned when an object has an
// adopt-existing annotation with an unrecognised value.
var ErrInvalidAdoptExistingAnnotation = errors.New("invalid " + AdoptExistingAnnotation + " annotation value")

// ValidateAdoptExistingAnnotation returns an error if the object has an
// adopt-existing annotation with an unrecognised value.
func ValidateAdoptExistingAnnotation(obj client.Object) error {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return nil
	}

	value, exists := annotations[AdoptExistingAnnotation]
	if !exists {
		return nil
	}

	switch value {
	case AdoptExistingAlways, AdoptExistingNever:
		return nil
	default:
		return fmt.Errorf(
			"%w: %q on %s %s/%s",
			reconcile.TerminalError(ErrInvalidAdoptExistingAnnotation),
			value,
			obj.GetObjectKind().GroupVersionKind().Kind,
			obj.GetNamespace(),
			obj.GetName(),
		)
	}
}

// validateRenderedRevision validates all objects in a rendered revision.
func validateRenderedRevision(rev *renderedRevision) error {
	for _, component := range rev.components {
		for _, obj := range component.CRDs() {
			if err := ValidateAdoptExistingAnnotation(obj); err != nil {
				return err
			}
		}

		for _, obj := range component.Objects() {
			if err := ValidateAdoptExistingAnnotation(obj); err != nil {
				return err
			}
		}
	}

	return nil
}

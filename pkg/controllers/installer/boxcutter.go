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
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"pkg.package-operator.run/boxcutter"
	"pkg.package-operator.run/boxcutter/probing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

// toBoxcutterRevision converts an InstallerRevision to a boxcutter.Revision.
// Each component in an InstallerRevision contains a parsed collection of
// objects with no further ordering. toBoxcutterRevision creates a Revision
// containing the following phases for each of these components:
// - a phase for the component's CRDs, if any
// - a phase for the component's remaining objects, if any
//
// Although this is not yet the case, toBoxcutterRevision is intended to capture
// all processing required only at installation time. Ideally InstallerRevision
// will contain only parsed versions of exactly what was in provider manifests
// without any further processing. That processing should be done here.
func toBoxcutterRevision(
	installerRevision revisiongenerator.InstallerRevision,
	collectObjects func(obj *unstructured.Unstructured),
	unmanagedCRDs []string,
) (boxcutter.Revision, error) {
	probeOpts := util.SliceMap(allProbes(), func(p *probing.GroupKindSelector) boxcutter.PhaseReconcileOption {
		return boxcutter.WithProbe(boxcutter.ProgressProbeType, p)
	})

	var phases []boxcutter.Phase

	addPhase := func(name string, objects []*unstructured.Unstructured) {
		if len(objects) == 0 {
			return
		}

		objects, adoptOpts := processAdoptExistingAnnotations(objects)
		bcPhase := boxcutter.NewPhase(name, util.SliceMap(objects, toClientObject)).
			WithReconcileOptions(append(probeOpts, adoptOpts...)...)

		phases = append(phases, bcPhase)
	}

	unmanagedSet := sets.New(unmanagedCRDs...)
	var compatObjects []*unstructured.Unstructured

	for _, component := range installerRevision.Components() {
		// Step 1: Transform — replace unmanaged CRDs with CompatibilityRequirements.
		// Future transformations (proxy env vars, etc.) chain here before the split.
		var transformed []*unstructured.Unstructured

		for _, obj := range component.Objects() {
			collectObjects(obj)

			if isCRD(obj) && unmanagedSet.Has(obj.GetName()) {
				unmanagedSet.Delete(obj.GetName())

				cr, err := buildCompatibilityRequirement(obj)
				if err != nil {
					return nil, fmt.Errorf("building CompatibilityRequirement for CRD %s: %w", obj.GetName(), err)
				}

				labels := cr.GetLabels()
				if labels == nil {
					labels = make(map[string]string)
				}

				labels[revisiongenerator.ManagedLabelKey] = "compatibility-requirements"
				cr.SetLabels(labels)

				collectObjects(cr)
				compatObjects = append(compatObjects, cr)

				// Unmanaged CRD intentionally dropped — not installed, only checked for compatibility.
				continue
			}

			transformed = append(transformed, obj)
		}

		// Step 2: Split transformed objects into CRDs and non-CRDs, build phases.
		var crds, objects []*unstructured.Unstructured
		for _, obj := range transformed {
			if isCRD(obj) {
				crds = append(crds, obj)
			} else {
				objects = append(objects, obj)
			}
		}

		addPhase(component.Name()+"-crds", crds)
		addPhase(component.Name(), objects)
	}

	if unmanagedSet.Len() > 0 {
		return nil, fmt.Errorf("unmanaged CRDs not found in any component: %v", sets.List(unmanagedSet))
	}

	// Prepend compatibility phase before all component phases.
	if len(compatObjects) > 0 {
		compatPhase := boxcutter.NewPhase("compatibility-requirements", util.SliceMap(compatObjects, toClientObject)).
			WithReconcileOptions(probeOpts...)
		phases = append([]boxcutter.Phase{compatPhase}, phases...)
	}

	return boxcutter.NewRevision(
		string(installerRevision.RevisionName()),
		installerRevision.RevisionIndex(),
		phases,
	), nil
}

func isCRD(obj *unstructured.Unstructured) bool {
	return obj.GetObjectKind().GroupVersionKind().GroupKind() == schema.GroupKind{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}
}

// processAdoptExistingAnnotations processes the adopt-existing annotation on
// each object. Objects with the annotation are deep copied and the annotation
// is stripped from the copy. Objects with "always" get a per-object
// CollisionProtectionIfNoController option. Objects without the annotation are
// returned unchanged.
//
// This function assumes that annotation values have already been validated
// during revision creation.
func processAdoptExistingAnnotations(objects []*unstructured.Unstructured) ([]*unstructured.Unstructured, []boxcutter.PhaseReconcileOption) {
	var reconcileOpts []boxcutter.PhaseReconcileOption

	return util.SliceMap(objects, func(obj *unstructured.Unstructured) *unstructured.Unstructured {
		annotations := obj.GetAnnotations()
		value, hasAnnotation := annotations[revisiongenerator.AdoptExistingAnnotation]

		if hasAnnotation {
			// Disable collision protection if the annotation is set to "always"
			if value == revisiongenerator.AdoptExistingAlways {
				reconcileOpts = append(reconcileOpts,
					boxcutter.WithObjectReconcileOptions(obj,
						boxcutter.WithCollisionProtection(boxcutter.CollisionProtectionNone),
					),
				)
			}

			// Strip the annotation from the object before returning it
			obj = obj.DeepCopy()
			annotationsCopy := obj.GetAnnotations()
			delete(annotationsCopy, revisiongenerator.AdoptExistingAnnotation)
			obj.SetAnnotations(annotationsCopy)
		}

		return obj
	}), reconcileOpts
}

func toClientObject(obj *unstructured.Unstructured) client.Object {
	return obj
}

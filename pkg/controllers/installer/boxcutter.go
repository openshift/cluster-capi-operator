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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"pkg.package-operator.run/boxcutter"
	"pkg.package-operator.run/boxcutter/probing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

func toClientObject(obj *unstructured.Unstructured) client.Object {
	return obj
}

// toBoxcutterRevision converts an InstallerRevision to a boxcutter.Revision.
func toBoxcutterRevision(installerRevision revisiongenerator.InstallerRevision, collectObjects func(obj *unstructured.Unstructured)) boxcutter.Revision {
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

	for _, component := range installerRevision.Components() {
		var crds, objects []*unstructured.Unstructured

		for _, obj := range component.Objects() {
			collectObjects(obj)

			gvk := obj.GetObjectKind().GroupVersionKind()
			if gvk.GroupKind() == (schema.GroupKind{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}) {
				crds = append(crds, obj)
			} else {
				objects = append(objects, obj)
			}
		}

		addPhase(component.Name()+"-crds", crds)
		addPhase(component.Name(), objects)
	}

	return boxcutter.NewRevision(
		string(installerRevision.RevisionName()),
		installerRevision.RevisionIndex(),
		phases,
	)
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

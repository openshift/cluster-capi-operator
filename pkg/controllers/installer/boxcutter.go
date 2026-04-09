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
	"pkg.package-operator.run/boxcutter"
	"pkg.package-operator.run/boxcutter/probing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

func toBoxcutterRevision(installerRevision revisiongenerator.InstallerRevision) boxcutter.Revision {
	return boxcutterRevision{revision: installerRevision}
}

// boxcutterRevision wraps an InstallerRevision and provides a boxcutter.Revision implementation.
type boxcutterRevision struct {
	revision revisiongenerator.InstallerRevision
}

var _ boxcutter.Revision = boxcutterRevision{}

// GetName returns the name of the revision.
func (r boxcutterRevision) GetName() string {
	return string(r.revision.RevisionName())
}

// GetRevisionNumber returns the revision number of the revision.
func (r boxcutterRevision) GetRevisionNumber() int64 {
	return r.revision.RevisionIndex()
}

// GetPhases returns the phases of the revision.
func (r boxcutterRevision) GetPhases() []boxcutter.Phase {
	probeOpts := util.SliceMap(allProbes(), func(p *probing.GroupKindSelector) boxcutter.PhaseReconcileOption {
		return boxcutter.WithProbe(boxcutter.ProgressProbeType, p)
	})

	var phases []boxcutter.Phase

	for _, component := range r.revision.Components() {
		if crds := component.CRDs(); len(crds) > 0 {
			objects, adoptOpts := processAdoptExistingAnnotations(crds)
			phases = append(phases, boxcutterPhase{
				name:             component.Name() + "-crds",
				objects:          objects,
				reconcileOptions: append(probeOpts, adoptOpts...),
			})
		}

		if objects := component.Objects(); len(objects) > 0 {
			objects, adoptOpts := processAdoptExistingAnnotations(objects)
			phases = append(phases, boxcutterPhase{
				name:             component.Name(),
				objects:          objects,
				reconcileOptions: append(probeOpts, adoptOpts...),
			})
		}
	}

	return phases
}

// processAdoptExistingAnnotations processes the adopt-existing annotation on
// each object. Objects with the annotation are deep copied and the annotation
// is stripped from the copy. Objects with "always" get a per-object
// CollisionProtectionIfNoController option. Objects without the annotation are
// returned unchanged.
//
// This function assumes that annotation values have already been validated
// during revision creation.
func processAdoptExistingAnnotations(objects []client.Object) ([]client.Object, []boxcutter.PhaseReconcileOption) {
	var reconcileOpts []boxcutter.PhaseReconcileOption

	return util.SliceMap(objects, func(obj client.Object) client.Object {
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
			obj = obj.DeepCopyObject().(client.Object) //nolint:forcetypeassert // This is guaranteed to be client.Object because obj is client.Object
			annotationsCopy := obj.GetAnnotations()
			delete(annotationsCopy, revisiongenerator.AdoptExistingAnnotation)
			obj.SetAnnotations(annotationsCopy)
		}

		return obj
	}), reconcileOpts
}

// GetReconcileOptions returns the reconcile options of the revision.
func (r boxcutterRevision) GetReconcileOptions() []boxcutter.RevisionReconcileOption {
	return nil
}

// GetTeardownOptions returns the teardown options of the revision.
func (r boxcutterRevision) GetTeardownOptions() []boxcutter.RevisionTeardownOption {
	return nil
}

type boxcutterPhase struct {
	name             string
	objects          []client.Object
	reconcileOptions []boxcutter.PhaseReconcileOption
}

var _ boxcutter.Phase = boxcutterPhase{}

// GetName returns the name of the phase.
func (p boxcutterPhase) GetName() string {
	return p.name
}

// GetObjects returns the objects of the phase.
func (p boxcutterPhase) GetObjects() []client.Object {
	return p.objects
}

// GetReconcileOptions returns the reconcile options of the phase.
func (p boxcutterPhase) GetReconcileOptions() []boxcutter.PhaseReconcileOption {
	return p.reconcileOptions
}

// GetTeardownOptions returns the teardown options of the phase.
func (p boxcutterPhase) GetTeardownOptions() []boxcutter.PhaseTeardownOption {
	return nil
}

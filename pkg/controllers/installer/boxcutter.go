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
	return boxcutterRevision{
		revision: installerRevision,
	}
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
			phases = append(phases, boxcutterPhase{
				name:             component.Name() + "-crds",
				objects:          crds,
				reconcileOptions: probeOpts,
			})
		}

		if objects := component.Objects(); len(objects) > 0 {
			phases = append(phases, boxcutterPhase{
				name:             component.Name(),
				objects:          objects,
				reconcileOptions: probeOpts,
			})
		}
	}

	return phases
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

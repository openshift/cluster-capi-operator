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
	"context"
	"fmt"
	"slices"

	"pkg.package-operator.run/boxcutter"
	"pkg.package-operator.run/boxcutter/probing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/runtimetransformer"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

// toBoxcutterRevision converts an InstallerRevision to a boxcutter.Revision, pre-computing
// all phases so that GetPhases is a trivial getter with no further work.
// Each RuntimeTransformer is called for every object before phase construction.
func toBoxcutterRevision(ctx context.Context, installerRevision revisiongenerator.InstallerRevision, transformers []runtimetransformer.RuntimeTransformer) (boxcutter.Revision, error) {
	probeOpts := util.SliceMap(allProbes(), func(p *probing.GroupKindSelector) boxcutter.PhaseReconcileOption {
		return boxcutter.WithProbe(boxcutter.ProgressProbeType, p)
	})

	var phases []boxcutter.Phase

	for _, component := range installerRevision.Components() {
		if crds := component.CRDs(); len(crds) > 0 {
			xfmrOpts, err := applyTransformers(ctx, transformers, crds)
			if err != nil {
				return nil, err
			}

			allOpts := slices.Concat(probeOpts, xfmrOpts)
			phases = append(phases, boxcutter.NewPhase(component.Name()+"-crds", crds).
				WithReconcileOptions(allOpts...))
		}

		if objects := component.Objects(); len(objects) > 0 {
			xfmrOpts, err := applyTransformers(ctx, transformers, objects)
			if err != nil {
				return nil, err
			}

			allOpts := slices.Concat(probeOpts, xfmrOpts)
			phases = append(phases, boxcutter.NewPhase(component.Name(), objects).
				WithReconcileOptions(allOpts...))
		}
	}

	return boxcutter.NewRevision(
		string(installerRevision.RevisionName()),
		installerRevision.RevisionIndex(),
		phases,
	), nil
}

// applyTransformers calls each transformer on each object in order, accumulating
// the phase-level reconcile options they return. It returns on the first error.
func applyTransformers(ctx context.Context, transformers []runtimetransformer.RuntimeTransformer, objects []client.Object) ([]boxcutter.PhaseReconcileOption, error) {
	var opts []boxcutter.PhaseReconcileOption

	for _, obj := range objects {
		for _, t := range transformers {
			objOpts, err := t.TransformObject(ctx, obj)
			if err != nil {
				return nil, fmt.Errorf("transforming %s: %w", client.ObjectKeyFromObject(obj), err)
			}

			opts = append(opts, objOpts...)
		}
	}

	return opts, nil
}

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
	"errors"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/runtime/schema"
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

	revisionTransformers := util.SliceMap(transformers, func(t runtimetransformer.RuntimeTransformer) runtimetransformer.RuntimeTransformer {
		return t.WithRevision(ctx, installerRevision)
	})

	var allErrs []error

	for _, component := range installerRevision.Components() {
		componentTransformers := util.SliceMap(revisionTransformers, func(t runtimetransformer.RuntimeTransformer) runtimetransformer.RuntimeTransformer {
			return t.WithComponent(ctx, component)
		})

		var crds, objects []client.Object

		for _, obj := range component.Objects() {
			gvk := obj.GetObjectKind().GroupVersionKind()
			if gvk.GroupKind() == (schema.GroupKind{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}) {
				crds = append(crds, obj)
			} else {
				objects = append(objects, obj)
			}
		}

		if len(crds) > 0 {
			xfmrOpts, err := applyTransformers(ctx, componentTransformers, crds)
			if err != nil {
				allErrs = append(allErrs, err)
			} else {
				allOpts := slices.Concat(probeOpts, xfmrOpts)
				phases = append(phases, boxcutter.NewPhase(component.Name()+"-crds", crds).
					WithReconcileOptions(allOpts...))
			}
		}

		if len(objects) > 0 {
			xfmrOpts, err := applyTransformers(ctx, componentTransformers, objects)
			if err != nil {
				allErrs = append(allErrs, err)
			} else {
				allOpts := slices.Concat(probeOpts, xfmrOpts)
				phases = append(phases, boxcutter.NewPhase(component.Name(), objects).
					WithReconcileOptions(allOpts...))
			}
		}
	}

	if len(allErrs) > 0 {
		return nil, errors.Join(allErrs...)
	}

	return boxcutter.NewRevision(
		string(installerRevision.RevisionName()),
		installerRevision.RevisionIndex(),
		phases,
	), nil
}

// applyTransformers calls each transformer on each object in order, accumulating
// the phase-level reconcile options they return. Errors from every object and
// transformer are collected and joined via errors.Join.
func applyTransformers(ctx context.Context, transformers []runtimetransformer.RuntimeTransformer, objects []client.Object) ([]boxcutter.PhaseReconcileOption, error) {
	var (
		opts    []boxcutter.PhaseReconcileOption
		allErrs []error
	)

	for _, obj := range objects {
		var objErrs []error

		for _, x := range transformers {
			objOpts, err := x.TransformObject(ctx, obj)
			if err != nil {
				objErrs = append(objErrs, err)
			} else {
				opts = append(opts, objOpts...)
			}
		}

		if len(objErrs) > 0 {
			allErrs = append(allErrs, fmt.Errorf("transforming %s: %w", client.ObjectKeyFromObject(obj), errors.Join(objErrs...)))
		}
	}

	if len(allErrs) > 0 {
		return nil, errors.Join(allErrs...)
	}

	return opts, nil
}

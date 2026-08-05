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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"pkg.package-operator.run/boxcutter"
	"pkg.package-operator.run/boxcutter/probing"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/cluster-capi-operator/pkg/manifesttransformer"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

func toClientObject(obj *unstructured.Unstructured) client.Object {
	return obj
}

// toBoxcutterRevision converts an InstallerRevision to a boxcutter.Revision.
// Each ManifestTransformer is called for every object before phase construction.
func toBoxcutterRevision(ctx context.Context, installerRevision revisiongenerator.InstallerRevision, transformers []manifesttransformer.ManifestTransformer, collectObjects func(obj *unstructured.Unstructured)) (boxcutter.Revision, error) {
	probeOpts := util.SliceMap(allProbes(), func(p *probing.GroupKindSelector) boxcutter.PhaseReconcileOption {
		return boxcutter.WithProbe(boxcutter.ProgressProbeType, p)
	})

	var phases []boxcutter.Phase

	withRevision := func(t manifesttransformer.ManifestTransformer) manifesttransformer.ManifestTransformer {
		return t.WithRevision(ctx, installerRevision)
	}
	revisionTransformers := util.SliceMap(transformers, withRevision)

	var allErrs []error

	for _, component := range installerRevision.Components() {
		withComponent := func(t manifesttransformer.ManifestTransformer) manifesttransformer.ManifestTransformer {
			return t.WithComponent(ctx, component)
		}
		componentTransformers := util.SliceMap(revisionTransformers, withComponent)

		var crds, objects []*unstructured.Unstructured

		for _, obj := range component.Objects() {
			if collectObjects != nil {
				collectObjects(obj)
			}

			gvk := obj.GetObjectKind().GroupVersionKind()
			if gvk.GroupKind() == (schema.GroupKind{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}) {
				crds = append(crds, obj)
			} else {
				objects = append(objects, obj)
			}
		}

		var err error

		if phases, err = addPhase(ctx, phases, probeOpts, component.Name()+"-crds", crds, componentTransformers); err != nil {
			allErrs = append(allErrs, err)
		}

		if phases, err = addPhase(ctx, phases, probeOpts, component.Name(), objects, componentTransformers); err != nil {
			allErrs = append(allErrs, err)
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

func addPhase(ctx context.Context, phases []boxcutter.Phase, probeOpts []boxcutter.PhaseReconcileOption, name string, objects []*unstructured.Unstructured, ctxTransformers []manifesttransformer.ManifestTransformer) ([]boxcutter.Phase, error) {
	if len(objects) == 0 {
		return phases, nil
	}

	var (
		xfmrOpts []boxcutter.PhaseReconcileOption
		allErrs  []error
	)

	transformedObjects := make([]*unstructured.Unstructured, 0, len(objects))

	for _, obj := range objects {
		transformedObj, objOpts, objErrs := applyTransformers(ctx, ctxTransformers, obj)
		if len(objErrs) > 0 {
			allErrs = append(allErrs, fmt.Errorf("transforming %s %s: %w", obj.GroupVersionKind(), client.ObjectKeyFromObject(obj), errors.Join(objErrs...)))
			continue
		}

		// A nil object means a transformer chose to skip it; it must not appear in any phase.
		if transformedObj == nil {
			continue
		}

		if len(objOpts) > 0 {
			xfmrOpts = append(xfmrOpts, boxcutter.WithObjectReconcileOptions(transformedObj, objOpts...))
		}

		transformedObjects = append(transformedObjects, transformedObj)
	}

	allOpts := slices.Concat(probeOpts, xfmrOpts)
	bcPhase := boxcutter.NewPhase(name, util.SliceMap(transformedObjects, toClientObject)).WithReconcileOptions(allOpts...)

	return append(phases, bcPhase), errors.Join(allErrs...)
}

// applyTransformers applies all transformers to an object in order, accumulating
// all boxcutter reconcile options and errors they return.
func applyTransformers(ctx context.Context, transformers []manifesttransformer.ManifestTransformer, obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, []error) {
	var (
		errs    []error
		allOpts []boxcutter.ObjectReconcileOption
	)

	for _, t := range transformers {
		transformedObj, opts, err := t.TransformObject(ctx, obj)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		// If the transformer returns a nil object, it means the object should be skipped.
		if transformedObj == nil {
			return nil, opts, errs
		}

		allOpts = append(allOpts, opts...)

		obj = transformedObj
	}

	return obj, allOpts, errs
}

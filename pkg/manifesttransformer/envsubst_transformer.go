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
	"fmt"
	"maps"

	"github.com/drone/envsubst/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pkg.package-operator.run/boxcutter"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

// EnvsubstTransformer expands envsubst variables in every string value of an
// object at install time. staticSubs are configured at construction and take
// precedence over substitutions recorded on the revision.
type EnvsubstTransformer struct {
	staticSubs map[string]string
	mergedSubs map[string]string
}

var _ ManifestTransformer = &EnvsubstTransformer{}

// NewEnvsubstTransformer creates an EnvsubstTransformer with the given static
// substitutions. Static substitutions take precedence over revision-level
// substitutions. Pass nil for no static substitutions.
func NewEnvsubstTransformer(staticSubs map[string]string) *EnvsubstTransformer {
	return &EnvsubstTransformer{
		staticSubs: maps.Clone(staticSubs),
	}
}

// TransformObject returns a copy of obj with envsubst variables expanded in
// every string value, using the revision's substitutions merged with any
// static substitutions (which take precedence).
func (e *EnvsubstTransformer) TransformObject(_ context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
	obj = obj.DeepCopy()

	if err := expandMapStrings(obj.Object, e.mergedSubs); err != nil {
		return nil, nil, fmt.Errorf("expanding envsubst variables in %s: %w", obj.GetName(), err)
	}

	return obj, nil, nil
}

// Validate is a no-op for EnvsubstTransformer.
func (e *EnvsubstTransformer) Validate(_ *unstructured.Unstructured) error {
	return nil
}

// WithRevision returns a new EnvsubstTransformer that merges the revision's
// ManifestSubstitutions with the static substitutions. Static substitutions
// take precedence.
func (e *EnvsubstTransformer) WithRevision(_ context.Context, revision revisiongenerator.ParsedRevision) ManifestTransformer {
	merged := revision.ManifestSubstitutions()
	if merged == nil {
		merged = make(map[string]string, len(e.staticSubs))
	}

	maps.Copy(merged, e.staticSubs)

	return &EnvsubstTransformer{
		staticSubs: e.staticSubs,
		mergedSubs: merged,
	}
}

// WithComponent is a no-op; envsubst expansion does not need component context.
func (e *EnvsubstTransformer) WithComponent(_ context.Context, _ revisiongenerator.ParsedComponent) ManifestTransformer {
	return e
}

// expandMapStrings recursively walks a map[string]interface{} and calls
// envsubst.Eval on every string leaf value.
func expandMapStrings(m map[string]interface{}, subs map[string]string) error {
	for k, val := range m {
		expanded, err := expandValue(val, subs)
		if err != nil {
			return err
		}

		m[k] = expanded
	}

	return nil
}

// expandSliceStrings recursively walks a []interface{} and calls envsubst.Eval
// on every string element.
func expandSliceStrings(s []interface{}, subs map[string]string) error {
	for i, elem := range s {
		expanded, err := expandValue(elem, subs)
		if err != nil {
			return err
		}

		s[i] = expanded
	}

	return nil
}

// expandValue expands a single value: strings are envsubst-expanded, maps and
// slices are walked recursively, and all other types are returned unchanged.
func expandValue(val interface{}, subs map[string]string) (interface{}, error) {
	switch t := val.(type) {
	case string:
		expanded, err := envsubst.Eval(t, func(key string) string {
			return subs[key]
		})
		if err != nil {
			return nil, fmt.Errorf("envsubst.Eval: %w", err)
		}

		return expanded, nil
	case map[string]interface{}:
		if err := expandMapStrings(t, subs); err != nil {
			return nil, err
		}

		return t, nil
	case []interface{}:
		if err := expandSliceStrings(t, subs); err != nil {
			return nil, err
		}

		return t, nil
	default:
		return val, nil
	}
}

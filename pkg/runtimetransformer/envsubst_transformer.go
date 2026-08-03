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
	"maps"
	"strconv"

	"github.com/drone/envsubst/v2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pkg.package-operator.run/boxcutter"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

var errNotUnstructured = errors.New("EnvsubstTransformer: expected *unstructured.Unstructured")

// EnvsubstTransformer expands envsubst variables in every string value of an
// Unstructured object at install time. staticSubs are configured at
// construction and take precedence over revision-level substitutions.
type EnvsubstTransformer struct {
	staticSubs map[string]string
	mergedSubs map[string]string
}

var _ RuntimeTransformer = &EnvsubstTransformer{}

// NewEnvsubstTransformer creates an EnvsubstTransformer with the given static
// substitutions. Static substitutions take precedence over revision-level
// substitutions. Pass nil for no static substitutions.
func NewEnvsubstTransformer(staticSubs map[string]string) *EnvsubstTransformer {
	return &EnvsubstTransformer{
		staticSubs: maps.Clone(staticSubs),
	}
}

// WithRevision returns a new EnvsubstTransformer that merges the revision's
// ManifestSubstitutions with the static substitutions. Static substitutions
// take precedence.
func (e *EnvsubstTransformer) WithRevision(_ context.Context, revision revisiongenerator.ParsedRevision) RuntimeTransformer {
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
func (e *EnvsubstTransformer) WithComponent(_ context.Context, _ revisiongenerator.ParsedComponent) RuntimeTransformer {
	return e
}

// TransformObject expands envsubst variables in all string values of obj.
// obj must be an *unstructured.Unstructured.
func (e *EnvsubstTransformer) TransformObject(_ context.Context, obj client.Object) ([]boxcutter.PhaseReconcileOption, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		// This is a programming contract violation.
		return nil, fmt.Errorf("%w, got %T", reconcile.TerminalError(errNotUnstructured), obj)
	}

	if errs := expandMapStrings(u.Object, e.mergedSubs); len(errs) > 0 {
		return nil, fmt.Errorf("expanding envsubst variables in %s: %w", obj.GetName(), reconcile.TerminalError(errors.Join(errs...)))
	}

	return nil, nil
}

// Validate checks that all string values in obj can be parsed as envsubst
// templates, catching malformed expressions (e.g. unclosed braces) before
// install time.
func (e *EnvsubstTransformer) Validate(obj client.Object) error {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("%w, got %T", reconcile.TerminalError(errNotUnstructured), obj)
	}

	if errs := validateMapStrings(u.Object); len(errs) > 0 {
		return fmt.Errorf("validating envsubst variables in %s: %w", obj.GetName(), reconcile.TerminalError(errors.Join(errs...)))
	}

	return nil
}

// walkUnstructured recursively traverses map[string]any and []any, applying fn to string leaves.
func walkUnstructured(key string, val any, fn func(key string, s string) (any, error)) (any, []error) {
	switch t := val.(type) {
	case string:
		value, err := fn(key, t)
		if err != nil {
			return nil, []error{err}
		}

		return value, nil
	case map[string]any:
		var reterrs []error

		for k, elem := range t {
			value, errs := walkUnstructured(key+"."+k, elem, fn)
			reterrs = append(reterrs, errs...)
			t[k] = value
		}

		return t, reterrs
	case []any:
		var reterrs []error

		for i, elem := range t {
			value, errs := walkUnstructured(key+"["+strconv.Itoa(i)+"]", elem, fn)
			reterrs = append(reterrs, errs...)
			t[i] = value
		}

		return t, reterrs
	default:
		return val, nil
	}
}

func expandMapStrings(m map[string]any, subs map[string]string) []error {
	_, err := walkUnstructured("", m, func(key, s string) (any, error) {
		expanded, err := envsubst.Eval(s, func(k string) string {
			return subs[k]
		})
		if err != nil {
			return nil, fmt.Errorf("invalid envsubst expression at %q: %w", key, err)
		}

		return expanded, nil
	})

	return err
}

func validateMapStrings(m map[string]any) []error {
	_, err := walkUnstructured("", m, func(key, s string) (any, error) {
		if _, err := envsubst.Parse(s); err != nil {
			return nil, fmt.Errorf("invalid envsubst expression at %q: %w", key, err)
		}

		return s, nil
	})

	return err
}

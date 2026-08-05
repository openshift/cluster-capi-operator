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
	"sync/atomic"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pkg.package-operator.run/boxcutter"

	"github.com/openshift/cluster-capi-operator/pkg/manifesttransformer"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

// testAnnotationKey is the annotation testValueTransformer writes.
const testAnnotationKey = "test.openshift.io/transformer-value"

// testAnnotationValue is set/cleared directly by tests to control what
// testValueTransformer stamps onto objects on the next reconcile. nil means
// "do nothing" -- the no-op path already exercised by every other test in
// the suite, which never touches this variable.
var testAnnotationValue atomic.Pointer[string]

// testValueTransformer stamps obj with whatever testAnnotationValue
// currently holds. It exists purely to let tests simulate a ManifestTransformer
// whose output changes between reconciles without a new revision, so drift
// correction of transformer-derived state can be verified directly.
type testValueTransformer struct{}

var _ manifesttransformer.ManifestTransformer = testValueTransformer{}

// TransformObject implements manifesttransformer.ManifestTransformer.
func (testValueTransformer) TransformObject(_ context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
	val := testAnnotationValue.Load()
	if val == nil {
		return obj, nil, nil
	}

	obj = obj.DeepCopy()

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	annotations[testAnnotationKey] = *val
	obj.SetAnnotations(annotations)

	return obj, nil, nil
}

// Validate implements manifesttransformer.ManifestTransformer.
func (testValueTransformer) Validate(_ *unstructured.Unstructured) error { return nil }

// WithRevision implements manifesttransformer.ManifestTransformer. It is a
// no-op; testValueTransformer does not need revision context.
func (t testValueTransformer) WithRevision(_ context.Context, _ revisiongenerator.RenderedRevision) manifesttransformer.ManifestTransformer {
	return t
}

// WithComponent implements manifesttransformer.ManifestTransformer. It is a
// no-op; testValueTransformer does not need component context.
func (t testValueTransformer) WithComponent(_ context.Context, _ revisiongenerator.RenderedComponent) manifesttransformer.ManifestTransformer {
	return t
}

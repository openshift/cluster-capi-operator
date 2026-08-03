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

	"pkg.package-operator.run/boxcutter"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// testAnnotationKey is the annotation testValueTransformer writes.
const testAnnotationKey = "test.openshift.io/transformer-value"

// testAnnotationValue is set/cleared directly by tests to control what
// testValueTransformer stamps onto objects on the next reconcile. nil means
// "do nothing" -- the no-op path already exercised by every other test in
// the suite, which never touches this variable.
var testAnnotationValue atomic.Pointer[string]

// testValueTransformer stamps obj with whatever testAnnotationValue
// currently holds. It exists purely to let tests simulate a RuntimeTransformer
// whose output changes between reconciles without a new revision, so drift
// correction of transformer-derived state can be verified directly.
type testValueTransformer struct{}

// TransformObject implements runtimetransformer.SimpleRuntimeTransformer.
func (testValueTransformer) TransformObject(_ context.Context, obj client.Object) ([]boxcutter.PhaseReconcileOption, error) {
	val := testAnnotationValue.Load()
	if val == nil {
		return nil, nil
	}

	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}

	annotations[testAnnotationKey] = *val
	obj.SetAnnotations(annotations)

	return nil, nil
}

// Validate implements runtimetransformer.SimpleRuntimeTransformer.
func (testValueTransformer) Validate(client.Object) error { return nil }

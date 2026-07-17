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

	"pkg.package-operator.run/boxcutter"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RuntimeTransformer transforms objects at install time, after YAML
// unmarshalling and before objects are collected into boxcutter phases.
type RuntimeTransformer interface {
	// TransformObject may mutate the object in place and returns any
	// phase-level reconcile options that should apply to that object's phase.
	TransformObject(ctx context.Context, obj client.Object) ([]boxcutter.PhaseReconcileOption, error)

	// Validate checks that the object is valid for this transformer. An error
	// prevents revision creation and is treated as non-retryable.
	Validate(obj client.Object) error
}

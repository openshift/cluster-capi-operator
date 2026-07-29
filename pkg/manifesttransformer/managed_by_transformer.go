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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"pkg.package-operator.run/boxcutter"

	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/revisiongenerator"
)

// ManagedLabelKey is a label key used to identify objects managed by the CAPI operator.
const ManagedLabelKey = operatorstatus.CAPIOperatorIdentifierDomain + "/managed-by"

// ManagedByTransformer adds a managed-by label to every object at install time.
// The label value is set to the component name via WithComponent.
type ManagedByTransformer struct {
	componentName string
}

var _ ManifestTransformer = &ManagedByTransformer{}

// NewManagedByTransformer creates a ManagedByTransformer. The component name
// is populated per-component via WithComponent.
func NewManagedByTransformer() *ManagedByTransformer {
	return &ManagedByTransformer{}
}

// TransformObject returns a copy of obj with ManagedLabelKey=componentName added.
func (m *ManagedByTransformer) TransformObject(_ context.Context, obj *unstructured.Unstructured) (*unstructured.Unstructured, []boxcutter.ObjectReconcileOption, error) {
	obj = obj.DeepCopy()

	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	labels[ManagedLabelKey] = m.componentName
	obj.SetLabels(labels)

	return obj, nil, nil
}

// Validate is a no-op for ManagedByTransformer.
func (m *ManagedByTransformer) Validate(_ *unstructured.Unstructured) error {
	return nil
}

// WithRevision is a no-op; managed-by labelling does not need revision context.
func (m *ManagedByTransformer) WithRevision(_ context.Context, _ revisiongenerator.RenderedRevision) ManifestTransformer {
	return m
}

// WithComponent returns a new ManagedByTransformer with the component name set.
func (m *ManagedByTransformer) WithComponent(_ context.Context, component revisiongenerator.RenderedComponent) ManifestTransformer {
	return &ManagedByTransformer{componentName: component.Name()}
}

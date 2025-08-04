/*
Copyright 2020 The Kubernetes Authors.
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

package v1alpha1

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Deprecated: This method is going to be removed in a next release.
func (webhook *IPClaim) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(webhook).
		WithDefaulter(webhook, admission.DefaulterRemoveUnknownOrOmitableFields).
		WithValidator(webhook).
		Complete()
}

var _ webhook.CustomDefaulter = &IPClaim{}
var _ webhook.CustomValidator = &IPClaim{}

// Deprecated: This method is going to be removed in a next release.
func (webhook *IPClaim) Default(_ context.Context, _ runtime.Object) error {
	return nil
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *IPClaim) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	c, ok := obj.(*IPClaim)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a IPClaim but got a %T", obj))
	}

	allErrs := field.ErrorList{}
	if c.Spec.Pool.Name == "" {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "pool", "name"),
				c.Spec.Pool.Name,
				"cannot be empty",
			),
		)
	}

	if len(allErrs) == 0 {
		return nil, nil
	}
	return nil, apierrors.NewInvalid(GroupVersion.WithKind("IPClaim").GroupKind(), c.Name, allErrs)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *IPClaim) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	allErrs := field.ErrorList{}
	oldIPClaim, ok := oldObj.(*IPClaim)
	if !ok || oldIPClaim == nil {
		return nil, apierrors.NewInternalError(errors.New("unable to convert existing object"))
	}

	newIPClaim, ok := newObj.(*IPClaim)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a IPClaim but got a %T", newObj))
	}

	if newIPClaim.Spec.Pool.Name != oldIPClaim.Spec.Pool.Name {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "pool"),
				newIPClaim.Spec.Pool,
				"cannot be modified",
			),
		)
	} else if newIPClaim.Spec.Pool.Namespace != oldIPClaim.Spec.Pool.Namespace {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "pool"),
				newIPClaim.Spec.Pool,
				"cannot be modified",
			),
		)
	} else if newIPClaim.Spec.Pool.Kind != oldIPClaim.Spec.Pool.Kind {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "pool"),
				newIPClaim.Spec.Pool,
				"cannot be modified",
			),
		)
	}

	if len(allErrs) == 0 {
		return nil, nil
	}
	return nil, apierrors.NewInvalid(GroupVersion.WithKind("IPClaim").GroupKind(), newIPClaim.Name, allErrs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *IPClaim) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

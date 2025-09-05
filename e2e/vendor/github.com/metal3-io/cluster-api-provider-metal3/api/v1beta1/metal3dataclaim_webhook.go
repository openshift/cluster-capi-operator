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

package v1beta1

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
func (webhook *Metal3DataClaim) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(webhook).
		WithDefaulter(webhook, admission.DefaulterRemoveUnknownOrOmitableFields).
		WithValidator(webhook).
		Complete()
}

var _ webhook.CustomDefaulter = &Metal3DataClaim{}
var _ webhook.CustomValidator = &Metal3DataClaim{}

// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3DataClaim) Default(_ context.Context, _ runtime.Object) error {
	return nil
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3DataClaim) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	c, ok := obj.(*Metal3DataClaim)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a Metal3DataClaim but got a %T", obj))
	}

	allErrs := field.ErrorList{}
	if c.Spec.Template.Name == "" {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "template", "name"),
				c.Spec.Template.Name,
				"must be set",
			),
		)
	}

	if len(allErrs) == 0 {
		return nil, nil
	}
	return nil, apierrors.NewInvalid(GroupVersion.WithKind("Metal3DataClaim").GroupKind(), c.Name, allErrs)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3DataClaim) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	allErrs := field.ErrorList{}

	newMetal3DataClaim, ok := newObj.(*Metal3DataClaim)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a Metal3DataClaim but got a %T", newObj))
	}

	oldMetal3DataClaim, ok := oldObj.(*Metal3DataClaim)
	if !ok || oldMetal3DataClaim == nil {
		return nil, apierrors.NewInternalError(errors.New("unable to convert existing object"))
	}

	if newMetal3DataClaim.Spec.Template.Name != oldMetal3DataClaim.Spec.Template.Name {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "template"),
				newMetal3DataClaim.Spec.Template,
				"cannot be modified",
			),
		)
	} else if newMetal3DataClaim.Spec.Template.Namespace != oldMetal3DataClaim.Spec.Template.Namespace {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "template"),
				newMetal3DataClaim.Spec.Template,
				"cannot be modified",
			),
		)
	} else if newMetal3DataClaim.Spec.Template.Kind != oldMetal3DataClaim.Spec.Template.Kind {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "template"),
				newMetal3DataClaim.Spec.Template,
				"cannot be modified",
			),
		)
	}

	if len(allErrs) == 0 {
		return nil, nil
	}
	return nil, apierrors.NewInvalid(GroupVersion.WithKind("Metal3DataClaim").GroupKind(), newMetal3DataClaim.Name, allErrs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3DataClaim) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

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
	"strconv"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3Data) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(webhook).
		WithDefaulter(webhook, admission.DefaulterRemoveUnknownOrOmitableFields).
		WithValidator(webhook).
		Complete()
}

var _ webhook.CustomDefaulter = &Metal3Data{}
var _ webhook.CustomValidator = &Metal3Data{}

// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3Data) Default(_ context.Context, _ runtime.Object) error {
	return nil
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3Data) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	c, ok := obj.(*Metal3Data)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a Metal3Data but got a %T", obj))
	}

	allErrs := field.ErrorList{}
	if (c.Spec.TemplateReference != "" && c.Name != c.Spec.TemplateReference+"-"+strconv.Itoa(c.Spec.Index)) ||
		(c.Spec.TemplateReference == "" && c.Name != c.Spec.Template.Name+"-"+strconv.Itoa(c.Spec.Index)) {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("name"),
				c.Name,
				"should follow the convention <Metal3Template Name>-<index>",
			),
		)
	}

	if c.Spec.Index < 0 {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "Index"),
				c.Spec.Index,
				"must be positive value",
			),
		)
	}

	if len(allErrs) == 0 {
		return nil, nil
	}
	return nil, apierrors.NewInvalid(GroupVersion.WithKind("Metal3Data").GroupKind(), c.Name, allErrs)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3Data) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	allErrs := field.ErrorList{}

	newMetal3Data, ok := newObj.(*Metal3Data)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a Metal3Data but got a %T", newObj))
	}

	oldMetal3Data, ok := oldObj.(*Metal3Data)
	if !ok || oldMetal3Data == nil {
		return nil, apierrors.NewInternalError(errors.New("unable to convert existing object"))
	}

	if newMetal3Data.Spec.Index != oldMetal3Data.Spec.Index {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "Index"),
				newMetal3Data.Spec.Index,
				"cannot be modified",
			),
		)
	}

	if newMetal3Data.Spec.Template.Name != oldMetal3Data.Spec.Template.Name {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "Template"),
				newMetal3Data.Spec.Template,
				"cannot be modified",
			),
		)
	} else if newMetal3Data.Spec.Template.Namespace != oldMetal3Data.Spec.Template.Namespace {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "Template"),
				newMetal3Data.Spec.Template,
				"cannot be modified",
			),
		)
	} else if newMetal3Data.Spec.Template.Kind != oldMetal3Data.Spec.Template.Kind {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "Template"),
				newMetal3Data.Spec.Template,
				"cannot be modified",
			),
		)
	}

	if newMetal3Data.Spec.Claim.Name != oldMetal3Data.Spec.Claim.Name {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "claim"),
				newMetal3Data.Spec.Claim,
				"cannot be modified",
			),
		)
	} else if newMetal3Data.Spec.Claim.Namespace != oldMetal3Data.Spec.Claim.Namespace {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "claim"),
				newMetal3Data.Spec.Claim,
				"cannot be modified",
			),
		)
	} else if newMetal3Data.Spec.Claim.Kind != oldMetal3Data.Spec.Claim.Kind {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "claim"),
				newMetal3Data.Spec.Claim,
				"cannot be modified",
			),
		)
	}

	if len(allErrs) == 0 {
		return nil, nil
	}
	return nil, apierrors.NewInvalid(GroupVersion.WithKind("Metal3Data").GroupKind(), newMetal3Data.Name, allErrs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3Data) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

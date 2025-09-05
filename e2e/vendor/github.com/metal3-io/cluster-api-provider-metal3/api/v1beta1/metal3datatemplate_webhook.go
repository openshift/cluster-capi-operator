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
	"reflect"
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
func (webhook *Metal3DataTemplate) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(webhook).
		WithDefaulter(webhook, admission.DefaulterRemoveUnknownOrOmitableFields).
		WithValidator(webhook).
		Complete()
}

var _ webhook.CustomDefaulter = &Metal3DataTemplate{}
var _ webhook.CustomValidator = &Metal3DataTemplate{}

// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3DataTemplate) Default(_ context.Context, _ runtime.Object) error {
	return nil
}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3DataTemplate) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	c, ok := obj.(*Metal3DataTemplate)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a Metal3DataTemplate but got a %T", obj))
	}

	return nil, webhook.validate(nil, c)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3DataTemplate) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	allErrs := field.ErrorList{}

	newM3dt, ok := newObj.(*Metal3DataTemplate)
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected a Metal3DataTemplate but got a %T", newObj))
	}

	oldM3dt, ok := oldObj.(*Metal3DataTemplate)
	if !ok || oldM3dt == nil {
		return nil, apierrors.NewInternalError(errors.New("unable to convert existing object"))
	}

	if !reflect.DeepEqual(newM3dt.Spec.MetaData, oldM3dt.Spec.MetaData) {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "MetaData"),
				newM3dt.Spec.MetaData,
				"cannot be modified",
			),
		)
	}

	if !reflect.DeepEqual(newM3dt.Spec.NetworkData, oldM3dt.Spec.NetworkData) {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "NetworkData"),
				newM3dt.Spec.NetworkData,
				"cannot be modified",
			),
		)
	}

	if len(allErrs) == 0 {
		return nil, nil
	}
	return nil, apierrors.NewInvalid(GroupVersion.WithKind("Metal3Data").GroupKind(), newM3dt.Name, allErrs)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type.
// Deprecated: This method is going to be removed in a next release.
func (webhook *Metal3DataTemplate) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

func (webhook *Metal3DataTemplate) validate(_, newM3dt *Metal3DataTemplate) error {
	var allErrs field.ErrorList

	if newM3dt.Spec.NetworkData != nil {
		for i, network := range newM3dt.Spec.NetworkData.Networks.IPv4 {
			if (network.FromPoolRef == nil || network.FromPoolRef.Name == "") && network.IPAddressFromIPPool == "" {
				allErrs = append(allErrs, field.Required(
					field.NewPath("spec", "networkData", "networks", "ipv4", strconv.Itoa(i), "fromPoolRef", "name"),
					"fromPoolRef needs to contain a reference to an IPPool",
				))
			}
		}
		for i, network := range newM3dt.Spec.NetworkData.Networks.IPv6 {
			if (network.FromPoolRef == nil || network.FromPoolRef.Name == "") && network.IPAddressFromIPPool == "" {
				allErrs = append(allErrs, field.Required(
					field.NewPath("spec", "networkData", "networks", "ipv6", strconv.Itoa(i), "fromPoolRef", "name"),
					"fromPoolRef needs to contain a reference to an IPPool",
				))
			}
		}
	}

	if len(allErrs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(GroupVersion.WithKind("Metal3DataTemplate").GroupKind(), newM3dt.Name, allErrs)
}

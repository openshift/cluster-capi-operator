// Copyright 2025 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package objectvalidation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type crdCompatibilityRequirementContextKey struct{}

// crdCompatibilityRequrementFromContext extracts the name of the CRDCompatibilityRequirement
// out of the context.
func crdCompatibilityRequrementFromContext(ctx context.Context) string {
	v := ctx.Value(crdCompatibilityRequirementContextKey{})

	switch v := v.(type) {
	case string:
		return v
	default:
		// Not reached.
		panic(fmt.Sprintf("unexpected value type for CRDCompatibilityRequirement context key: %T", v))
	}
}

// crdCompatibilityRequrementIntoContext takes the request's .URL.Path, extracts the name
// of the CRDCompatibilityRequirement from it and adds it to the context so it can later
// be read inside the Handle functions.
func crdCompatibilityRequrementIntoContext(ctx context.Context, r *http.Request) context.Context {
	crdCompatibilityRequirementName := strings.TrimPrefix(r.URL.Path, webhookPrefix)
	return context.WithValue(ctx, crdCompatibilityRequirementContextKey{}, crdCompatibilityRequirementName)
}

// Handle handles admission requests.
//
// Note: This function is adapted from sigs.k8s.io/controller-runtime/pkg/webhook/admission/validator_custom.go validatorForType.Handle
// and be compared to that.
func (h *objectValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	if h.decoder == nil {
		panic("decoder should never be nil")
	}

	ctx = admission.NewContextWithRequest(ctx, req)

	crdCompatibilityRequirementName := crdCompatibilityRequrementFromContext(ctx)

	// Get the object in the request
	obj := &unstructured.Unstructured{}

	var err error
	var warnings []string

	switch req.Operation {
	case v1.Connect:
		// No validation for connect requests.
	case v1.Create:
		if err := h.decoder.Decode(req, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		warnings, err = h.ValidateCreate(ctx, crdCompatibilityRequirementName, obj)
	case v1.Update:
		oldObj := &unstructured.Unstructured{}
		if err := h.decoder.DecodeRaw(req.Object, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if err := h.decoder.DecodeRaw(req.OldObject, oldObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		warnings, err = h.ValidateUpdate(ctx, crdCompatibilityRequirementName, oldObj, obj)
	case v1.Delete:
		// In reference to PR: https://github.com/kubernetes/kubernetes/pull/76346
		// OldObject contains the object being deleted
		if err := h.decoder.DecodeRaw(req.OldObject, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		warnings, err = h.ValidateDelete(ctx, crdCompatibilityRequirementName, obj)
	default:
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("unknown operation %q", req.Operation))
	}

	// Check the error message first.
	if err != nil {
		var apiStatus apierrors.APIStatus
		if errors.As(err, &apiStatus) {
			return validationResponseFromStatus(false, apiStatus.Status()).WithWarnings(warnings...)
		}
		return admission.Denied(err.Error()).WithWarnings(warnings...)
	}

	// Return allowed if everything succeeded.
	return admission.Allowed("").WithWarnings(warnings...)
}

func validationResponseFromStatus(allowed bool, status metav1.Status) admission.Response {
	resp := admission.Response{
		AdmissionResponse: admissionv1.AdmissionResponse{
			Allowed: allowed,
			Result:  &status,
		},
	}
	return resp
}

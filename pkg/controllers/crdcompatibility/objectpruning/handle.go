// Copyright 2026 Red Hat, Inc.
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

package objectpruning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type compatibilityRequirementContextKey struct{}

// compatibilityRequrementFromContext extracts the name of the CompatibilityRequirement
// out of the context.
func compatibilityRequrementFromContext(ctx context.Context) string {
	v := ctx.Value(compatibilityRequirementContextKey{})

	switch v := v.(type) {
	case string:
		return v
	default:
		// Not reached.
		panic(fmt.Sprintf("unexpected value type for CompatibilityRequirement context key: %T", v))
	}
}

// compatibilityRequrementIntoContext takes the request's .URL.Path, extracts the name
// of the CompatibilityRequirement from it and adds it to the context so it can later
// be read inside the Handle functions.
func compatibilityRequrementIntoContext(ctx context.Context, r *http.Request) context.Context {
	compatibilityRequirementName := strings.TrimPrefix(r.URL.Path, WebhookPrefix)
	return context.WithValue(ctx, compatibilityRequirementContextKey{}, compatibilityRequirementName)
}

// Handle handles admission requests.
//
// Note: This function is adapted from sigs.k8s.io/controller-runtime/pkg/webhook/admission/defaulter_custom.go defaulterForType.Handle
// and be compared to that.
func (v *validator) Handle(ctx context.Context, req admission.Request) admission.Response {
	if v.decoder == nil {
		panic("decoder should never be nil")
	}

	// Always skip when a DELETE operation received in custom mutation handler.
	if req.Operation == admissionv1.Delete {
		return admission.Response{AdmissionResponse: admissionv1.AdmissionResponse{
			Allowed: true,
			Result: &metav1.Status{
				Code: http.StatusOK,
			},
		}}
	}

	ctx = admission.NewContextWithRequest(ctx, req)
	compatibilityRequirementName := compatibilityRequrementFromContext(ctx)

	// Get the object in the request
	obj := &unstructured.Unstructured{}
	if err := v.decoder.Decode(req, obj); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Default the object
	if err := v.handleObjectPruning(ctx, compatibilityRequirementName, obj); err != nil {
		var apiStatus apierrors.APIStatus
		if errors.As(err, &apiStatus) {
			return validationResponseFromStatus(false, apiStatus.Status())
		}
		return admission.Denied(err.Error())
	}

	// Create the patch
	marshalled, err := json.Marshal(obj)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	handlerResponse := admission.PatchResponseFromRaw(req.Object.Raw, marshalled)
	return handlerResponse
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

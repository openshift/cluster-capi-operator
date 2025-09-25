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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	webhookPrefix = "/crdcompatibility/"
	contextKey    = "CRDCompatibilityRequirementName"
)

func NewObjectValidator() *ObjectValidator {
	return &ObjectValidator{
		// This decoder is only used to decode to unstructured and for CRDCompatibilityRequirements.
		decoder: admission.NewDecoder(runtime.NewScheme()),
	}
}

type ObjectValidator struct {
	client  client.Reader
	decoder admission.Decoder
}

type controllerOption func(*builder.Builder) *builder.Builder

func (o *ObjectValidator) SetupWithManager(ctx context.Context, mgr ctrl.Manager, opts ...controllerOption) error {
	o.client = mgr.GetClient()
	mgr.GetWebhookServer().Register(webhookPrefix+"{CRDCompatibilityRequirement}", &admission.Webhook{
		Handler: o,
		WithContextFunc: func(ctx context.Context, r *http.Request) context.Context {
			crdCompatibilityRequirementName := strings.TrimPrefix(r.URL.Path, webhookPrefix)
			return context.WithValue(ctx, contextKey, crdCompatibilityRequirementName)
		},
	})

	return nil
}

// Handle handles admission requests.
func (o *ObjectValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	if o.client == nil {
		panic("client should never be nil")
	}

	crdCompatibilityRequirementName, ok := ctx.Value(contextKey).(string)
	if !ok {
		admission.Errored(http.StatusBadRequest, fmt.Errorf("expected to have key CRDCompatibilityRequirementName in context"))
	}

	ctx = admission.NewContextWithRequest(ctx, req)

	// Get the object in the request
	obj := &unstructured.Unstructured{}

	var err error
	var warnings []string

	fmt.Printf("Handling CRDCompatibilityRequirementName=%s %q %s %s/%s", crdCompatibilityRequirementName, req.Operation, req.Kind, req.Namespace, req.Name)

	switch req.Operation {
	case v1.Connect:
		// No validation for connect requests.
		// TODO(vincepri): Should we validate CONNECT requests? In what cases?
	case v1.Create:
		if err := o.decoder.Decode(req, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		warnings, err = o.ValidateCreate(ctx, crdCompatibilityRequirementName, obj)
	case v1.Update:
		oldObj := &unstructured.Unstructured{}
		if err := o.decoder.DecodeRaw(req.Object, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if err := o.decoder.DecodeRaw(req.OldObject, oldObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		warnings, err = o.ValidateUpdate(ctx, crdCompatibilityRequirementName, oldObj, obj)
	case v1.Delete:
		// In reference to PR: https://github.com/kubernetes/kubernetes/pull/76346
		// OldObject contains the object being deleted
		if err := o.decoder.DecodeRaw(req.OldObject, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}

		warnings, err = o.ValidateDelete(ctx, crdCompatibilityRequirementName, obj)
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

func (o *ObjectValidator) ValidateCreate(ctx context.Context, crdCompatibilityRequirementName string, obj *unstructured.Unstructured) (warnings admission.Warnings, err error) {
	return admission.Warnings{fmt.Sprintf("This is a POC, handling request from CRDCompatibilityRequirement %s for unstructured object of kind %s: %s/%s", crdCompatibilityRequirementName, obj.GetObjectKind().GroupVersionKind(), obj.GetNamespace(), obj.GetName())}, nil
}

func (o *ObjectValidator) ValidateUpdate(ctx context.Context, crdCompatibilityRequirementName string, oldObj, obj *unstructured.Unstructured) (warnings admission.Warnings, err error) {
	return admission.Warnings{fmt.Sprintf("This is a POC, handling request from CRDCompatibilityRequirement %s for unstructured object of kind %s: %s/%s", crdCompatibilityRequirementName, obj.GetObjectKind().GroupVersionKind(), obj.GetNamespace(), obj.GetName())}, nil
}

func (o *ObjectValidator) ValidateDelete(ctx context.Context, crdCompatibilityRequirementName string, obj *unstructured.Unstructured) (warnings admission.Warnings, err error) {
	return admission.Warnings{fmt.Sprintf("This is a POC, handling request from CRDCompatibilityRequirement %s for unstructured object of kind %s: %s/%s", crdCompatibilityRequirementName, obj.GetObjectKind().GroupVersionKind(), obj.GetNamespace(), obj.GetName())}, nil
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

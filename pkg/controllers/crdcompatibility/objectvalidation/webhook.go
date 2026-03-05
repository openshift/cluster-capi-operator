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
	"sync"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionshelpers "k8s.io/apiextensions-apiserver/pkg/apihelpers"
	apiextensionsinternal "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apiextensions-apiserver/pkg/crdserverscheme"
	"k8s.io/apiextensions-apiserver/pkg/registry/customresource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	// webhookPrefix is the static path prefix of our object admission endpoint.
	// Requests will be sent to a sub-path with the next component of the path
	// as a compatibility requirement name.
	webhookPrefix = "/compatibility-requirement-object-admission/"
)

var (
	errObjectValidator       = errors.New("failed to create the object validator")
	errUnexpectedObjectType  = errors.New("unexpected object type")
	errUnknownCRDAdmitAction = errors.New("unknown CRD admit action")
)

var _ admission.Handler = &validator{}

type validationStrategyCacheKey struct {
	// The name of the CompatibilityRequirement.
	compatibilityRequirementName string
	// The API version of the schema we are caching.
	version string
}

type validationStrategyCacheValue struct {
	strategy rest.RESTCreateUpdateStrategy

	// The UID of the CompatibilityRequirement.
	uid types.UID

	// The generation of the CompatibilityRequirement.
	generation int64
}

// validator implements the admission.Handler to have a custom Handle function which is able to
// validate arbitrary objects against CompatibilityRequirements by leveraging unstructured.
type validator struct {
	client                client.Reader
	decoder               admission.Decoder
	universalDeserializer runtime.Decoder

	validationStrategyCacheLock sync.RWMutex
	validationStrategyCache     map[validationStrategyCacheKey]validationStrategyCacheValue
}

// NewValidator returns a partially initialized ObjectValidator.
func NewValidator() *validator {
	return &validator{
		// This decoder is only used to decode to unstructured and for CompatibilityRequirements.
		decoder:                 admission.NewDecoder(runtime.NewScheme()),
		validationStrategyCache: make(map[validationStrategyCacheKey]validationStrategyCacheValue),
	}
}

type controllerOption func(*builder.Builder) *builder.Builder

// SetupWithManager registers the objectValidator webhook with an manager.
func (v *validator) SetupWithManager(ctx context.Context, mgr ctrl.Manager, opts ...controllerOption) error {
	v.client = mgr.GetClient()

	serializer := serializer.NewCodecFactory(mgr.GetScheme())
	v.universalDeserializer = serializer.UniversalDeserializer()

	// Register a webhook on a path with a dynamic component for the compatibility requirement name.
	// we will extract this component into the context so that the handler can identify which compatibility
	// requirement the request was intended to validate against.
	mgr.GetWebhookServer().Register(webhookPrefix+"{CompatibilityRequirement}", &admission.Webhook{
		Handler:         v,
		WithContextFunc: compatibilityRequrementIntoContext,
	})

	return nil
}

// ValidateCreate validates the creation of an object.
func (v *validator) ValidateCreate(ctx context.Context, compatibilityRequirementName string, obj *unstructured.Unstructured) (admission.Warnings, error) {
	compatibilityRequirement := &apiextensionsv1alpha1.CompatibilityRequirement{}
	if err := v.client.Get(ctx, client.ObjectKey{Name: compatibilityRequirementName}, compatibilityRequirement); err != nil {
		return nil, fmt.Errorf("failed to get CompatibilityRequirement %q: %w", compatibilityRequirementName, err)
	}

	strategy, err := v.getValidationStrategy(compatibilityRequirement, obj.GroupVersionKind().Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get validation strategy: %w", err)
	}

	errs := strategy.Validate(ctx, obj)
	warnings := strategy.WarningsOnCreate(ctx, obj)

	switch compatibilityRequirement.Spec.ObjectSchemaValidation.Action {
	case apiextensionsv1alpha1.CRDAdmitActionWarn:
		warnings = append(warnings, util.SliceMap(errs, errorToString)...)

		return warnings, nil
	case apiextensionsv1alpha1.CRDAdmitActionDeny:
		if len(errs) > 0 {
			return warnings, apierrors.NewInvalid(obj.GroupVersionKind().GroupKind(), obj.GetName(), errs)
		}

		return warnings, nil
	default:
		// This should be impossible as validation on the action is enforced by openapi as an enum.
		return nil, fmt.Errorf("%w: %q", errUnknownCRDAdmitAction, compatibilityRequirement.Spec.ObjectSchemaValidation.Action)
	}
}

// ValidateUpdate validates the update of an object.
func (v *validator) ValidateUpdate(ctx context.Context, compatibilityRequirementName string, oldObj, obj *unstructured.Unstructured) (admission.Warnings, error) {
	compatibilityRequirement := &apiextensionsv1alpha1.CompatibilityRequirement{}
	if err := v.client.Get(ctx, client.ObjectKey{Name: compatibilityRequirementName}, compatibilityRequirement); err != nil {
		return nil, fmt.Errorf("failed to get CompatibilityRequirement %q: %w", compatibilityRequirementName, err)
	}

	strategy, err := v.getValidationStrategy(compatibilityRequirement, obj.GroupVersionKind().Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get validation strategy: %w", err)
	}

	errs := strategy.ValidateUpdate(ctx, obj, oldObj)
	warnings := strategy.WarningsOnUpdate(ctx, obj, oldObj)

	switch compatibilityRequirement.Spec.ObjectSchemaValidation.Action {
	case apiextensionsv1alpha1.CRDAdmitActionWarn:
		warnings = append(warnings, util.SliceMap(errs, errorToString)...)

		return warnings, nil
	case apiextensionsv1alpha1.CRDAdmitActionDeny:
		if len(errs) > 0 {
			return warnings, apierrors.NewInvalid(obj.GroupVersionKind().GroupKind(), obj.GetName(), errs)
		}

		return warnings, nil
	default:
		// This should be impossible as validation on the action is enforced by openapi as an enum.
		return nil, fmt.Errorf("%w: %q", errUnknownCRDAdmitAction, compatibilityRequirement.Spec.ObjectSchemaValidation.Action)
	}
}

// ValidateDelete validates the deletion of an object.
func (v *validator) ValidateDelete(ctx context.Context, compatibilityRequirementName string, obj *unstructured.Unstructured) (warnings admission.Warnings, err error) {
	return nil, nil
}

func (v *validator) getValidationStrategy(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, version string) (rest.RESTCreateUpdateStrategy, error) {
	cacheKey := getValidationStrategyCacheKey(compatibilityRequirement, version)

	strategy, ok := v.getValidationStrategyFromCache(compatibilityRequirement, cacheKey)
	if ok {
		return strategy, nil
	}

	v.validationStrategyCacheLock.Lock()
	defer v.validationStrategyCacheLock.Unlock()

	// Check the cache again under the write lock in case another thread populated the cache
	// while we were waiting for the write lock.
	strategyValue, ok := v.validationStrategyCache[cacheKey]
	if ok && isCacheEntryValid(compatibilityRequirement, strategyValue) {
		return strategyValue.strategy, nil
	}

	strategy, err := v.createVersionedStrategy(compatibilityRequirement, version)
	if err != nil {
		return nil, fmt.Errorf("failed to create validation strategy: %w", err)
	}

	v.storeValidationStrategyInCache(compatibilityRequirement, cacheKey, strategy)

	return strategy, nil
}

func getValidationStrategyCacheKey(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, version string) validationStrategyCacheKey {
	return validationStrategyCacheKey{
		compatibilityRequirementName: compatibilityRequirement.Name,
		version:                      version,
	}
}

func isCacheEntryValid(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, strategy validationStrategyCacheValue) bool {
	return compatibilityRequirement.Generation == strategy.generation && compatibilityRequirement.UID == strategy.uid
}

func (v *validator) getValidationStrategyFromCache(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, cacheKey validationStrategyCacheKey) (rest.RESTCreateUpdateStrategy, bool) {
	v.validationStrategyCacheLock.RLock()
	defer v.validationStrategyCacheLock.RUnlock()

	strategy, ok := v.validationStrategyCache[cacheKey]
	if !ok || !isCacheEntryValid(compatibilityRequirement, strategy) {
		return nil, false
	}

	return strategy.strategy, ok
}

func (v *validator) storeValidationStrategyInCache(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, cacheKey validationStrategyCacheKey, strategy rest.RESTCreateUpdateStrategy) {
	// No locking here as we take the lock when constructing the new strategy.
	v.validationStrategyCache[cacheKey] = validationStrategyCacheValue{
		strategy:   strategy,
		uid:        compatibilityRequirement.UID,
		generation: compatibilityRequirement.Generation,
	}
}

// https://github.com/kubernetes/kubernetes/blob/ebc1ccc491c944fa0633f147698e0dc02675051d/staging/src/k8s.io/apiextensions-apiserver/pkg/registry/customresource/strategy.go#L76
//
//nolint:cyclop,funlen // This is copied so ignore linting issues
func (v *validator) createVersionedStrategy(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, version string) (rest.RESTCreateUpdateStrategy, error) {
	// Extract the CRD so we can use the schema.
	// Use a universal deserializer as it correctly handles YAML and JSON decoding based on the expected key formatting for CRDs.
	// N.B. DO NOT switch this to a YAML library - they do not correctly handle the OpenAPIV3Schema casing within the CRD version schema.
	obj, _, err := v.universalDeserializer.Decode([]byte(compatibilityRequirement.Spec.CompatibilitySchema.CustomResourceDefinition.Data), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decode compatibility schema data for CompatibilityRequirement %q: %w", compatibilityRequirement.Name, err)
	}

	compatibilityCRD, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		return nil, fmt.Errorf("failed to decode compatibility schema data for CompatibilityRequirement %q: %w: got type %T", compatibilityRequirement.Name, errUnexpectedObjectType, obj)
	}

	// This should be validated by the CompatibilityRequirement admission webhook.
	// So we should never error here but adding for safety.
	if compatibilityCRD.APIVersion != "apiextensions.k8s.io/v1" || compatibilityCRD.Kind != "CustomResourceDefinition" {
		return nil, fmt.Errorf("%w: expected APIVersion to be apiextensions.k8s.io/v1 and Kind to be CustomResourceDefinition, got %s/%s", errObjectValidator, compatibilityCRD.APIVersion, compatibilityCRD.Kind)
	}

	typer := newUnstructuredObjectTyper(compatibilityCRD, version)

	// Adapted from k8s.io/apiextensions-apiserver/pkg/apiserver/customresource_handler.go getOrCreateServingInfoFor.
	validationSchema, err := apiextensionshelpers.GetSchemaForVersion(compatibilityCRD, version)
	if err != nil {
		return nil, fmt.Errorf("the server could not properly serve the CR schema: %w", err)
	} else if validationSchema == nil {
		return nil, fmt.Errorf("%w: validationSchema can't be nil", errObjectValidator)
	}

	internalValidationSchema := &apiextensionsinternal.CustomResourceValidation{}
	if err := apiextensionsv1.Convert_v1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(validationSchema, internalValidationSchema, nil); err != nil {
		return nil, fmt.Errorf("failed to convert CRD validation to internal version: %w", err)
	}

	internalSchemaProps := internalValidationSchema.OpenAPIV3Schema

	structuralSchema, err := structuralschema.NewStructural(internalValidationSchema.OpenAPIV3Schema)
	if err != nil {
		return nil, fmt.Errorf("failed to convert CRD validation to internal version: %w", err)
	}

	validator, _, err := apiextensionsvalidation.NewSchemaValidator(internalSchemaProps)
	if err != nil {
		return nil, fmt.Errorf("failed to create a SchemaValidator: %w", err)
	}

	subresources, err := apiextensionshelpers.GetSubresourcesForVersion(compatibilityCRD, version)
	if err != nil {
		return nil, fmt.Errorf("failed to get subresources for version %q: %w", version, err)
	}

	var statusSpec *apiextensionsinternal.CustomResourceSubresourceStatus

	var statusValidator apiextensionsvalidation.SchemaValidator

	if subresources != nil && subresources.Status != nil { //nolint:nestif // This is copied so ignore linting issues
		statusSpec = &apiextensionsinternal.CustomResourceSubresourceStatus{}
		if err := apiextensionsv1.Convert_v1_CustomResourceSubresourceStatus_To_apiextensions_CustomResourceSubresourceStatus(subresources.Status, statusSpec, nil); err != nil {
			return nil, fmt.Errorf("failed converting CRD status subresource to internal version: %w", err)
		}
		// for the status subresource, validate only against the status schema
		if internalValidationSchema != nil && internalValidationSchema.OpenAPIV3Schema != nil && internalValidationSchema.OpenAPIV3Schema.Properties != nil {
			if statusSchema, ok := internalValidationSchema.OpenAPIV3Schema.Properties["status"]; ok {
				statusValidator, _, err = apiextensionsvalidation.NewSchemaValidator(&statusSchema)
				if err != nil {
					return nil, fmt.Errorf("failed to create status schema validator: %w", err)
				}
			}
		}
	}

	var scaleSpec *apiextensionsinternal.CustomResourceSubresourceScale

	if subresources != nil && subresources.Scale != nil {
		scaleSpec = &apiextensionsinternal.CustomResourceSubresourceScale{}
		if err := apiextensionsv1.Convert_v1_CustomResourceSubresourceScale_To_apiextensions_CustomResourceSubresourceScale(subresources.Scale, scaleSpec, nil); err != nil {
			return nil, fmt.Errorf("failed converting CRD scale subresource to internal version: %w", err)
		}
	}

	strategy := customresource.NewStrategy(
		typer,
		compatibilityCRD.Spec.Scope == apiextensionsv1.NamespaceScoped,
		resourceGVK(compatibilityCRD, version),
		validator,
		statusValidator,
		structuralSchema,
		statusSpec,
		scaleSpec,
		nil,
	)

	return strategy, nil
}

// Adapted from https://github.com/kubernetes/apiextensions-apiserver/blob/f623d794ec40752b5939960ca2d816465bd46664/pkg/apiserver/customresource_handler.go#L1207
func newUnstructuredObjectTyper(crd *apiextensionsv1.CustomResourceDefinition, version string) apiserver.UnstructuredObjectTyper {
	// In addition to Unstructured objects (Custom Resources), we also may sometimes need to
	// decode unversioned Options objects, so we delegate to parameterScheme for such types.
	parameterScheme := runtime.NewScheme()
	parameterScheme.AddUnversionedTypes(schema.GroupVersion{Group: crd.Spec.Group, Version: version},
		&metav1.ListOptions{},
		&metav1.GetOptions{},
		&metav1.DeleteOptions{},
	)

	return apiserver.UnstructuredObjectTyper{
		Delegate:          parameterScheme,
		UnstructuredTyper: crdserverscheme.NewUnstructuredObjectTyper(),
	}
}

func resourceGVK(crd *apiextensionsv1.CustomResourceDefinition, version string) schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   crd.Spec.Group,
		Version: version,
		Kind:    crd.Spec.Names.Kind,
	}
}

// ValidatingWebhookConfigurationFor returns a ValidatingWebhookConfiguration for a CompatibilityRequirement and the CRD to which it is associated.
//
//nolint:funlen
func ValidatingWebhookConfigurationFor(obj *apiextensionsv1alpha1.CompatibilityRequirement, crd *apiextensionsv1.CustomResourceDefinition) *admissionregistrationv1.ValidatingWebhookConfiguration {
	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ValidatingWebhookConfiguration",
			APIVersion: "admissionregistration.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: obj.Name,
			Annotations: map[string]string{
				"service.beta.openshift.io/inject-cabundle": "true",
			},
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				AdmissionReviewVersions: []string{"v1"},
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service: &admissionregistrationv1.ServiceReference{
						Name:      "compatibility-requirements-controllers-webhook-service",
						Namespace: "openshift-compatibility-requirements-operator",
						Path:      ptr.To(fmt.Sprintf("%s%s", webhookPrefix, obj.Name)),
					},
				},
				SideEffects:   ptr.To(admissionregistrationv1.SideEffectClassNone),
				FailurePolicy: ptr.To(admissionregistrationv1.Fail),
				MatchPolicy:   ptr.To(admissionregistrationv1.Exact),
				Name:          "compatibilityrequirement.operator.openshift.io",
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{crd.Spec.Group},
							APIVersions: util.SliceMap(crd.Spec.Versions, func(version apiextensionsv1.CustomResourceDefinitionVersion) string { return version.Name }),
							Resources:   []string{crd.Spec.Names.Plural},
							Scope:       ptr.To(admissionregistrationv1.ScopeType(crd.Spec.Scope)),
						},
						Operations: []admissionregistrationv1.OperationType{"CREATE", "UPDATE"},
					},
				},
				MatchConditions:   obj.Spec.ObjectSchemaValidation.MatchConditions,
				NamespaceSelector: &obj.Spec.ObjectSchemaValidation.NamespaceSelector,
				ObjectSelector:    &obj.Spec.ObjectSchemaValidation.ObjectSelector,
			},
		},
	}

	var hasStatus, hasScale bool

	for _, version := range crd.Spec.Versions {
		if version.Subresources != nil {
			if version.Subresources.Status != nil && !hasStatus {
				hasStatus = true

				vwc.Webhooks[0].Rules[0].Rule.Resources = append(vwc.Webhooks[0].Rules[0].Rule.Resources, crd.Spec.Names.Plural+"/status")
			}

			if version.Subresources.Scale != nil && !hasScale {
				hasScale = true

				vwc.Webhooks[0].Rules[0].Rule.Resources = append(vwc.Webhooks[0].Rules[0].Rule.Resources, crd.Spec.Names.Plural+"/scale")
			}
		}
	}

	return vwc
}

func errorToString(err *field.Error) string {
	return err.Error()
}

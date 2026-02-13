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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/registry/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/yaml"
)

const (
	// WebhookPrefix is the static path prefix of our object admission endpoint.
	// Requests will be sent to a sub-path with the next component of the path
	// as a compatibility requirement name.
	WebhookPrefix = "/compatibility-requirement-object-admission/"
)

var (
	errObjectValidator = errors.New("failed to create the object validator")
)

var _ admission.Handler = &validator{}

type validationStrategyCacheKey struct {
	uid                          types.UID
	compatibilityRequirementName string
	version                      string
	generation                   int64
}

// validator implements the admission.Handler to have a custom Handle function which is able to
// validate arbitrary objects against CompatibilityRequirements by leveraging unstructured.
type validator struct {
	client  client.Reader
	decoder admission.Decoder

	validationStrategyCacheLock sync.RWMutex
	validationStrategyCache     map[validationStrategyCacheKey]rest.RESTCreateUpdateStrategy
}

// NewValidator returns a partially initialized ObjectValidator.
func NewValidator() *validator {
	return &validator{
		// This decoder is only used to decode to unstructured and for CompatibilityRequirements.
		decoder:                 admission.NewDecoder(runtime.NewScheme()),
		validationStrategyCache: make(map[validationStrategyCacheKey]rest.RESTCreateUpdateStrategy),
	}
}

type controllerOption func(*builder.Builder) *builder.Builder

// SetupWithManager registers the objectValidator webhook with an manager.
func (v *validator) SetupWithManager(ctx context.Context, mgr ctrl.Manager, opts ...controllerOption) error {
	v.client = mgr.GetClient()

	// Register a webhook on a path with a dynamic component for the compatibility requirement name.
	// we will extract this component into the context so that the handler can identify which compatibility
	// requirement the request was intended to validate against.
	mgr.GetWebhookServer().Register(WebhookPrefix+"{CompatibilityRequirement}", &admission.Webhook{
		Handler:         v,
		WithContextFunc: compatibilityRequrementIntoContext,
	})

	return nil
}

// ValidateCreate validates the creation of an object.
func (v *validator) ValidateCreate(ctx context.Context, compatibilityRequirementName string, obj *unstructured.Unstructured) (admission.Warnings, error) {
	strategy, err := v.getValidationStrategy(ctx, compatibilityRequirementName, obj.GroupVersionKind().Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get validation strategy: %w", err)
	}

	errs := strategy.Validate(ctx, obj)
	warnings := strategy.WarningsOnCreate(ctx, obj)

	if len(errs) > 0 {
		return warnings, apierrors.NewInvalid(obj.GroupVersionKind().GroupKind(), obj.GetName(), errs)
	}

	return warnings, nil

}

// ValidateUpdate validates the update of an object.
func (v *validator) ValidateUpdate(ctx context.Context, compatibilityRequirementName string, oldObj, obj *unstructured.Unstructured) (admission.Warnings, error) {
	strategy, err := v.getValidationStrategy(ctx, compatibilityRequirementName, obj.GroupVersionKind().Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get validation strategy: %w", err)
	}

	errs := strategy.ValidateUpdate(ctx, obj, oldObj)
	warnings := strategy.WarningsOnUpdate(ctx, obj, oldObj)

	if len(errs) > 0 {
		return warnings, apierrors.NewInvalid(obj.GroupVersionKind().GroupKind(), obj.GetName(), errs)
	}

	return warnings, nil
}

// ValidateDelete validates the deletion of an object.
func (v *validator) ValidateDelete(ctx context.Context, compatibilityRequirementName string, obj *unstructured.Unstructured) (warnings admission.Warnings, err error) {
	return nil, nil
}

func (v *validator) getValidationStrategy(ctx context.Context, compatibilityRequirementName string, version string) (rest.RESTCreateUpdateStrategy, error) {
	compatibilityRequirement := &apiextensionsv1alpha1.CompatibilityRequirement{}
	if err := v.client.Get(ctx, client.ObjectKey{Name: compatibilityRequirementName}, compatibilityRequirement); err != nil {
		return nil, fmt.Errorf("failed to get CompatibilityRequirement %q: %w", compatibilityRequirementName, err)
	}

	strategy, ok := v.getValidationStrategyFromCache(compatibilityRequirement, version)
	if ok {
		return strategy, nil
	}

	v.validationStrategyCacheLock.Lock()
	defer v.validationStrategyCacheLock.Unlock()

	strategy, err := v.createVersionedStrategy(ctx, compatibilityRequirementName, version)
	if err != nil {
		return nil, fmt.Errorf("failed to get validation strategy: %w", err)
	}

	v.storeValidationStrategyInCache(compatibilityRequirement, version, strategy)
	v.pruneOldValidationStrategies(compatibilityRequirement, version)

	return strategy, nil
}

func getValidationStrategyCacheKey(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, version string) validationStrategyCacheKey {
	return validationStrategyCacheKey{
		uid:                          compatibilityRequirement.UID,
		compatibilityRequirementName: compatibilityRequirement.Name,
		version:                      version,
		generation:                   compatibilityRequirement.Generation,
	}
}

func (v *validator) getValidationStrategyFromCache(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, version string) (rest.RESTCreateUpdateStrategy, bool) {
	v.validationStrategyCacheLock.RLock()
	defer v.validationStrategyCacheLock.RUnlock()

	strategy, ok := v.validationStrategyCache[getValidationStrategyCacheKey(compatibilityRequirement, version)]
	return strategy, ok
}

func (v *validator) storeValidationStrategyInCache(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, version string, strategy rest.RESTCreateUpdateStrategy) {
	// No locking here as we take the lock when constructing the new strategy.
	v.validationStrategyCache[getValidationStrategyCacheKey(compatibilityRequirement, version)] = strategy
}

func (v *validator) pruneOldValidationStrategies(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, version string) {
	currentKey := getValidationStrategyCacheKey(compatibilityRequirement, version)

	// Delete all strategies that are for the same compatibility requirement but have an older generation.
	for key := range v.validationStrategyCache {
		if key.uid == currentKey.uid &&
			key.compatibilityRequirementName == currentKey.compatibilityRequirementName &&
			key.version == currentKey.version &&
			key.generation < currentKey.generation {
			delete(v.validationStrategyCache, key)
		}
	}
}

// https://github.com/kubernetes/kubernetes/blob/ebc1ccc491c944fa0633f147698e0dc02675051d/staging/src/k8s.io/apiextensions-apiserver/pkg/registry/customresource/strategy.go#L76
func (v *validator) createVersionedStrategy(ctx context.Context, compatibilityRequirementName string, version string) (rest.RESTCreateUpdateStrategy, error) {
	// Get the CompatibilityRequirement
	compatibilityRequirement := &apiextensionsv1alpha1.CompatibilityRequirement{}
	if err := v.client.Get(ctx, client.ObjectKey{Name: compatibilityRequirementName}, compatibilityRequirement); err != nil {
		return nil, fmt.Errorf("failed to get CompatibilityRequirement %q: %w", compatibilityRequirementName, err)
	}

	// Extract the CRD so we can use the schema.
	compatibilityCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal([]byte(compatibilityRequirement.Spec.CompatibilitySchema.CustomResourceDefinition.Data), &compatibilityCRD); err != nil {
		return nil, fmt.Errorf("failed to parse compatibility schema data for CompatibilityRequirement %q: %w", compatibilityRequirement.Name, err)
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

	if subresources != nil && subresources.Status != nil {
		statusSpec = &apiextensionsinternal.CustomResourceSubresourceStatus{}
		if err := apiextensionsv1.Convert_v1_CustomResourceSubresourceStatus_To_apiextensions_CustomResourceSubresourceStatus(subresources.Status, statusSpec, nil); err != nil {
			return nil, fmt.Errorf("failed converting CRD status subresource to internal version: %v", err)
		}
		// for the status subresource, validate only against the status schema
		if internalValidationSchema != nil && internalValidationSchema.OpenAPIV3Schema != nil && internalValidationSchema.OpenAPIV3Schema.Properties != nil {
			if statusSchema, ok := internalValidationSchema.OpenAPIV3Schema.Properties["status"]; ok {
				statusValidator, _, err = apiextensionsvalidation.NewSchemaValidator(&statusSchema)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	var scaleSpec *apiextensionsinternal.CustomResourceSubresourceScale

	if subresources != nil && subresources.Scale != nil {
		scaleSpec = &apiextensionsinternal.CustomResourceSubresourceScale{}
		if err := apiextensionsv1.Convert_v1_CustomResourceSubresourceScale_To_apiextensions_CustomResourceSubresourceScale(subresources.Scale, scaleSpec, nil); err != nil {
			return nil, fmt.Errorf("failed converting CRD status subresource to internal version: %v", err)
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
	if err != nil {
		return nil, fmt.Errorf("failed to create custom resource strategy: %w", err)
	}

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

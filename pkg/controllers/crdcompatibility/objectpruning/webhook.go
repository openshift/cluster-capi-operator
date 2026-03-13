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
	"errors"
	"fmt"
	"sync"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionshelpers "k8s.io/apiextensions-apiserver/pkg/apihelpers"
	apiextensionsinternal "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	errObjectValidator      = errors.New("failed to create the object schema")
	errUnexpectedObjectType = errors.New("unexpected object type")
)

const (
	// webhookPrefix is the static path prefix of our object admission endpoint.
	// Requests will be sent to a sub-path with the next component of the path
	// as a compatibility requirement name.
	webhookPrefix = "/compatibility-requirement-object-mutation/"
)

type structuralSchemaCacheKey struct {
	// The name of the CompatibilityRequirement.
	compatibilityRequirementName string
	// The API version of the schema we are caching.
	version string
}

type structuralSchemaCacheValue struct {
	schema *structuralschema.Structural

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

	structuralSchemaCacheLock sync.RWMutex
	structuralSchemaCache     map[structuralSchemaCacheKey]structuralSchemaCacheValue
}

// NewValidator returns a partially initialized ObjectValidator.
func NewValidator() *validator {
	return &validator{
		// This decoder is only used to decode to unstructured and for CompatibilityRequirements.
		decoder:               admission.NewDecoder(runtime.NewScheme()),
		structuralSchemaCache: make(map[structuralSchemaCacheKey]structuralSchemaCacheValue),
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

// handleObjectPruning handles the pruning of an object.
func (v *validator) handleObjectPruning(ctx context.Context, compatibilityRequirementName string, obj *unstructured.Unstructured) error {
	schema, err := v.getStructuralSchema(ctx, compatibilityRequirementName, obj.GroupVersionKind().Version)
	if err != nil {
		return fmt.Errorf("failed to get schema for CompatibilityRequirement %q: %w", compatibilityRequirementName, err)
	}

	pruning.Prune(obj.Object, schema, true)

	return nil
}

func (v *validator) getStructuralSchema(ctx context.Context, compatibilityRequirementName string, version string) (*structuralschema.Structural, error) {
	compatibilityRequirement := &apiextensionsv1alpha1.CompatibilityRequirement{}
	if err := v.client.Get(ctx, client.ObjectKey{Name: compatibilityRequirementName}, compatibilityRequirement); err != nil {
		return nil, fmt.Errorf("failed to get CompatibilityRequirement %q: %w", compatibilityRequirementName, err)
	}

	cacheKey := getStructuralSchemaCacheKey(compatibilityRequirement, version)

	schema, ok := v.getStructuralSchemaFromCache(compatibilityRequirement, cacheKey)
	if ok {
		return schema, nil
	}

	v.structuralSchemaCacheLock.Lock()
	defer v.structuralSchemaCacheLock.Unlock()

	// Check the cache again under the write lock in case another thread populated the cache
	// while we were waiting for the write lock.
	schemaValue, ok := v.structuralSchemaCache[cacheKey]
	if ok && isCacheEntryValid(compatibilityRequirement, schemaValue) {
		return schemaValue.schema, nil
	}

	schema, err := v.getCompatibilityRequirementStructuralSchema(compatibilityRequirement, version)
	if err != nil {
		return nil, fmt.Errorf("failed to get structural schema: %w", err)
	}

	v.storeStructuralSchemaInCache(compatibilityRequirement, cacheKey, schema)

	return schema, nil
}

func getStructuralSchemaCacheKey(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, version string) structuralSchemaCacheKey {
	return structuralSchemaCacheKey{
		compatibilityRequirementName: compatibilityRequirement.Name,
		version:                      version,
	}
}

func isCacheEntryValid(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, schema structuralSchemaCacheValue) bool {
	return compatibilityRequirement.Generation == schema.generation && compatibilityRequirement.UID == schema.uid
}

func (v *validator) getStructuralSchemaFromCache(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, cacheKey structuralSchemaCacheKey) (*structuralschema.Structural, bool) {
	v.structuralSchemaCacheLock.RLock()
	defer v.structuralSchemaCacheLock.RUnlock()

	schema, ok := v.structuralSchemaCache[cacheKey]
	if !ok || !isCacheEntryValid(compatibilityRequirement, schema) {
		return nil, false
	}

	return schema.schema, true
}

func (v *validator) storeStructuralSchemaInCache(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, cacheKey structuralSchemaCacheKey, schema *structuralschema.Structural) {
	// No locking here as we take the lock when constructing the new schema.
	v.structuralSchemaCache[cacheKey] = structuralSchemaCacheValue{
		schema:     schema,
		uid:        compatibilityRequirement.UID,
		generation: compatibilityRequirement.Generation,
	}
}

func (v *validator) getCompatibilityRequirementStructuralSchema(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement, version string) (*structuralschema.Structural, error) {
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

	structuralSchema, err := structuralschema.NewStructural(internalValidationSchema.OpenAPIV3Schema)
	if err != nil {
		return nil, fmt.Errorf("failed to convert CRD validation to internal version: %w", err)
	}

	return structuralSchema, nil
}

// MutatingWebhookConfigurationFor returns a MutatingWebhookConfiguration for a CompatibilityRequirement and the CRD to which it is associated.
//
//nolint:funlen
func MutatingWebhookConfigurationFor(obj *apiextensionsv1alpha1.CompatibilityRequirement, crd *apiextensionsv1.CustomResourceDefinition) *admissionregistrationv1.MutatingWebhookConfiguration {
	vwc := &admissionregistrationv1.MutatingWebhookConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "MutatingWebhookConfiguration",
			APIVersion: "admissionregistration.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: obj.Name,
			Annotations: map[string]string{
				"service.beta.openshift.io/inject-cabundle": "true",
			},
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
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

				vwc.Webhooks[0].Rules[0].Resources = append(vwc.Webhooks[0].Rules[0].Resources, crd.Spec.Names.Plural+"/status")
			}

			if version.Subresources.Scale != nil && !hasScale {
				hasScale = true

				vwc.Webhooks[0].Rules[0].Resources = append(vwc.Webhooks[0].Rules[0].Resources, crd.Spec.Names.Plural+"/scale")
			}
		}
	}

	return vwc
}

/*
Copyright 2025 Red Hat, Inc.

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

package crdcompatibility

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/objectpruning"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/objectvalidation"
	"github.com/openshift/cluster-capi-operator/pkg/crdchecker"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	terminalErrorReasonConfigurationError string = "ConfigurationError"
)

type reconcileState struct {
	*CompatibilityRequirementReconciler

	compatibilityCRD *apiextensionsv1.CustomResourceDefinition
	currentCRD       *apiextensionsv1.CustomResourceDefinition
	requirement      *apiextensionsv1alpha1.CompatibilityRequirement

	compatibilityErrors   []string
	compatibilityWarnings []string
}

// Reconcile handles the reconciliation of CompatibilityRequirement resources.
func (r *CompatibilityRequirementReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// Fetch the CompatibilityRequirement instance
	obj := &apiextensionsv1alpha1.CompatibilityRequirement{}
	if err := r.client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(4).Info("Observed CompatibilityRequirement deleted")
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get CompatibilityRequirement: %w", err)
	}

	state := &reconcileState{CompatibilityRequirementReconciler: r, requirement: obj}

	result, reconcileErr := state.reconcile(ctx, obj)
	err := state.writeStatus(ctx, obj, reconcileErr)

	if errors.Join(reconcileErr, err) != nil {
		return ctrl.Result{}, errors.Join(reconcileErr, err)
	}

	return result, nil
}

func (r *reconcileState) reconcile(ctx context.Context, compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement) (ctrl.Result, error) {
	if !compatibilityRequirement.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, compatibilityRequirement)
	}

	return r.reconcileCreateOrUpdate(ctx, compatibilityRequirement)
}

func (r *reconcileState) parseCompatibilityCRD(compatibilityRequirement *apiextensionsv1alpha1.CompatibilityRequirement) error {
	// Parse the CRD in compatibilityCRD into a CRD object
	compatibilityCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal([]byte(compatibilityRequirement.Spec.CompatibilitySchema.CustomResourceDefinition.Data), compatibilityCRD); err != nil {
		return util.TerminalWithReasonError(fmt.Errorf("failed to parse compatibilityCRD: %w", err), terminalErrorReasonConfigurationError) //nolint:wrapcheck
	}

	r.compatibilityCRD = compatibilityCRD

	return nil
}

func (r *reconcileState) fetchCurrentCRD(ctx context.Context, log logr.Logger) error {
	if r.compatibilityCRD == nil {
		return nil
	}

	crdName := r.compatibilityCRD.GetName()
	if crdName == "" {
		return nil
	}

	currentCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: crdName}, currentCRD); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("CRD not found", "crdRef", crdName)
			return nil
		} else {
			return fmt.Errorf("failed to fetch CRD %s: %w", crdName, err)
		}
	}

	r.currentCRD = currentCRD

	return nil
}

func (r *reconcileState) checkCompatibilityRequirement() error {
	if r.compatibilityCRD == nil || r.currentCRD == nil {
		return nil
	}

	var err error
	r.compatibilityErrors, r.compatibilityWarnings, err = crdchecker.CheckCompatibilityRequirement(r.compatibilityCRD, r.currentCRD)

	if err != nil {
		return fmt.Errorf("failed to check CRD compatibility: %w", err)
	}

	return nil
}

func (r *reconcileState) reconcileCreateOrUpdate(ctx context.Context, obj *apiextensionsv1alpha1.CompatibilityRequirement) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	logger.Info("Reconciling CompatibilityRequirement")

	// Set the finalizer before reconciling
	if !slices.Contains(obj.Finalizers, finalizerName) {
		if err := setFinalizer(ctx, r.client, obj); err != nil {
			return ctrl.Result{}, err
		}
	}

	err := errors.Join(
		r.parseCompatibilityCRD(obj),
		r.fetchCurrentCRD(ctx, logger),
		r.checkCompatibilityRequirement(),
		r.ensureObjectValidationWebhook(ctx, obj),
		r.ensureObjectPruningWebhook(ctx, obj),
	)

	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *reconcileState) reconcileDelete(ctx context.Context, obj *apiextensionsv1alpha1.CompatibilityRequirement) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	logger.Info("Reconciling CompatibilityRequirement deletion")

	err := errors.Join(
		clearFinalizer(ctx, r.client, obj),
		r.removeObjectValidationWebhook(ctx, obj),
		r.removeObjectPruningWebhook(ctx, obj),
	)

	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *reconcileState) ensureObjectValidationWebhook(ctx context.Context, obj *apiextensionsv1alpha1.CompatibilityRequirement) error {
	if isObjectValidationWebhookEnabled(obj) {
		return nil
	}

	webhookConfig := validatingWebhookConfigurationFor(obj, r.compatibilityCRD)
	if err := r.client.Get(ctx, types.NamespacedName{Name: webhookConfig.Name}, webhookConfig); err != nil {
		if apierrors.IsNotFound(err) {
			return r.client.Create(ctx, webhookConfig)
		}

		return err
	}

	return r.client.Update(ctx, webhookConfig)
}

func (r *reconcileState) removeObjectValidationWebhook(ctx context.Context, obj *apiextensionsv1alpha1.CompatibilityRequirement) error {
	webhookConfig := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: obj.Name,
		},
	}

	if err := r.client.Get(ctx, types.NamespacedName{Name: webhookConfig.Name}, webhookConfig); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return r.client.Delete(ctx, webhookConfig)
}

func (r *reconcileState) ensureObjectPruningWebhook(ctx context.Context, obj *apiextensionsv1alpha1.CompatibilityRequirement) error {
	if isObjectValidationWebhookEnabled(obj) {
		return nil
	}

	webhookConfig := mutatingWebhookConfigurationFor(obj, r.compatibilityCRD)
	if err := r.client.Get(ctx, types.NamespacedName{Name: webhookConfig.Name}, webhookConfig); err != nil {
		if apierrors.IsNotFound(err) {
			return r.client.Create(ctx, webhookConfig)
		}

		return err
	}

	return r.client.Update(ctx, webhookConfig)
}

func (r *reconcileState) removeObjectPruningWebhook(ctx context.Context, obj *apiextensionsv1alpha1.CompatibilityRequirement) error {
	webhookConfig := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: obj.Name,
		},
	}

	if err := r.client.Get(ctx, types.NamespacedName{Name: webhookConfig.Name}, webhookConfig); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return r.client.Delete(ctx, webhookConfig)
}

func isObjectValidationWebhookEnabled(obj *apiextensionsv1alpha1.CompatibilityRequirement) bool {
	osv := obj.Spec.ObjectSchemaValidation
	return osv.Action == "" && osv.MatchConditions == nil && labelSelectorIsEmpty(osv.NamespaceSelector) && labelSelectorIsEmpty(osv.ObjectSelector)
}

func labelSelectorIsEmpty(ls metav1.LabelSelector) bool {
	return len(ls.MatchLabels) == 0 && len(ls.MatchExpressions) == 0
}

func validatingWebhookConfigurationFor(obj *apiextensionsv1alpha1.CompatibilityRequirement, crd *apiextensionsv1.CustomResourceDefinition) *admissionregistrationv1.ValidatingWebhookConfiguration {
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
						Name:      "compatibility-requirements-controllers-validation-webhook-service",
						Namespace: "openshift-compatibility-requirements-operator",
						Path:      ptr.To(fmt.Sprintf("%s%s", objectvalidation.WebhookPrefix, obj.Name)),
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

func mutatingWebhookConfigurationFor(obj *apiextensionsv1alpha1.CompatibilityRequirement, crd *apiextensionsv1.CustomResourceDefinition) *admissionregistrationv1.MutatingWebhookConfiguration {
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
						Name:      "compatibility-requirements-controllers-validation-webhook-service",
						Namespace: "openshift-compatibility-requirements-operator",
						Path:      ptr.To(fmt.Sprintf("%s%s", objectpruning.WebhookPrefix, obj.Name)),
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

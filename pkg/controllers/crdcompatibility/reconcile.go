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
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/clock"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/objectpruning"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/objectvalidation"
	"github.com/openshift/cluster-capi-operator/pkg/crdchecker"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
)

const (
	terminalErrorReasonConfigurationError string = "ConfigurationError"
)

var (
	errWebhookConfigNotControlledByCompatibilityRequirement = errors.New("webhook config is not controlled by CompatibilityRequirement")
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
		r.removeObjectValidationWebhook(ctx, obj),
		r.removeObjectPruningWebhook(ctx, obj),
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Finalizer must be cleared after the VWC has successfully been removed.
	if err := clearFinalizer(ctx, r.client, obj); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

//nolint:dupl // This and the ensureObjectPruningWebhook function look very similar, but are populating different objects.
func (r *reconcileState) ensureObjectValidationWebhook(ctx context.Context, obj *apiextensionsv1alpha1.CompatibilityRequirement) error {
	if !isObjectValidationWebhookEnabled(obj) || r.compatibilityCRD == nil {
		// Ensure that the webhook is removed in case we previously created it.
		return r.removeObjectValidationWebhook(ctx, obj)
	}

	webhookConfig := objectvalidation.ValidatingWebhookConfigurationFor(obj, r.compatibilityCRD)

	existingWebhookConfig := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: webhookConfig.Name}, existingWebhookConfig); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get ValidatingWebhookConfiguration %s: %w", webhookConfig.Name, err)
	} else if err == nil && !metav1.IsControlledBy(existingWebhookConfig, obj) {
		return fmt.Errorf("%w: %s", errWebhookConfigNotControlledByCompatibilityRequirement, webhookConfig.Name)
	}

	if _, _, err := resourceapply.ApplyValidatingWebhookConfigurationImproved(
		ctx,
		r.kubeClient.AdmissionregistrationV1(),
		events.NewKubeRecorder(r.kubeClient.CoreV1().Events("default"), "crd-compatibility", &corev1.ObjectReference{
			Kind: "CompatibilityRequirement",
			Name: obj.Name,
		}, clock.RealClock{}),
		webhookConfig,
		r.resourceCache,
	); err != nil {
		return fmt.Errorf("failed to apply ValidatingWebhookConfiguration %s: %w", webhookConfig.Name, err)
	}

	return nil
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

		return fmt.Errorf("failed to get ValidatingWebhookConfiguration %s: %w", webhookConfig.Name, err)
	}

	if err := r.client.Delete(ctx, webhookConfig); err != nil {
		return fmt.Errorf("failed to delete ValidatingWebhookConfiguration %s: %w", webhookConfig.Name, err)
	}

	return nil
}

//nolint:dupl // This and the ensureObjectValidationWebhook function look very similar, but are populating different objects.
func (r *reconcileState) ensureObjectPruningWebhook(ctx context.Context, obj *apiextensionsv1alpha1.CompatibilityRequirement) error {
	if !isObjectValidationWebhookEnabled(obj) || r.compatibilityCRD == nil {
		// Ensure that the webhook is removed in case we previously created it.
		return r.removeObjectPruningWebhook(ctx, obj)
	}

	webhookConfig := objectpruning.MutatingWebhookConfigurationFor(obj, r.compatibilityCRD)

	existingWebhookConfig := &admissionregistrationv1.MutatingWebhookConfiguration{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: webhookConfig.Name}, existingWebhookConfig); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get MutatingWebhookConfiguration %s: %w", webhookConfig.Name, err)
	} else if err == nil && !metav1.IsControlledBy(existingWebhookConfig, obj) {
		return fmt.Errorf("%w: %s", errWebhookConfigNotControlledByCompatibilityRequirement, webhookConfig.Name)
	}

	// If we don't own the webhook config, we should not be overwriting it.
	if _, _, err := resourceapply.ApplyMutatingWebhookConfigurationImproved(
		ctx,
		r.kubeClient.AdmissionregistrationV1(),
		events.NewKubeRecorder(r.kubeClient.CoreV1().Events("default"), "crd-compatibility", &corev1.ObjectReference{
			Kind: "CompatibilityRequirement",
			Name: obj.Name,
		}, clock.RealClock{}),
		webhookConfig,
		r.resourceCache,
	); err != nil {
		return fmt.Errorf("failed to apply MutatingWebhookConfiguration %s: %w", webhookConfig.Name, err)
	}

	return nil
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

		return fmt.Errorf("failed to get MutatingWebhookConfiguration %s: %w", webhookConfig.Name, err)
	}

	// If we don't own the webhook config, we should not be deleting it.
	if !metav1.IsControlledBy(webhookConfig, obj) {
		return nil
	}

	if err := r.client.Delete(ctx, webhookConfig); err != nil {
		return fmt.Errorf("failed to delete MutatingWebhookConfiguration %s: %w", webhookConfig.Name, err)
	}

	return nil
}

func isObjectValidationWebhookEnabled(obj *apiextensionsv1alpha1.CompatibilityRequirement) bool {
	osv := obj.Spec.ObjectSchemaValidation
	return osv.Action != "" || osv.MatchConditions != nil || !labelSelectorIsEmpty(osv.NamespaceSelector) || !labelSelectorIsEmpty(osv.ObjectSelector)
}

func labelSelectorIsEmpty(ls metav1.LabelSelector) bool {
	return len(ls.MatchLabels) == 0 && len(ls.MatchExpressions) == 0
}

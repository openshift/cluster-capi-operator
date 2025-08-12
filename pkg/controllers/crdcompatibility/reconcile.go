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
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/crdchecker"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"
)

const (
	noRequeueErrorReasonConfigurationError string = "ConfigurationError"
)

type reconcileState struct {
	*CRDCompatibilityReconciler

	compatibilityCRD *apiextensionsv1.CustomResourceDefinition
	currentCRD       *apiextensionsv1.CustomResourceDefinition

	compatibilityErrors   []string
	compatibilityWarnings []string
}

// Reconcile handles the reconciliation of CRDCompatibilityRequirement resources
func (r *CRDCompatibilityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the CRDCompatibilityRequirement instance
	obj := &operatorv1alpha1.CRDCompatibilityRequirement{}
	if err := r.client.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(4).Info("Observed CRDCompatibilityRequirement deleted")
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, fmt.Errorf("failed to get CRDCompatibilityRequirement: %w", err)
	}

	state := &reconcileState{CRDCompatibilityReconciler: r}

	result, reconcileErr := state.reconcile(ctx, obj)
	if err := state.writeStatus(ctx, obj, reconcileErr); err != nil {
		return ctrl.Result{}, err
	}

	if reconcileErr != nil {
		return ctrl.Result{}, util.LogNoRequeueError(reconcileErr, logger)
	}

	return result, nil
}

func (r *reconcileState) reconcile(ctx context.Context, crdCompatibilityRequirement *operatorv1alpha1.CRDCompatibilityRequirement) (ctrl.Result, error) {
	if !crdCompatibilityRequirement.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, crdCompatibilityRequirement)
	}

	return r.reconcileCreateOrUpdate(ctx, crdCompatibilityRequirement)
}

func (r *reconcileState) parseCompatibilityCRD(crdCompatibilityRequirement *operatorv1alpha1.CRDCompatibilityRequirement) error {
	// Parse the CRD in compatibilityCRD into a CRD object
	compatibilityCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal([]byte(crdCompatibilityRequirement.Spec.CompatibilityCRD), compatibilityCRD); err != nil {
		return util.NoRequeueError(fmt.Errorf("failed to parse compatibilityCRD for %s: %w", crdCompatibilityRequirement.Spec.CRDRef, err), noRequeueErrorReasonConfigurationError)
	}
	r.compatibilityCRD = compatibilityCRD

	return nil
}

func (r *reconcileState) fetchCurrentCRD(ctx context.Context, log logr.Logger, crdCompatibilityRequirement *operatorv1alpha1.CRDCompatibilityRequirement) error {
	currentCRD := &apiextensionsv1.CustomResourceDefinition{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: crdCompatibilityRequirement.Spec.CRDRef}, currentCRD); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("CRD not found", "crdRef", crdCompatibilityRequirement.Spec.CRDRef)
			return nil
		} else {
			return fmt.Errorf("failed to fetch CRD %s: %w", crdCompatibilityRequirement.Spec.CRDRef, err)
		}
	}
	r.currentCRD = currentCRD

	return nil
}

func (r *reconcileState) checkCRDCompatibility() error {
	if r.compatibilityCRD == nil || r.currentCRD == nil {
		return nil
	}

	errors, warnings, err := crdchecker.CheckCRDCompatibility(r.compatibilityCRD, r.currentCRD)
	r.compatibilityErrors = errors
	r.compatibilityWarnings = warnings

	return err
}

func (r *reconcileState) reconcileCreateOrUpdate(ctx context.Context, crdCompatibilityRequirement *operatorv1alpha1.CRDCompatibilityRequirement) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Reconciling CRDCompatibilityRequirement")

	// Set the finalizer before reconciling
	if !slices.Contains(crdCompatibilityRequirement.Finalizers, finalizerName) {
		if err := setFinalizer(ctx, r.client, crdCompatibilityRequirement); err != nil {
			return ctrl.Result{}, err
		}
	}

	err := errors.Join(
		r.parseCompatibilityCRD(crdCompatibilityRequirement),
		r.fetchCurrentCRD(ctx, logger, crdCompatibilityRequirement),
		r.checkCRDCompatibility(),
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	if r.compatibilityCRD == nil {
		// Should have been handled by API validation
		return ctrl.Result{}, util.NoRequeueError(fmt.Errorf("compatibilityCRD was not provided"), noRequeueErrorReasonConfigurationError)
	}

	// Add the requirement to the webhook validator
	r.validator.setRequirement(crdCompatibilityRequirement.Spec.CRDRef, r.compatibilityCRD)

	// TODO: Implement reconciliation logic
	// - Validate CRDCompatibilityRequirement spec
	// - Check if required CRDs exist
	// - Update status based on compatibility requirements
	// - Handle any errors and update conditions

	return ctrl.Result{}, nil
}

func (r *reconcileState) reconcileDelete(ctx context.Context, obj *operatorv1alpha1.CRDCompatibilityRequirement) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Reconciling CRDCompatibilityRequirement deletion")

	// Remove the requirement from the webhook validator
	r.validator.setRequirement(obj.Spec.CRDRef, nil)

	if err := clearFinalizer(ctx, r.client, obj); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

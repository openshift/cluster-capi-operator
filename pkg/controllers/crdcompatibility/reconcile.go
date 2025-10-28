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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	apiextensionsv1alpha1 "github.com/openshift/api/apiextensions/v1alpha1"
	"github.com/openshift/cluster-capi-operator/pkg/crdchecker"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	noRequeueErrorReasonConfigurationError string = "ConfigurationError"
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
	if err := state.writeStatus(ctx, obj, reconcileErr); err != nil {
		return ctrl.Result{}, err
	}

	if reconcileErr != nil {
		return ctrl.Result{}, util.LogNoRequeueError(reconcileErr, logger) //nolint:wrapcheck
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
		return util.NoRequeueError(fmt.Errorf("failed to parse compatibilityCRD: %w", err), noRequeueErrorReasonConfigurationError) //nolint:wrapcheck
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
	)

	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *reconcileState) reconcileDelete(ctx context.Context, obj *apiextensionsv1alpha1.CompatibilityRequirement) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	logger.Info("Reconciling CompatibilityRequirement deletion")

	if err := clearFinalizer(ctx, r.client, obj); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

/*
Copyright 2026 Red Hat, Inc.

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

package migrationcommon

import (
	"context"
	"errors"
	"fmt"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/synccommon"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	errUnsupportedCurrentAuthority       = errors.New("unsupported current authoritativeAPI")
	errUnsupportedStableAuthority        = errors.New("unsupported stable authority")
	errUnrecognizedPausedConditionStatus = errors.New("unrecognized paused condition status")
)

func unsupportedCurrentAuthorityError(authority mapiv1beta1.MachineAuthority) error {
	return fmt.Errorf("%w: %q", errUnsupportedCurrentAuthority, authority)
}

func unsupportedStableAuthorityError(authority mapiv1beta1.MachineAuthority) error {
	return fmt.Errorf("%w: %q", errUnsupportedStableAuthority, authority)
}

func unrecognizedPausedConditionStatusError(obj client.Object, status corev1.ConditionStatus) error {
	return fmt.Errorf("%w for %s/%s: %q", errUnrecognizedPausedConditionStatus, obj.GetNamespace(), obj.GetName(), status)
}

type primaryCAPIObjectP[objT any] interface {
	*objT
	client.Object
}

// Migratable exposes the data and side effects needed to reconcile migration
// status for a Machine API object and its primary Cluster API counterpart.
type Migratable[
	capiT any,
	capiPT primaryCAPIObjectP[capiT],
] interface {
	MAPIObject() client.Object

	DesiredAuthority() mapiv1beta1.MachineAuthority
	CurrentAuthority() mapiv1beta1.MachineAuthority
	SynchronizedAPI() mapiv1beta1.SynchronizedAPI
	SynchronizedGeneration() int64
	MAPIConditions() []mapiv1beta1.Condition

	EnsureCAPIPaused(ctx context.Context, capi capiPT) (bool, error)
	EnsureCAPIUnpaused(ctx context.Context, capi capiPT) (bool, error)
}

// Reconcile advances migration state for a Machine API object toward its
// desired authoritative API.
func Reconcile[
	statusPT synccommon.StatusApplyConfigurationP[statusT, statusPT],
	objPT synccommon.ObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT, capiT any,
	capiPT primaryCAPIObjectP[capiT],
](
	ctx context.Context,
	k8sClient client.Client,
	controllerName string,
	capiNamespace string,
	newApplyConfig synccommon.ObjApplyConfigurationConstructor[objPT, statusPT],
	migratable Migratable[capiT, capiPT],
) (ctrl.Result, error) {
	desiredAuthority := migratable.DesiredAuthority()

	if migratable.CurrentAuthority() == "" {
		if err := validateDesiredAuthority(desiredAuthority); err != nil {
			return ctrl.Result{}, err
		}

		if err := synccommon.ApplyMigrationStatus(ctx, k8sClient, controllerName, newApplyConfig, migratable.MAPIObject(), desiredAuthority); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to apply authoritativeAPI to status: %w", err)
		}

		return ctrl.Result{}, nil
	}

	switch desiredAuthority {
	case mapiv1beta1.MachineAuthorityClusterAPI:
		return reconcileToCAPI(ctx, k8sClient, controllerName, capiNamespace, newApplyConfig, migratable)
	case mapiv1beta1.MachineAuthorityMachineAPI:
		return reconcileToMAPI(ctx, k8sClient, controllerName, capiNamespace, newApplyConfig, migratable)
	case mapiv1beta1.MachineAuthorityMigrating:
		fallthrough
	default:
		return ctrl.Result{}, validateDesiredAuthority(desiredAuthority)
	}
}

func validateDesiredAuthority(desiredAuthority mapiv1beta1.MachineAuthority) error {
	switch desiredAuthority {
	case mapiv1beta1.MachineAuthorityClusterAPI, mapiv1beta1.MachineAuthorityMachineAPI:
		return nil
	case mapiv1beta1.MachineAuthorityMigrating:
		fallthrough
	default:
		return fmt.Errorf("unable to determine desired migration direction: %w", synccommon.UnsupportedTargetAuthorityError(desiredAuthority))
	}
}

//nolint:funlen // Keep the full MachineAPI->ClusterAPI transition flow in one function.
func reconcileToCAPI[
	statusPT synccommon.StatusApplyConfigurationP[statusT, statusPT],
	objPT synccommon.ObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT, capiT any,
	capiPT primaryCAPIObjectP[capiT],
](
	ctx context.Context,
	k8sClient client.Client,
	controllerName string,
	capiNamespace string,
	newApplyConfig synccommon.ObjApplyConfigurationConstructor[objPT, statusPT],
	migratable Migratable[capiT, capiPT],
) (ctrl.Result, error) {
	switch migratable.CurrentAuthority() {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		isSynchronized, err := isStableStateSynchronized(ctx, k8sClient, capiNamespace, migratable, mapiv1beta1.MachineAuthorityMachineAPI)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to check Machine API stable sync gate: %w", err)
		}

		if !isSynchronized {
			return ctrl.Result{}, nil
		}

		if err := synccommon.ApplyMigrationStatus(ctx, k8sClient, controllerName, newApplyConfig, migratable.MAPIObject(), mapiv1beta1.MachineAuthorityMigrating); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to set authoritativeAPI to Migrating: %w", err)
		}

		return ctrl.Result{}, nil
	case mapiv1beta1.MachineAuthorityMigrating:
		// The migrating state controls the pausing of Machine API controller.
		paused, err := isMAPIPaused(migratable)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to check Machine API paused condition: %w", err)
		}

		if !paused {
			return ctrl.Result{}, nil
		}

		if err := synccommon.ApplyMigrationStatusAndResetSyncStatus(ctx, k8sClient, controllerName, newApplyConfig, migratable.MAPIObject(), mapiv1beta1.MachineAuthorityClusterAPI); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to apply authoritativeAPI and reset sync status: %w", err)
		}

		return ctrl.Result{}, nil
	case mapiv1beta1.MachineAuthorityClusterAPI:
		// This prevents the user from deliberately pausing the Cluster API object.
		// We should look into a way to allow this in the future.
		capiObj, found, err := getPrimaryCAPIObject[capiT, capiPT](ctx, k8sClient, capiNamespace, migratable.MAPIObject().GetName())
		if err != nil {
			return ctrl.Result{}, err
		}

		if !found {
			return ctrl.Result{}, nil
		}

		unpaused, err := migratable.EnsureCAPIUnpaused(ctx, capiObj)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to ensure the Cluster API side is unpaused: %w", err)
		}

		if !unpaused {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, nil
	default:
		return ctrl.Result{}, unsupportedCurrentAuthorityError(migratable.CurrentAuthority())
	}
}

//nolint:funlen // Keep the full ClusterAPI->MachineAPI transition flow in one function.
func reconcileToMAPI[
	statusPT synccommon.StatusApplyConfigurationP[statusT, statusPT],
	objPT synccommon.ObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT, capiT any,
	capiPT primaryCAPIObjectP[capiT],
](
	ctx context.Context,
	k8sClient client.Client,
	controllerName string,
	capiNamespace string,
	newApplyConfig synccommon.ObjApplyConfigurationConstructor[objPT, statusPT],
	migratable Migratable[capiT, capiPT],
) (ctrl.Result, error) {
	switch migratable.CurrentAuthority() {
	case mapiv1beta1.MachineAuthorityClusterAPI:
		capiObj, found, err := getPrimaryCAPIObject[capiT, capiPT](ctx, k8sClient, capiNamespace, migratable.MAPIObject().GetName())
		if err != nil {
			return ctrl.Result{}, err
		}

		if !found {
			return ctrl.Result{}, nil
		}

		paused, err := migratable.EnsureCAPIPaused(ctx, capiObj)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to ensure the Cluster API side is paused: %w", err)
		}

		if !paused {
			return ctrl.Result{}, nil
		}

		isSynchronized, err := isStableStateSynchronized(ctx, k8sClient, capiNamespace, migratable, mapiv1beta1.MachineAuthorityClusterAPI)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to check Cluster API stable sync gate: %w", err)
		}

		if !isSynchronized {
			return ctrl.Result{}, nil
		}

		if err := synccommon.ApplyMigrationStatus(ctx, k8sClient, controllerName, newApplyConfig, migratable.MAPIObject(), mapiv1beta1.MachineAuthorityMigrating); err != nil {
			return ctrl.Result{}, fmt.Errorf("unable to set authoritativeAPI to Migrating: %w", err)
		}

		return ctrl.Result{}, nil
	case mapiv1beta1.MachineAuthorityMigrating:
		// This state is not required by the logic. We go through Migrating to satisfy API validation rule.
		if err := synccommon.ApplyMigrationStatusAndResetSyncStatus(ctx, k8sClient, controllerName, newApplyConfig, migratable.MAPIObject(), mapiv1beta1.MachineAuthorityMachineAPI); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to apply authoritativeAPI and reset sync status: %w", err)
		}

		return ctrl.Result{}, nil
	case mapiv1beta1.MachineAuthorityMachineAPI:
		capiObj, found, err := getPrimaryCAPIObject[capiT, capiPT](ctx, k8sClient, capiNamespace, migratable.MAPIObject().GetName())
		if err != nil {
			return ctrl.Result{}, err
		}

		if !found {
			return ctrl.Result{}, nil
		}

		paused, err := migratable.EnsureCAPIPaused(ctx, capiObj)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to ensure the Cluster API side is paused: %w", err)
		}

		if !paused {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, nil
	default:
		return ctrl.Result{}, unsupportedCurrentAuthorityError(migratable.CurrentAuthority())
	}
}

func isStableStateSynchronized[
	capiT any,
	capiPT primaryCAPIObjectP[capiT],
](
	ctx context.Context,
	k8sClient client.Client,
	capiNamespace string,
	migratable Migratable[capiT, capiPT],
	authority mapiv1beta1.MachineAuthority,
) (bool, error) {
	cond, err := util.GetConditionStatus(migratable.MAPIObject(), string(controllers.SynchronizedCondition))
	if err != nil {
		return false, fmt.Errorf("unable to get synchronized condition for %s/%s: %w", migratable.MAPIObject().GetNamespace(), migratable.MAPIObject().GetName(), err)
	}

	if cond != corev1.ConditionTrue {
		return false, nil
	}

	expectedSynchronizedAPI := synccommon.AuthoritativeAPIToSynchronizedAPI(authority)
	if expectedSynchronizedAPI == nil {
		return false, unsupportedStableAuthorityError(authority)
	}

	if migratable.SynchronizedAPI() != *expectedSynchronizedAPI {
		return false, nil
	}

	switch authority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		return migratable.SynchronizedGeneration() == migratable.MAPIObject().GetGeneration(), nil
	case mapiv1beta1.MachineAuthorityClusterAPI:
		capiObj, found, err := getPrimaryCAPIObject[capiT, capiPT](ctx, k8sClient, capiNamespace, migratable.MAPIObject().GetName())
		if err != nil {
			return false, err
		}

		if !found {
			return false, nil
		}

		return migratable.SynchronizedGeneration() == capiObj.GetGeneration(), nil
	case mapiv1beta1.MachineAuthorityMigrating:
		return false, unsupportedStableAuthorityError(authority)
	default:
		return false, unsupportedStableAuthorityError(authority)
	}
}

func isMAPIPaused[
	capiT any,
	capiPT primaryCAPIObjectP[capiT],
](migratable Migratable[capiT, capiPT]) (bool, error) {
	pausedCondition := util.GetMAPICondition(migratable.MAPIConditions(), "Paused")
	if pausedCondition == nil {
		return false, nil
	}

	switch pausedCondition.Status {
	case corev1.ConditionTrue:
		return true, nil
	case corev1.ConditionFalse, corev1.ConditionUnknown:
		return false, nil
	default:
		return false, unrecognizedPausedConditionStatusError(migratable.MAPIObject(), pausedCondition.Status)
	}
}

func getPrimaryCAPIObject[
	capiT any,
	capiPT primaryCAPIObjectP[capiT],
](
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
	name string,
) (capiPT, bool, error) {
	var zero capiPT

	capiObj := capiPT(new(capiT))

	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, capiObj); err != nil {
		if client.IgnoreNotFound(err) == nil {
			return zero, false, nil
		}

		return zero, false, fmt.Errorf("failed to get primary Cluster API object %s/%s: %w", namespace, name, err)
	}

	return capiObj, true, nil
}

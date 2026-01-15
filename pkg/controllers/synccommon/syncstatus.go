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

package synccommon

import (
	"context"
	"errors"
	"fmt"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// errUnrecognizedConditionStatus is returned when the condition status is not recognized.
	errUnrecognizedConditionStatus = errors.New("error unrecognized condition status")

	// errUnsupportedSyncStatusType is returned when attempting to set sync status on a type which does not support it.
	errUnsupportedSyncStatusType = errors.New("type does not support setting sync status")

	// ErrInvalidSynchronizedAPI is returned when SynchronizedAPI has an unexpected value.
	ErrInvalidSynchronizedAPI = errors.New("invalid synchronizedAPI value")

	// ErrMissingSynchronizedAPI is returned when SynchronizedAPI is required but not set.
	ErrMissingSynchronizedAPI = errors.New("missing synchronizedAPI value")

	// ErrUnsupportedTargetAuthority is returned when the target authority is not supported.
	ErrUnsupportedTargetAuthority = errors.New("unsupported target authority")

	// ErrUnexpectedCurrentAuthorityDuringCancellation is returned when a migration cancellation
	// cannot map the current authority back to a supported source authority.
	ErrUnexpectedCurrentAuthorityDuringCancellation = errors.New("unexpected current authority while cancelling migration")
)

// ApplySyncStatus updates the MAPI object status for a sync controller, either
// Machine or MachineSet.
//
// Go is not yet able to correctly infer the first type argument, so it must be
// provided explicitly. The remaining type arguments will be inferred, and can
// be omitted.
func ApplySyncStatus[
	statusPT StatusApplyConfigurationP[statusT, statusPT],
	objPT ObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT any,
](
	ctx context.Context, k8sClient client.Client, controllerName string,
	applyConfigConstructor ObjApplyConfigurationConstructor[objPT, statusPT], mapiObj client.Object,
	status corev1.ConditionStatus, reason, message string, generation *int64,
	synchronizedAPI *mapiv1beta1.SynchronizedAPI,
) error {
	objAC, _, err := newSyncStatusApplyConfiguration(applyConfigConstructor, mapiObj, status, reason, message, generation, synchronizedAPI)
	if err != nil {
		return err
	}

	if err := k8sClient.Status().Patch(ctx, mapiObj, util.ApplyConfigPatch(objAC), client.ForceOwnership, client.FieldOwner(controllerName+"-SynchronizedCondition")); err != nil {
		return fmt.Errorf("failed to patch Machine API %T object status with synchronized condition: %w", mapiObj, err)
	}

	return nil
}

// newSyncStatusApplyConfiguration generates an apply configuration to update
// the MAPI object status for a sync controller, either Machine or MachineSet.
//
// As this data can be written by 2 different controllers (sync and migration),
// we set the resourceVersion of mapiObj in the returned configuration.
//
// Go is not yet able to correctly infer the first type argument, so it must be
// provided explicitly. The remaining type arguments will be inferred, and can
// be omitted.
func newSyncStatusApplyConfiguration[
	statusPT StatusApplyConfigurationP[statusT, statusPT],
	objPT ObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT any,
](
	applyConfigConstructor ObjApplyConfigurationConstructor[objPT, statusPT], mapiObj client.Object,
	status corev1.ConditionStatus, reason, message string, generation *int64,
	synchronizedAPI *mapiv1beta1.SynchronizedAPI,
) (objPT, statusPT, error) {
	var (
		severity               mapiv1beta1.ConditionSeverity
		synchronizedGeneration int64
		oldConditions          []mapiv1beta1.Condition

		err error
	)

	var currentSynchronizedAPI mapiv1beta1.SynchronizedAPI

	synchronizedGeneration, oldConditions, currentSynchronizedAPI, err = getPreviousSyncStatus(mapiObj)
	if err != nil {
		return nil, nil, err
	}

	if generation != nil {
		synchronizedGeneration = *generation
	}

	severity, err = conditionSeverityForStatus(status)
	if err != nil {
		return nil, nil, err
	}

	conditionAC := machinev1applyconfigs.Condition().
		WithType(controllers.SynchronizedCondition).
		WithStatus(status).
		WithReason(reason).
		WithMessage(message).
		WithSeverity(severity)

	util.SetLastTransitionTime(controllers.SynchronizedCondition, oldConditions, conditionAC)

	statusAC := statusPT(new(statusT)).
		WithConditions(conditionAC).
		WithSynchronizedGeneration(synchronizedGeneration)

	// Set SynchronizedAPI to define deterministically which object's generation
	// the SynchronizedGeneration refers to. When the caller does not supply a
	// new value, preserve the current one instead of clearing a field owned by
	// the sync controller.
	synchronizedAPI = preserveSynchronizedAPI(synchronizedAPI, currentSynchronizedAPI)
	if synchronizedAPI != nil {
		statusAC.WithSynchronizedAPI(*synchronizedAPI)
	}

	objAC := applyConfigConstructor(mapiObj.GetName(), mapiObj.GetNamespace()).
		WithResourceVersion(mapiObj.GetResourceVersion()).
		WithStatus(statusAC)

	return objAC, statusAC, nil
}

func conditionSeverityForStatus(status corev1.ConditionStatus) (mapiv1beta1.ConditionSeverity, error) {
	switch status {
	case corev1.ConditionTrue:
		return mapiv1beta1.ConditionSeverityNone, nil
	case corev1.ConditionFalse:
		return mapiv1beta1.ConditionSeverityError, nil
	case corev1.ConditionUnknown:
		return mapiv1beta1.ConditionSeverityInfo, nil
	default:
		return "", fmt.Errorf("%w: %s", errUnrecognizedConditionStatus, status)
	}
}

func preserveSynchronizedAPI(synchronizedAPI *mapiv1beta1.SynchronizedAPI, currentSynchronizedAPI mapiv1beta1.SynchronizedAPI) *mapiv1beta1.SynchronizedAPI {
	if synchronizedAPI == nil && currentSynchronizedAPI != "" {
		return ptr.To(currentSynchronizedAPI)
	}

	return synchronizedAPI
}

// AuthoritativeAPIToSynchronizedAPI converts a MachineAuthority to its corresponding SynchronizedAPI value.
// Returns nil for values that don't have a direct mapping.
func AuthoritativeAPIToSynchronizedAPI(authority mapiv1beta1.MachineAuthority) *mapiv1beta1.SynchronizedAPI {
	switch authority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		return ptr.To(mapiv1beta1.MachineAPISynchronized)
	case mapiv1beta1.MachineAuthorityClusterAPI:
		return ptr.To(mapiv1beta1.ClusterAPISynchronized)
	case mapiv1beta1.MachineAuthorityMigrating:
		return nil
	}

	return nil
}

// SynchronizedAPIToAuthoritativeAPI converts a SynchronizedAPI to its corresponding MachineAuthority.
// Returns an empty value for values that don't have a direct mapping.
func SynchronizedAPIToAuthoritativeAPI(synchronizedAPI mapiv1beta1.SynchronizedAPI) mapiv1beta1.MachineAuthority {
	switch synchronizedAPI {
	case mapiv1beta1.MachineAPISynchronized:
		return mapiv1beta1.MachineAuthorityMachineAPI
	case mapiv1beta1.ClusterAPISynchronized:
		return mapiv1beta1.MachineAuthorityClusterAPI
	}

	return ""
}

// MigrationDirection determines the current and desired authorities for a migration.
// When statusAuthority is Migrating, it uses SynchronizedAPI to infer the current authority.
// On success, currentAuthority is guaranteed to be either MachineAPI or ClusterAPI.
// Missing or invalid SynchronizedAPI values are returned as errors instead of
// surfacing as a Migrating or otherwise unsupported current authority.
func MigrationDirection(statusAuthority mapiv1beta1.MachineAuthority, synchronizedAPI mapiv1beta1.SynchronizedAPI, specAuthority mapiv1beta1.MachineAuthority) (currentAuthority, desiredAuthority mapiv1beta1.MachineAuthority, isMigrating bool, err error) {
	desiredAuthority = specAuthority
	if statusAuthority != mapiv1beta1.MachineAuthorityMigrating {
		return statusAuthority, desiredAuthority, false, nil
	}

	if synchronizedAPI == "" {
		return "", desiredAuthority, true, MissingSynchronizedAPIError()
	}

	currentAuthority = SynchronizedAPIToAuthoritativeAPI(synchronizedAPI)
	if currentAuthority == "" {
		return "", desiredAuthority, true, InvalidSynchronizedAPIError(synchronizedAPI)
	}

	return currentAuthority, desiredAuthority, true, nil
}

// UnsupportedTargetAuthorityError returns a shared error for unsupported migration targets.
func UnsupportedTargetAuthorityError(targetAuthority mapiv1beta1.MachineAuthority) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedTargetAuthority, targetAuthority)
}

// UnexpectedCurrentAuthorityDuringCancellationError returns a shared error for unexpected
// current authorities encountered while cancelling a migration.
func UnexpectedCurrentAuthorityDuringCancellationError(currentAuthority mapiv1beta1.MachineAuthority) error {
	return fmt.Errorf("%w: %s", ErrUnexpectedCurrentAuthorityDuringCancellation, currentAuthority)
}

// InvalidSynchronizedAPIError returns a shared error for unexpected SynchronizedAPI values.
func InvalidSynchronizedAPIError(synchronizedAPI mapiv1beta1.SynchronizedAPI) error {
	return fmt.Errorf("%w: %s", ErrInvalidSynchronizedAPI, synchronizedAPI)
}

// MissingSynchronizedAPIError returns a shared error for missing SynchronizedAPI values.
func MissingSynchronizedAPIError() error {
	return fmt.Errorf("%w while authoritativeAPI is Migrating", ErrMissingSynchronizedAPI)
}

func getPreviousSyncStatus(mapiObj interface{}) (int64, []mapiv1beta1.Condition, mapiv1beta1.SynchronizedAPI, error) {
	// Unlike the apply configurations, which have method accessors, we can't
	// define an interface to assert the presence of fields.
	switch o := mapiObj.(type) {
	case *mapiv1beta1.Machine:
		return o.Status.SynchronizedGeneration, o.Status.Conditions, o.Status.SynchronizedAPI, nil
	case *mapiv1beta1.MachineSet:
		return o.Status.SynchronizedGeneration, o.Status.Conditions, o.Status.SynchronizedAPI, nil
	default:
		return 0, nil, "", fmt.Errorf("%w: %T", errUnsupportedSyncStatusType, mapiObj)
	}
}

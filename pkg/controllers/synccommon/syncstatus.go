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

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"

	machinev1applyconfigs "github.com/openshift/client-go/machine/applyconfigurations/machine/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// errUnrecognizedConditionStatus is returned when the condition status is not recognized.
	errUnrecognizedConditionStatus = errors.New("error unrecognized condition status")

	// errUnsupportedSyncStatusType is returned when attempting to set sync status on a type which does not support it.
	errUnsupportedSyncStatusType = errors.New("type does not support setting sync status")
)

// ApplySyncStatus updates the MAPI object status for a sync controller, either
// Machine or MachineSet.
//
// Go is not yet able to correctly infer the first type argument, so it must be
// provided explicitly. The remaining type arguments will be inferred, and can
// be omitted.
func ApplySyncStatus[
	statusPT syncStatusApplyConfigurationP[statusT, statusPT],
	objPT syncObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT any,
](
	ctx context.Context, k8sClient client.Client, controllerName string,
	applyConfigConstructor syncObjApplyConfigurationConstructor[objPT, statusPT], mapiObj client.Object,
	status corev1.ConditionStatus, reason, message string, generation *int64,
) error {
	objAC, _, err := newSyncStatusApplyConfiguration(applyConfigConstructor, mapiObj, status, reason, message, generation)
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
	statusPT syncStatusApplyConfigurationP[statusT, statusPT],
	objPT syncObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT any,
](
	applyConfigConstructor syncObjApplyConfigurationConstructor[objPT, statusPT], mapiObj client.Object,
	status corev1.ConditionStatus, reason, message string, generation *int64,
) (objPT, statusPT, error) {
	var (
		severity               machinev1beta1.ConditionSeverity
		synchronizedGeneration int64
		oldConditions          []machinev1beta1.Condition

		err error
	)

	synchronizedGeneration, oldConditions, err = getPreviousSyncStatus(mapiObj)
	if err != nil {
		return nil, nil, err
	}

	// Update synchronizedGeneration if a new value was passed in explicitly.
	if generation != nil {
		synchronizedGeneration = *generation
	}

	switch status {
	case corev1.ConditionTrue:
		severity = machinev1beta1.ConditionSeverityNone
	case corev1.ConditionFalse:
		severity = machinev1beta1.ConditionSeverityError
	case corev1.ConditionUnknown:
		severity = machinev1beta1.ConditionSeverityInfo
	default:
		return nil, nil, fmt.Errorf("%w: %s", errUnrecognizedConditionStatus, status)
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

	objAC := applyConfigConstructor(mapiObj.GetName(), mapiObj.GetNamespace()).
		WithResourceVersion(mapiObj.GetResourceVersion()).
		WithStatus(statusAC)

	return objAC, statusAC, nil
}

func getPreviousSyncStatus(mapiObj interface{}) (int64, []machinev1beta1.Condition, error) {
	// Unlike the apply configurations, which have method accessors, we can't
	// define an interface to assert the presence of fields.
	switch o := mapiObj.(type) {
	case *machinev1beta1.Machine:
		return o.Status.SynchronizedGeneration, o.Status.Conditions, nil
	case *machinev1beta1.MachineSet:
		return o.Status.SynchronizedGeneration, o.Status.Conditions, nil
	default:
		return 0, nil, fmt.Errorf("%w: %T", errUnsupportedSyncStatusType, mapiObj)
	}
}

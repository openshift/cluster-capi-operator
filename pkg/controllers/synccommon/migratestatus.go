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
	"fmt"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ApplyMigrationStatusAndResetSyncStatus writes the migration controller status fields
// and resets the sync controller status (sets Synchronized condition to Unknown and
// synchronizedGeneration to 0). It always sets authoritativeAPI. If synchronizedAPI is
// provided, it will also be written.
//
// This is used when completing a migration to signal the sync controller that
// it needs to re-synchronize from the new authoritative API.
//
// Due to the potential for racing with the sync controller, it sets
// ResourceVersion in the operation.
func ApplyMigrationStatusAndResetSyncStatus[
	statusPT syncStatusApplyConfigurationP[statusT, statusPT],
	objPT syncObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT any,
](
	ctx context.Context, k8sClient client.Client, controllerName string,
	newApplyConfig syncObjApplyConfigurationConstructor[objPT, statusPT], mapiObj client.Object,
	authority mapiv1beta1.MachineAuthority, synchronizedAPI *mapiv1beta1.SynchronizedAPI,
) error {
	objAC, statusAC, err := newSyncStatusApplyConfiguration(newApplyConfig, mapiObj,
		corev1.ConditionUnknown, controllers.ReasonAuthoritativeAPIChanged, "Waiting for resync after change of AuthoritativeAPI", ptr.To(int64(0)))
	if err != nil {
		return err
	}

	if synchronizedAPI != nil {
		statusAC.WithSynchronizedAPI(*synchronizedAPI)
	}

	return applyAuthoritativeAPI(ctx, k8sClient, controllerName, mapiObj, authority, objAC, statusAC)
}

// ApplyMigrationStatus writes the migration controller status fields to a MAPI object.
// It always sets authoritativeAPI. If synchronizedAPI is provided, it will also be written.
func ApplyMigrationStatus[
	statusPT syncStatusApplyConfigurationP[statusT, statusPT],
	objPT syncObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT any,
](
	ctx context.Context, k8sClient client.Client, controllerName string,
	newApplyConfig syncObjApplyConfigurationConstructor[objPT, statusPT], mapiObj client.Object,
	authority mapiv1beta1.MachineAuthority, synchronizedAPI *mapiv1beta1.SynchronizedAPI,
) error {
	statusAC := statusPT(new(statusT))
	objAC := newApplyConfig(mapiObj.GetName(), mapiObj.GetNamespace()).
		WithStatus(statusAC)

	if synchronizedAPI != nil {
		statusAC.WithSynchronizedAPI(*synchronizedAPI)
	}

	return applyAuthoritativeAPI(ctx, k8sClient, controllerName, mapiObj, authority, objAC, statusAC)
}

// applyAuthoritativeAPI adds the status of the migration controller to the
// supplied apply configuration and applies the result, using the FieldOwner of
// the migration controller.  This allows us combine the sync and migration
// statuses in a single transaction when required.
func applyAuthoritativeAPI[
	statusPT syncStatusApplyConfigurationP[statusT, statusPT],
	objPT syncObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT any,
](
	ctx context.Context, k8sClient client.Client, controllerName string,
	mapiObj client.Object,
	authority mapiv1beta1.MachineAuthority,
	objAC objPT, statusAC statusPT,
) error {
	logger := logf.FromContext(ctx)
	logger.Info("Setting AuthoritativeAPI status", "authoritativeAPI", authority)

	statusAC.WithAuthoritativeAPI(authority)

	// Note that we are writing fields owned by the synchronization controller
	// and forcing ownership to the AuthoritativeAPI. The synchronization
	// controller will force ownership of its own fields back again the next
	// time it modifies them. We think this is probably going to work out ok.
	// Apologies to future self if it didn't.
	//
	// We need to do this due to a validation rule which prevents resetting
	// synchronizedGeneration unless also changing authoritativeAPI. Given that
	// these fields are owned by different controllers, some fudging is
	// required.
	if err := k8sClient.Status().Patch(ctx, mapiObj, util.ApplyConfigPatch(objAC), client.ForceOwnership, client.FieldOwner(controllerName+"-AuthoritativeAPI")); err != nil {
		return fmt.Errorf("failed to patch Machine API object set status with authoritativeAPI %q: %w", authority, err)
	}

	return nil
}

// IsMigrationCancellationRequested determines if the user wants to return to the last successfully synchronized state.
//
// A migration cancellation occurs when:
// - status.authoritativeAPI is "Migrating"
// - spec.authoritativeAPI matches status.synchronizedAPI.
func IsMigrationCancellationRequested(
	specAuthoritativeAPI mapiv1beta1.MachineAuthority,
	statusAuthoritativeAPI mapiv1beta1.MachineAuthority,
	statusSynchronizedAPI mapiv1beta1.SynchronizedAPI,
) bool {
	if statusAuthoritativeAPI != mapiv1beta1.MachineAuthorityMigrating {
		return false
	}

	if statusSynchronizedAPI == "" {
		// No synchronizedAPI set, cannot be a migration cancellation
		return false
	}

	// Check if spec matches the last synchronized API
	return AuthoritativeAPIToSynchronizedAPI(specAuthoritativeAPI) == statusSynchronizedAPI
}

// AuthoritativeAPIToSynchronizedAPI converts a MachineAuthority to its corresponding SynchronizedAPI value.
// Returns empty string for MachineAuthorityMigrating or other values that don't have a direct mapping.
func AuthoritativeAPIToSynchronizedAPI(authority mapiv1beta1.MachineAuthority) mapiv1beta1.SynchronizedAPI {
	switch authority {
	case mapiv1beta1.MachineAuthorityMachineAPI:
		return mapiv1beta1.MachineAPISynchronized
	case mapiv1beta1.MachineAuthorityClusterAPI:
		return mapiv1beta1.ClusterAPISynchronized
	case mapiv1beta1.MachineAuthorityMigrating:
		return ""
	default:
		return ""
	}
}

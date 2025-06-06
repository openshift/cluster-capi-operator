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

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ApplyAuthoritativeAPIAndResetSyncStatus writes the status of the migration
// controller, and also resets the status written by the sync controller. It
// does this in a single operation, using the field owner of the migration
// controller.
//
// Due to the potential for racing with the sync controller, it sets
// ResourceVersion in the operation.
func ApplyAuthoritativeAPIAndResetSyncStatus[
	statusPT syncStatusApplyConfigurationP[statusT, statusPT],
	objPT syncObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT any,
](
	ctx context.Context, k8sClient client.Client, controllerName string,
	newApplyConfig syncObjApplyConfigurationConstructor[objPT, statusPT], mapiObj client.Object,
	authority machinev1beta1.MachineAuthority,
) error {
	objAC, statusAC, err := newSyncStatusApplyConfiguration(newApplyConfig, mapiObj,
		corev1.ConditionUnknown, controllers.ReasonAuthoritativeAPIChanged, "Waiting for resync after change of AuthoritativeAPI", ptr.To(int64(0)))
	if err != nil {
		return err
	}

	return applyAuthoritativeAPI(ctx, k8sClient, controllerName, mapiObj, authority, objAC, statusAC)
}

// ApplyAuthoritativeAPI writes the status of the migration controller to a MAPI
// object.
func ApplyAuthoritativeAPI[
	statusPT syncStatusApplyConfigurationP[statusT, statusPT],
	objPT syncObjApplyConfigurationP[objT, objPT, statusPT],
	statusT, objT any,
](
	ctx context.Context, k8sClient client.Client, controllerName string,
	newApplyConfig syncObjApplyConfigurationConstructor[objPT, statusPT], mapiObj client.Object,
	authority machinev1beta1.MachineAuthority,
) error {
	statusAC := statusPT(new(statusT))
	objAC := newApplyConfig(mapiObj.GetName(), mapiObj.GetNamespace()).
		WithStatus(statusAC)

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
	authority machinev1beta1.MachineAuthority,
	objAC objPT, statusAC statusPT,
) error {
	logger := log.FromContext(ctx)
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

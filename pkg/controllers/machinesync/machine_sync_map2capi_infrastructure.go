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

package machinesync

import (
	"context"
	"fmt"

	"github.com/go-test/deep"
	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/util"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// createOrUpdateCAPIInfraMachine creates a CAPI infra machine from a MAPI machine, or updates if it exists and it is out of date.
// syncronizationIsProgressing will be set to true if an existing CAPI Infrastructure Machine was deleted for later recreation due to immutable changes.
//
//nolint:unparam
func (r *MachineSyncReconciler) createOrUpdateCAPIInfraMachine(ctx context.Context, sourceMAPIMachine *machinev1beta1.Machine, existingCAPIInfraMachine client.Object, convertedCAPIInfraMachine client.Object) (res ctrl.Result, zc bool, err error) {
	logger := log.FromContext(ctx)
	// This const signals whether or not we are still progressing
	// towards syncronizing the MAPI machine with the CAPI infra machine.
	// It is then passed up the stack so the syncronized condition can be set accordingly.
	const syncronizationIsProgressingFalse = false

	// If there is no existing CAPI Infrastructure machine, create a new one.
	if err := r.ensureCAPIInfraMachine(ctx, sourceMAPIMachine, existingCAPIInfraMachine, convertedCAPIInfraMachine); err != nil {
		return ctrl.Result{}, syncronizationIsProgressingFalse, fmt.Errorf("failed to ensure CAPI Infrastructure machine: %w", err)
	}

	// Compare the existing CAPI Infrastructure machine with the converted CAPI Infrastructure machine to check for changes.
	diff, err := compareCAPIInfraMachines(r.Platform, existingCAPIInfraMachine, convertedCAPIInfraMachine)
	if err != nil {
		return ctrl.Result{}, syncronizationIsProgressingFalse, fmt.Errorf("failed to compare CAPI Infrastructure machines: %w", err)
	}

	// Infrastructure machines are immutable so we delete it for spec changes.
	// The next reconciliation will create it with the expected changes.
	// Note: this could be improved to only trigger deletion on known immutable changes.
	if hasSpecChanges(diff) {
		logger.Info("Deleting the corresponding Cluster API Infrastructure machine as it is out of date, it will be recreated", "diff", fmt.Sprintf("%+v", diff))

		syncronizationIsProgressing, err := r.ensureCAPIInfraMachineDeleted(ctx, sourceMAPIMachine, existingCAPIInfraMachine)

		return ctrl.Result{}, syncronizationIsProgressing, err
	}

	// Update CAPI Infrastructure machine metadata if needed.
	metadataUpdated, err := r.ensureCAPIInfraMachineMetadataUpdated(ctx, sourceMAPIMachine, diff, convertedCAPIInfraMachine)
	if err != nil {
		return ctrl.Result{}, syncronizationIsProgressingFalse, fmt.Errorf("failed to update CAPI Infrastructure machine metadata: %w", err)
	}

	// Update CAPI Infrastructure machine status if needed.
	statusUpdated, err := r.ensureCAPIInfraMachineStatusUpdated(ctx, sourceMAPIMachine, existingCAPIInfraMachine, convertedCAPIInfraMachine, diff, metadataUpdated)
	if err != nil {
		return ctrl.Result{}, syncronizationIsProgressingFalse, fmt.Errorf("failed to update CAPI Infrastructure machine status: %w", err)
	}

	if metadataUpdated || statusUpdated {
		logger.Info("Successfully updated CAPI machine")
	} else {
		logger.Info("No changes detected for CAPI machine")
	}

	return ctrl.Result{}, syncronizationIsProgressingFalse, nil
}

// ensureCAPIInfraMachine creates a new CAPI Infrastructure machine if one doesn't exist.
func (r *MachineSyncReconciler) ensureCAPIInfraMachine(ctx context.Context, sourceMAPIMachine *machinev1beta1.Machine, existingCAPIInfraMachine, convertedCAPIInfraMachine client.Object) error {
	// If there is an existing CAPI Infrastructure machine, no need to create one.
	if existingCAPIInfraMachine != nil {
		return nil
	}

	logger := log.FromContext(ctx)

	var ok bool

	existingCAPIInfraMachine, ok = convertedCAPIInfraMachine.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("failed to assert convertedCAPIInfraMachine: %w", errAssertingInfrasMachineClientObject)
	}

	if err := r.Create(ctx, existingCAPIInfraMachine); err != nil {
		logger.Error(err, "Failed to create CAPI Infrastructure machine")

		createErr := fmt.Errorf("failed to create CAPI Infrastructure machine: %w", err)
		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, sourceMAPIMachine, corev1.ConditionFalse, reasonFailedToCreateCAPIMachine, createErr.Error(), nil); condErr != nil {
			return utilerrors.NewAggregate([]error{createErr, condErr})
		}

		return createErr
	}

	logger.Info("Successfully created CAPI Infrastructure machine", "name", existingCAPIInfraMachine.GetName())

	return nil
}

// ensureCAPIInfraMachineMetadataUpdated updates the CAPI Infrastructure machine if changes are detected to the metadata or spec (if possible).
func (r *MachineSyncReconciler) ensureCAPIInfraMachineMetadataUpdated(ctx context.Context, mapiMachine *machinev1beta1.Machine, diff map[string]any, updatedOrCreatedCAPIInfraMachine client.Object) (bool, error) {
	logger := log.FromContext(ctx)

	// If there are no spec changes, return early.
	if !hasMetadataChanges(diff) {
		return false, nil
	}

	logger.Info("Changes detected for CAPI Infrastructure machine. Updating it", "diff", fmt.Sprintf("%+v", diff))

	if err := r.Update(ctx, updatedOrCreatedCAPIInfraMachine); err != nil {
		logger.Error(err, "Failed to update CAPI Infrastructure machine")

		updateErr := fmt.Errorf("failed to update CAPI Infrastructure machine: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachine, updateErr.Error(), nil); condErr != nil {
			return false, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return false, updateErr
	}

	return true, nil
}

func (r *MachineSyncReconciler) ensureCAPIInfraMachineStatusUpdated(ctx context.Context, mapiMachine *machinev1beta1.Machine, existingCAPIInfraMachine, convertedCAPIInfraMachine client.Object, diff map[string]any, specUpdated bool) (bool, error) {
	logger := log.FromContext(ctx)

	// If there are no status changes and the spec has not been updated, return early.
	if !hasStatusChanges(diff) && !specUpdated {
		return false, nil
	}

	// If the source API object (MAPI Machine) status.synchronizedGeneration does not match the objectmeta.generation
	// it means the source API object status has not yet caught up with the desired spec,
	// so we don't want to update the CAPI Infrastructure machine status until that has happened.
	if mapiMachine.Status.SynchronizedGeneration != mapiMachine.ObjectMeta.Generation {
		logger.Info("Changes detected for CAPI Infrastructure machine status, but the MAPI machine spec has not been observed yet, skipping status update")

		return false, nil
	}

	target, ok := existingCAPIInfraMachine.DeepCopyObject().(client.Object)
	if !ok {
		return false, fmt.Errorf("failed to assert existingCAPIInfraMachine: %w", errAssertingInfrasMachineClientObject)
	}

	patchBase := client.MergeFrom(target)

	if err := setChangedCAPIInfraMachineStatusFields(r.Platform, existingCAPIInfraMachine, convertedCAPIInfraMachine); err != nil {
		return false, fmt.Errorf("failed to set CAPI Infrastructure Machine status: %w", err)
	}

	// // Update the observed generation to match the updated source API object generation.
	// existingCAPIInfraMachine.Status.ObservedGeneration = updatedOrCreatedCAPIInfraMachine.ObjectMeta.Generation

	isPatchRequired, err := util.IsPatchRequired(existingCAPIInfraMachine, patchBase)
	if err != nil {
		return false, fmt.Errorf("failed to check if patch is required: %w", err)
	}

	if !isPatchRequired {
		// If the patch is not required, return early.
		return false, nil
	}

	logger.Info("Changes detected for CAPI Infrastructure machine status. Updating it")

	if err := r.Status().Patch(ctx, existingCAPIInfraMachine, patchBase); err != nil {
		logger.Error(err, "Failed to update CAPI Infrastructure machine status")
		updateErr := fmt.Errorf("failed to update status: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachine, updateErr.Error(), nil); condErr != nil {
			return false, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return false, updateErr
	}

	return true, nil
}

// ensureCAPIInfraMachineDeleted deletes the CAPI Infrastructure machine if changes are detected to the spec which is immutable.
func (r *MachineSyncReconciler) ensureCAPIInfraMachineDeleted(ctx context.Context, sourceMAPIMachine *machinev1beta1.Machine, existingCAPIInfraMachine client.Object) (bool, error) {
	logger := log.FromContext(ctx)

	// Trigger deletion
	if err := r.Delete(ctx, existingCAPIInfraMachine); err != nil {
		logger.Error(err, "Failed to delete Cluster API Infrastructure machine")

		deleteErr := fmt.Errorf("failed to delete Cluster API Infrastructure machine: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, sourceMAPIMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachine, deleteErr.Error(), nil); condErr != nil {
			return false, utilerrors.NewAggregate([]error{deleteErr, condErr})
		}

		return false, deleteErr
	}

	// Remove finalizers from the deleting CAPI infraMachine, it is not authoritative.
	existingCAPIInfraMachine.SetFinalizers(nil)

	if err := r.Update(ctx, existingCAPIInfraMachine); err != nil {
		logger.Error(err, "Failed to remove finalizer for deleting Cluster API Infrastructure machine")

		deleteErr := fmt.Errorf("failed to remove finalizer for deleting Cluster API Infrastructure machine: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, sourceMAPIMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIInfraMachine, deleteErr.Error(), nil); condErr != nil {
			return false, utilerrors.NewAggregate([]error{deleteErr, condErr})
		}

		return false, deleteErr
	}

	logger.Info("Successfully deleted outdated Cluster API Infrastructure machine")

	// Return with syncronized as progressing to signal the caller
	// we are still progressing and aren't fully synced yet.
	return true, nil
}

// compareCAPIInfraMachines compares CAPI infra machines a and b, and returns a list of differences, or none if there are none.
//
//nolint:funlen,gocognit
func compareCAPIInfraMachines(platform configv1.PlatformType, infraMachine1, infraMachine2 client.Object) (map[string]any, error) {
	diff := make(map[string]any)

	switch platform {
	case configv1.AWSPlatformType:
		typedInfraMachine1, ok := infraMachine1.(*awsv1.AWSMachine)
		if !ok {
			return nil, errAssertingCAPIAWSMachine
		}

		typedinfraMachine2, ok := infraMachine2.(*awsv1.AWSMachine)
		if !ok {
			return nil, errAssertingCAPIAWSMachine
		}

		if diffSpec := deep.Equal(typedInfraMachine1.Spec, typedinfraMachine2.Spec); len(diffSpec) > 0 {
			diff[".spec"] = diffSpec
		}

		if diffMetadata := util.ObjectMetaEqual(typedInfraMachine1.ObjectMeta, typedinfraMachine2.ObjectMeta); len(diffMetadata) > 0 {
			diff[".metadata"] = diffMetadata
		}

		if diffStatus := deep.Equal(typedInfraMachine1.Status, typedinfraMachine2.Status); len(diffStatus) > 0 {
			diff[".status"] = diffStatus
		}
	case configv1.OpenStackPlatformType:
		typedInfraMachine1, ok := infraMachine1.(*openstackv1.OpenStackMachine)
		if !ok {
			return nil, errAssertingCAPIOpenStackMachine
		}

		typedinfraMachine2, ok := infraMachine2.(*openstackv1.OpenStackMachine)
		if !ok {
			return nil, errAssertingCAPIOpenStackMachine
		}

		if diffSpec := deep.Equal(typedInfraMachine1.Spec, typedinfraMachine2.Spec); len(diffSpec) > 0 {
			diff[".spec"] = diffSpec
		}

		if diffMetadata := util.ObjectMetaEqual(typedInfraMachine1.ObjectMeta, typedinfraMachine2.ObjectMeta); len(diffMetadata) > 0 {
			diff[".metadata"] = diffMetadata
		}

		if diffStatus := deep.Equal(typedInfraMachine1.Status, typedinfraMachine2.Status); len(diffStatus) > 0 {
			diff[".status"] = diffStatus
		}
	case configv1.PowerVSPlatformType:
		typedInfraMachine1, ok := infraMachine1.(*ibmpowervsv1.IBMPowerVSMachine)
		if !ok {
			return nil, errAssertingCAPIIBMPowerVSMachine
		}

		typedinfraMachine2, ok := infraMachine2.(*ibmpowervsv1.IBMPowerVSMachine)
		if !ok {
			return nil, errAssertingCAPIIBMPowerVSMachine
		}

		if diffSpec := deep.Equal(typedInfraMachine1.Spec, typedinfraMachine2.Spec); len(diffSpec) > 0 {
			diff[".spec"] = diffSpec
		}

		if diffMetadata := util.ObjectMetaEqual(typedInfraMachine1.ObjectMeta, typedinfraMachine2.ObjectMeta); len(diffMetadata) > 0 {
			diff[".metadata"] = diffMetadata
		}

		if diffStatus := deep.Equal(typedInfraMachine1.Status, typedinfraMachine2.Status); len(diffStatus) > 0 {
			diff[".status"] = diffStatus
		}

	default:
		return nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}

	return diff, nil
}

// setChangedCAPIInfraMachineStatusFields sets the updated fields in the CAPI Infrastructure machine status.
func setChangedCAPIInfraMachineStatusFields(platform configv1.PlatformType, existingCAPIInfraMachine, convertedCAPIInfraMachine client.Object) error {
	switch platform {
	case configv1.AWSPlatformType:
		existing, ok := existingCAPIInfraMachine.(*awsv1.AWSMachine)
		if !ok {
			return errAssertingCAPIAWSMachine
		}

		converted, ok := convertedCAPIInfraMachine.(*awsv1.AWSMachine)
		if !ok {
			return errAssertingCAPIAWSMachine
		}

		ensureCAPIConditions(existing, converted)

		// No need to merge v1beta2 conditions because they don't exist for AWSMachine's.

		// Finally overwrite the entire existingCAPIMachine status with the convertedCAPIMachine status.
		existing.Status = converted.Status

		return nil
	case configv1.OpenStackPlatformType:
		existing, ok := existingCAPIInfraMachine.(*openstackv1.OpenStackMachine)
		if !ok {
			return errAssertingCAPIOpenStackMachine
		}

		converted, ok := convertedCAPIInfraMachine.(*openstackv1.OpenStackMachine)
		if !ok {
			return errAssertingCAPIOpenStackMachine
		}

		ensureCAPIConditions(existing, converted)

		// No need to merge v1beta2 conditions because they don't exist for OpenstackMachine's.

		// Finally overwrite the entire existingCAPIMachine status with the convertedCAPIMachine status.
		existing.Status = converted.Status

		return nil
	case configv1.PowerVSPlatformType:
		existing, ok := existingCAPIInfraMachine.(*ibmpowervsv1.IBMPowerVSMachine)
		if !ok {
			return errAssertingCAPIIBMPowerVSMachine
		}

		converted, ok := convertedCAPIInfraMachine.(*ibmpowervsv1.IBMPowerVSMachine)
		if !ok {
			return errAssertingCAPIIBMPowerVSMachine
		}

		ensureCAPIConditions(existing, converted)

		// Merge the v1beta2 conditions.
		if converted.Status.V1Beta2 != nil && existing.Status.V1Beta2 != nil {
			ensureCAPIV1Beta2Conditions(existing, converted)
		}

		// Finally overwrite the entire existing status with the convertedCAPIMachine status.
		existing.Status = converted.Status

		return nil
	default:
		return fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

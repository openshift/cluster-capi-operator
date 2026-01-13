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

	configv1 "github.com/openshift/api/config/v1"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-capi-operator/pkg/util"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	ibmpowervsv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	openstackv1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// createOrUpdateCAPIInfraMachine creates a Cluster API infra machine from a Machine API machine, or updates if it exists and it is out of date.
// syncronizationIsProgressing will be set to true if an existing Cluster API Infrastructure Machine was deleted for later recreation due to immutable changes.
//
//nolint:unparam
func (r *MachineSyncReconciler) createOrUpdateCAPIInfraMachine(ctx context.Context, sourceMAPIMachine *mapiv1beta1.Machine, existingCAPIInfraMachine client.Object, convertedCAPIInfraMachine client.Object) (res ctrl.Result, zc bool, err error) {
	logger := logf.FromContext(ctx)
	// This const signals whether or not we are still progressing
	// towards syncronizing the Machine API machine with the Cluster API infra machine.
	// It is then passed up the stack so the syncronized condition can be set accordingly.
	const syncronizationIsProgressingFalse = false

	// If there is an existing Cluster API Infrastructure machine, no need to create one.
	if util.IsNilObject(existingCAPIInfraMachine) {
		// If there is no existing Cluster API Infrastructure machine, create a new one.
		existingCAPIInfraMachine, err = r.ensureCAPIInfraMachine(ctx, sourceMAPIMachine, convertedCAPIInfraMachine)
		if err != nil {
			return ctrl.Result{}, syncronizationIsProgressingFalse, fmt.Errorf("failed to ensure Cluster API Infrastructure machine: %w", err)
		}
	}

	// Compare the existing Cluster API Infrastructure machine with the converted Cluster API Infrastructure machine to check for changes.
	diff, err := compareCAPIInfraMachines(r.Platform, existingCAPIInfraMachine, convertedCAPIInfraMachine)
	if err != nil {
		return ctrl.Result{}, syncronizationIsProgressingFalse, fmt.Errorf("failed to compare Cluster API Infrastructure machines: %w", err)
	}

	// Infrastructure machines are immutable so we delete it for spec changes.
	// The next reconciliation will create it with the expected changes.
	// Note: this could be improved to only trigger deletion on known immutable changes.
	if diff.HasSpecChanges() {
		logger.Info("Deleting the corresponding Cluster API Infrastructure machine as it is out of date, it will be recreated", "diff", fmt.Sprintf("%+v", diff))

		syncronizationIsProgressing, err := r.ensureCAPIInfraMachineDeleted(ctx, sourceMAPIMachine, existingCAPIInfraMachine)

		return ctrl.Result{}, syncronizationIsProgressing, err
	}

	// Update Cluster API Infrastructure machine metadata if needed.
	metadataUpdated, err := r.ensureCAPIInfraMachineMetadataUpdated(ctx, sourceMAPIMachine, diff, convertedCAPIInfraMachine)
	if err != nil {
		return ctrl.Result{}, syncronizationIsProgressingFalse, fmt.Errorf("failed to update Cluster API Infrastructure machine metadata: %w", err)
	}

	// Update Cluster API Infrastructure machine status if needed.
	statusUpdated, err := r.ensureCAPIInfraMachineStatusUpdated(ctx, sourceMAPIMachine, existingCAPIInfraMachine, convertedCAPIInfraMachine, diff, metadataUpdated)
	if err != nil {
		return ctrl.Result{}, syncronizationIsProgressingFalse, fmt.Errorf("failed to update Cluster API Infrastructure machine status: %w", err)
	}

	if metadataUpdated || statusUpdated {
		logger.Info("Successfully updated Cluster API Infrastructure machine")
	} else {
		logger.Info("No changes detected for Cluster API Infrastructure machine")
	}

	return ctrl.Result{}, syncronizationIsProgressingFalse, nil
}

// ensureCAPIInfraMachine creates a new Cluster API Infrastructure machine if one doesn't exist and returns the created one.
func (r *MachineSyncReconciler) ensureCAPIInfraMachine(ctx context.Context, sourceMAPIMachine *mapiv1beta1.Machine, convertedCAPIInfraMachine client.Object) (client.Object, error) {
	logger := logf.FromContext(ctx)

	var ok bool

	createdCAPIInfraMachine, ok := convertedCAPIInfraMachine.DeepCopyObject().(client.Object)
	if !ok {
		return nil, fmt.Errorf("failed to assert convertedCAPIInfraMachine: %w", errAssertingInfrasMachineClientObject)
	}

	if err := r.Create(ctx, createdCAPIInfraMachine); err != nil {
		logger.Error(err, "Failed to create Cluster API Infrastructure machine")

		createErr := fmt.Errorf("failed to create Cluster API Infrastructure machine: %w", err)
		if condErr := r.applySynchronizedConditionWithPatch(
			ctx, sourceMAPIMachine, corev1.ConditionFalse, reasonFailedToCreateCAPIMachine, createErr.Error(), nil); condErr != nil {
			return nil, utilerrors.NewAggregate([]error{createErr, condErr})
		}

		return nil, createErr
	}

	logger.Info("Successfully created Cluster API Infrastructure machine", "name", createdCAPIInfraMachine.GetName())

	return createdCAPIInfraMachine, nil
}

// ensureCAPIInfraMachineMetadataUpdated updates the Cluster API Infrastructure machine if changes are detected to the metadata or spec (if possible).
func (r *MachineSyncReconciler) ensureCAPIInfraMachineMetadataUpdated(ctx context.Context, mapiMachine *mapiv1beta1.Machine, diff util.DiffResult, updatedOrCreatedCAPIInfraMachine client.Object) (bool, error) {
	logger := logf.FromContext(ctx)

	// If there are no spec changes, return early.
	if !diff.HasMetadataChanges() {
		return false, nil
	}

	logger.Info("Changes detected for Cluster API Infrastructure machine. Updating it", "diff", fmt.Sprintf("%+v", diff))

	if err := r.Update(ctx, updatedOrCreatedCAPIInfraMachine); err != nil {
		logger.Error(err, "Failed to update Cluster API Infrastructure machine")

		updateErr := fmt.Errorf("failed to update Cluster API Infrastructure machine: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachine, updateErr.Error(), nil); condErr != nil {
			return false, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return false, updateErr
	}

	return true, nil
}

func (r *MachineSyncReconciler) ensureCAPIInfraMachineStatusUpdated(ctx context.Context, mapiMachine *mapiv1beta1.Machine, existingCAPIInfraMachine, convertedCAPIInfraMachine client.Object, diff util.DiffResult, specUpdated bool) (bool, error) {
	logger := logf.FromContext(ctx)

	// If there are no status changes and the spec has not been updated, return early.
	if !diff.HasStatusChanges() && !specUpdated {
		return false, nil
	}

	// If the source API object (MAPI Machine) status.synchronizedGeneration does not match the objectmeta.generation
	// it means the source API object status has not yet caught up with the desired spec,
	// so we don't want to update the Cluster API Infrastructure machine status until that has happened.
	if mapiMachine.Status.SynchronizedGeneration != mapiMachine.ObjectMeta.Generation {
		logger.Info("Changes detected for Cluster API Infrastructure machine status, but the MAPI machine spec has not been observed yet, skipping status update")

		return false, nil
	}

	target, ok := existingCAPIInfraMachine.DeepCopyObject().(client.Object)
	if !ok {
		return false, fmt.Errorf("failed to assert existingCAPIInfraMachine: %w", errAssertingInfrasMachineClientObject)
	}

	patchBase := client.MergeFrom(target)

	if err := setChangedCAPIInfraMachineStatusFields(r.Platform, existingCAPIInfraMachine, convertedCAPIInfraMachine); err != nil {
		return false, fmt.Errorf("failed to set Cluster API Infrastructure Machine status: %w", err)
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

	logger.Info("Changes detected for Cluster API Infrastructure machine status. Updating it")

	if err := r.Status().Patch(ctx, existingCAPIInfraMachine, patchBase); err != nil {
		logger.Error(err, "Failed to update Cluster API Infrastructure machine status")
		updateErr := fmt.Errorf("failed to update status: %w", err)

		if condErr := r.applySynchronizedConditionWithPatch(ctx, mapiMachine, corev1.ConditionFalse, reasonFailedToUpdateCAPIMachine, updateErr.Error(), nil); condErr != nil {
			return false, utilerrors.NewAggregate([]error{updateErr, condErr})
		}

		return false, updateErr
	}

	return true, nil
}

// ensureCAPIInfraMachineDeleted deletes the Cluster API Infrastructure machine if changes are detected to the spec which is immutable.
func (r *MachineSyncReconciler) ensureCAPIInfraMachineDeleted(ctx context.Context, sourceMAPIMachine *mapiv1beta1.Machine, existingCAPIInfraMachine client.Object) (bool, error) {
	logger := logf.FromContext(ctx)

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

	// Remove finalizers from the deleting Cluster API infraMachine, it is not authoritative.
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

// compareCAPIInfraMachines compares Cluster API infra machines a and b, and returns a list of differences, or none if there are none.
func compareCAPIInfraMachines(platform configv1.PlatformType, infraMachine1, infraMachine2 client.Object) (util.DiffResult, error) {
	diffOpts := []util.DiffOption{}

	// Make per provider adjustments to the differ.
	switch platform {
	case configv1.AWSPlatformType:
	case configv1.OpenStackPlatformType:
	case configv1.PowerVSPlatformType:
	default:
		return nil, fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}

	diff, err := util.NewDefaultDiffer(
		append(diffOpts,
			// The paused condition is always handled by the corresponding CAPI controller.
			util.WithIgnoreConditionType(clusterv1.PausedCondition),
		)...,
	).Diff(infraMachine1, infraMachine2)
	if err != nil {
		return nil, fmt.Errorf("failed to compare Cluster API infrastructure machines: %w", err)
	}

	return diff, nil
}

// setChangedCAPIInfraMachineStatusFields sets the updated fields in the Cluster API Infrastructure machine status.
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

		util.EnsureCAPIV1Beta1Conditions(existing, converted)

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

		util.EnsureCAPIV1Beta1Conditions(existing, converted)

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

		util.EnsureCAPIV1Beta1Conditions(existing, converted)

		// Merge the v1beta2 conditions.
		util.EnsureCAPIV1Beta2Conditions(existing, converted)

		// Finally overwrite the entire existing status with the convertedCAPIMachine status.
		existing.Status = converted.Status

		return nil
	default:
		return fmt.Errorf("%w: %s", errPlatformNotSupported, platform)
	}
}

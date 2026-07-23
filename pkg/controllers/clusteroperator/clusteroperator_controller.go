/*
Copyright 2024 Red Hat, Inc.

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

package clusteroperator

import (
	"context"
	"fmt"
	"slices"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/installer"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/revision"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
	"github.com/openshift/cluster-capi-operator/pkg/util"
)

const (
	capiUnsupportedPlatformMsg = "Cluster API is not yet implemented on this platform"
	controllerName             = "ClusterOperatorController"
)

// ClusterOperatorController watches the cluster-api ClusterOperator and
// aggregates per-controller sub-conditions into top-level conditions.
type ClusterOperatorController struct {
	client.Client
	ReleaseVersion        string
	IsUnsupportedPlatform bool
}

// Reconcile reconciles the cluster-api ClusterOperator object.
func (r *ClusterOperatorController) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName(controllerName)

	co := &configv1.ClusterOperator{}
	if err := r.Get(ctx, client.ObjectKey{Name: controllers.ClusterOperatorName}, co); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ClusterOperator: %w", err)
	}

	log.Info("Reconciling ClusterOperator aggregation")

	var conditions []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration

	if r.IsUnsupportedPlatform {
		conditions = r.unsupportedPlatformStatus()
	} else {
		conditions = r.aggregatedStatus(co.Status.Conditions)
	}

	// Merge new conditions with existing conditions and patch if changes are required.
	conditionsChanged := operatorstatus.MergeConditions(conditions, co.Status.Conditions)
	versionChanged := r.IsUnsupportedPlatform &&
		currentOperatorVersion(co.Status.Versions, operatorstatus.OperatorVersionKey) != r.ReleaseVersion

	if conditionsChanged || versionChanged {
		if err := r.writeStatus(ctx, co, conditions); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// currentOperatorVersion returns the version string for the given key in the
// ClusterOperator's status versions list, or an empty string if not found.
func currentOperatorVersion(versions []configv1.OperandVersion, name string) string {
	for i := range versions {
		if versions[i].Name == name {
			return versions[i].Version
		}
	}

	return ""
}

func (r *ClusterOperatorController) writeStatus(ctx context.Context, co *configv1.ClusterOperator, conditions []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration) error {
	applyConfig := configv1apply.ClusterOperator(controllers.ClusterOperatorName).
		WithUID(co.UID).
		WithStatus(configv1apply.ClusterOperatorStatus().
			WithConditions(conditions...),
		)

	// We don't run the revision controller on unsupported platforms, so we must
	// write the release version here.
	if r.IsUnsupportedPlatform {
		applyConfig.Status = applyConfig.Status.WithVersions(
			configv1apply.OperandVersion().
				WithName(operatorstatus.OperatorVersionKey).
				WithVersion(r.ReleaseVersion))
	}

	if err := r.Status().Patch(ctx, co, util.ApplyConfigPatch(applyConfig),
		operatorstatus.CAPIFieldOwner(controllerName), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to write ClusterOperator status: %w", err)
	}

	return nil
}

// unsupportedPlatformStatus sets a fixed status with Available=true,
// Progressing=false, Degraded=false, Upgradeable=true when running on an
// unsupported platform.
func (r *ClusterOperatorController) unsupportedPlatformStatus() []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	return []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{
		condition(configv1.OperatorAvailable, configv1.ConditionTrue, operatorstatus.ReasonAsExpected, capiUnsupportedPlatformMsg),
		condition(configv1.OperatorProgressing, configv1.ConditionFalse, operatorstatus.ReasonAsExpected, ""),
		condition(configv1.OperatorDegraded, configv1.ConditionFalse, operatorstatus.ReasonAsExpected, ""),
		condition(configv1.OperatorUpgradeable, configv1.ConditionTrue, operatorstatus.ReasonAsExpected, ""),
	}
}

type subcontrollerStatus struct {
	controller             operatorstatus.ControllerResultGenerator
	available, progressing subcontrollerCondition
}

type subcontrollerCondition struct {
	status  configv1.ConditionStatus
	reason  operatorstatus.Reason
	message string
}

func getSubcontrollerCondition(conditions []configv1.ClusterOperatorStatusCondition, condType configv1.ClusterStatusConditionType) subcontrollerCondition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return subcontrollerCondition{
				status:  conditions[i].Status,
				reason:  operatorstatus.ReasonFromString(conditions[i].Reason),
				message: conditions[i].Message,
			}
		}
	}

	return subcontrollerCondition{
		status:  configv1.ConditionUnknown,
		reason:  operatorstatus.ReasonUninitialized,
		message: "initializing",
	}
}

func (r *ClusterOperatorController) aggregatedStatus(currentConditions []configv1.ClusterOperatorStatusCondition) []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	newConditions := []*configv1apply.ClusterOperatorStatusConditionApplyConfiguration{
		// The capi operator does not yet set the degraded condition. This will
		// be added by automatically flagging a Progressing condition which
		// lasts longer than some duration.
		condition(configv1.OperatorDegraded, configv1.ConditionFalse, operatorstatus.ReasonAsExpected, ""),

		// Nothing the capi operator currently does prevents upgradeability.
		// This will be added when CRD compatibility is integrated with the
		// installer and revision controllers.
		condition(configv1.OperatorUpgradeable, configv1.ConditionTrue, operatorstatus.ReasonAsExpected, ""),
	}

	// Sub-controllers whose Progressing and Degraded conditions are aggregated
	subControllers := []operatorstatus.ControllerResultGenerator{
		installer.ResultGenerator,
		revision.ResultGenerator,
		// TBD as they are migrated:
		// - corecluster
		// - infracluster
		// - secretsync
		// - kubeconfig
	}

	// Populate subcontrollerStatus with the available and progressing conditions of each subcontroller.
	subcontrollerStatuses := util.SliceMap(subControllers, func(subController operatorstatus.ControllerResultGenerator) subcontrollerStatus {
		availableType := subController.SubConditionType(operatorstatus.ConditionAvailableSuffix)
		progressingType := subController.SubConditionType(operatorstatus.ConditionProgressingSuffix)

		return subcontrollerStatus{
			controller:  subController,
			available:   getSubcontrollerCondition(currentConditions, availableType),
			progressing: getSubcontrollerCondition(currentConditions, progressingType),
		}
	})

	// We are progressing if we find any subcontroller with a progressing condition that is true or unknown.
	isProgressing := slices.IndexFunc(subcontrollerStatuses, func(status subcontrollerStatus) bool {
		return status.progressing.status == configv1.ConditionTrue || status.progressing.status == configv1.ConditionUnknown
	}) >= 0
	progressingReason, progressingMessage := aggregateReasonAndMessage(subcontrollerStatuses, func(s subcontrollerStatus) subcontrollerCondition {
		return s.progressing
	})

	switch {
	case isProgressing:
		newConditions = append(newConditions, condition(configv1.OperatorProgressing, configv1.ConditionTrue, progressingReason, progressingMessage))
	case progressingReason > operatorstatus.ReasonAsExpected:
		newConditions = append(newConditions, condition(configv1.OperatorProgressing, configv1.ConditionFalse, progressingReason, progressingMessage))
	default:
		newConditions = append(newConditions, condition(configv1.OperatorProgressing, configv1.ConditionFalse, operatorstatus.ReasonAsExpected, ""))
	}

	// We are not available if we find any subcontroller with an available condition that is not explicitly true.
	notAvailable := slices.IndexFunc(subcontrollerStatuses, func(status subcontrollerStatus) bool {
		return status.available.status != configv1.ConditionTrue
	}) >= 0
	availableReason, availableMessage := aggregateReasonAndMessage(subcontrollerStatuses, func(s subcontrollerStatus) subcontrollerCondition {
		return s.available
	})

	if notAvailable {
		newConditions = append(newConditions, condition(configv1.OperatorAvailable, configv1.ConditionFalse, availableReason, availableMessage))
	} else {
		newConditions = append(newConditions, condition(configv1.OperatorAvailable, configv1.ConditionTrue, operatorstatus.ReasonAsExpected, "Cluster API Operator is available"))
	}

	return newConditions
}

func aggregateReasonAndMessage(statuses []subcontrollerStatus, extract func(subcontrollerStatus) subcontrollerCondition) (operatorstatus.Reason, string) {
	var maxReason operatorstatus.Reason

	var parts []string

	for _, s := range statuses {
		cond := extract(s)
		if cond.reason <= operatorstatus.ReasonAsExpected {
			continue
		}

		if cond.reason > maxReason {
			maxReason = cond.reason
		}

		if cond.message != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", s.controller, cond.message))
		} else {
			parts = append(parts, string(s.controller))
		}
	}

	return maxReason, strings.Join(parts, "; ")
}

func condition(condType configv1.ClusterStatusConditionType, status configv1.ConditionStatus, reason operatorstatus.Reason, message string) *configv1apply.ClusterOperatorStatusConditionApplyConfiguration {
	return configv1apply.ClusterOperatorStatusCondition().
		WithType(condType).
		WithStatus(status).
		WithReason(reason.String()).
		WithMessage(message)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterOperatorController) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		Named(controllerName).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(operatorstatus.ClusterOperatorStatusChanged())).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

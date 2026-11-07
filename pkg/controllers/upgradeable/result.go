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
package upgradeable

import (
	configv1 "github.com/openshift/api/config/v1"
	configv1apply "github.com/openshift/client-go/config/applyconfigurations/config/v1"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

// UpgradeableResult combines controller health with the assessment verdict.
type UpgradeableResult struct {
	operatorstatus.ReconcileResult
}

// WithUpgradeable attaches the assessment condition to the controller result.
func (r UpgradeableResult) WithUpgradeable(status configv1.ConditionStatus, reason, message string) UpgradeableResult {
	// Health and the verdict share one SSA field owner. Writing them together
	// prevents either apply from accidentally relinquishing the other's fields.
	r.ReconcileResult = r.WithConditions(configv1apply.ClusterOperatorStatusCondition().
		WithType(configv1.OperatorUpgradeable).WithStatus(status).WithReason(reason).WithMessage(message))

	return r
}

func assessmentError(err error) UpgradeableResult {
	return UpgradeableResult{ResultGenerator.Error(err)}.WithUpgradeable(configv1.ConditionUnknown, "EphemeralError", err.Error())
}

func assessmentWaiting(description string) UpgradeableResult {
	return UpgradeableResult{ResultGenerator.WaitingOnExternal(description)}.WithUpgradeable(configv1.ConditionUnknown, "Progressing", "")
}

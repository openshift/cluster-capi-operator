// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	capiframework "github.com/openshift/cluster-capi-operator/e2e/framework"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

const (
	// SynchronizedCondition indicates that a machine resource has been successfully synchronized between MAPI and CAPI during migration.
	SynchronizedCondition mapiv1beta1.ConditionType = "Synchronized"
	// MAPIPausedCondition represents the paused state for MAPI machines.
	MAPIPausedCondition mapiv1beta1.ConditionType = "Paused"
	// CAPIPausedCondition represents the paused state for CAPI machines.
	CAPIPausedCondition = clusterv1.PausedCondition
)

// skipUnlessMigrationEnabled skips the current spec if Machine API migration
// is not enabled for the detected platform. This mirrors the gate checks in
// cmd/machine-api-migration/main.go: the general MachineAPIMigration gate
// must be enabled, and platform-specific gates (e.g. MachineAPIMigrationVSphere)
// must also be enabled when applicable.
func skipUnlessMigrationEnabled() {
	GinkgoHelper()

	if !capiframework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateMachineAPIMigration) {
		Skip("MachineAPIMigration feature gate is not enabled")
	}

	switch platform {
	case configv1.VSpherePlatformType:
		if !capiframework.IsFeatureGateEnabled(ctx, cl, features.FeatureGateMachineAPIMigrationVSphere) {
			Skip(fmt.Sprintf("MachineAPIMigrationVSphere feature gate is not enabled for %s", platform))
		}
	}
}

// generateName returns a unique resource name by appending a random suffix to
// the given prefix. This avoids name collisions between Ordered test contexts
// that run sequentially on the same cluster.
//
// TODO: migrate the create helpers to use Kubernetes metadata.generateName
// directly, which would let the API server guarantee uniqueness. That refactor
// touches every helper signature and caller, so it's deferred to a follow-up.
func generateName(prefix string) string {
	return prefix + utilrand.String(5)
}

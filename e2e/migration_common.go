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
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
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

package e2e

import (
	"fmt"
	"time"

	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
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

// UniqueName generates a unique name with the given prefix using nanosecond timestamp.
// This is useful for creating resources in parallel tests without naming conflicts.
func UniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

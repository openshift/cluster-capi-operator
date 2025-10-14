package e2e

import (
	mapiv1beta1 "github.com/openshift/api/machine/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	// SynchronizedCondition indicates that a machine resource has been successfully synchronized between MAPI and CAPI during migration.
	SynchronizedCondition mapiv1beta1.ConditionType = "Synchronized"
	// MAPIPausedCondition represents the paused state for MAPI machines.
	MAPIPausedCondition mapiv1beta1.ConditionType = "Paused"
	// CAPIPausedCondition represents the paused state for CAPI machines.
	CAPIPausedCondition = clusterv1.PausedV1Beta2Condition
)

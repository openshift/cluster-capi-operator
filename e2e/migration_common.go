package e2e

import (
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	SynchronizedCondition machinev1beta1.ConditionType = "Synchronized"
	MAPIPausedCondition   machinev1beta1.ConditionType = "Paused"
	CAPIPausedCondition                                = clusterv1.PausedV1Beta2Condition

	RoleLabel = "machine.openshift.io/cluster-api-machine-role"
)

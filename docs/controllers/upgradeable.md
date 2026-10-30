# Upgradeable controller

## Overview

[Upgradeable controller](../../pkg/controllers/upgradeable/upgradeable_controller.go) is responsible for assessing whether the cluster is compatible with the Cluster API components of the next minor OpenShift release, before an upgrade is initiated. The result of the assessment is reported in the `Upgradeable` condition of the `cluster-api` ClusterOperator, which the Cluster Version Operator uses as an upgrade precondition.

## Ownership

The upgradeable controller is the sole writer of the `Upgradeable` condition of the `cluster-api` ClusterOperator. The [ClusterOperator controller](clusteroperator.md) retains ownership of the `Available`, `Progressing` and `Degraded` conditions, and aggregates the upgradeable controller's sub-conditions into them like those of any other sub-controller.

# Cluster operator controller

## Overview

The [ClusterOperator controller](../../pkg/controllers/clusteroperator/clusteroperator_controller.go)
runs in the `capi-operator` Deployment and reports the health of Cluster API components through
`ClusterOperator/cluster-api` by aggregating subcontroller conditions.

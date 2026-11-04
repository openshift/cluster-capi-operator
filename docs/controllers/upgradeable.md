# Upgradeable controller

## Overview

[Upgradeable controller](../../pkg/controllers/upgradeable/upgradeable_controller.go) is responsible for assessing whether the cluster is compatible with the Cluster API components of the next minor OpenShift release, before an upgrade is initiated. The result of the assessment is reported in the `Upgradeable` condition of the `cluster-api` ClusterOperator, which the Cluster Version Operator uses as an upgrade precondition.

## Ownership

The upgradeable controller is the sole writer of the `Upgradeable` condition of the `cluster-api` ClusterOperator. The [ClusterOperator controller](clusteroperator.md) retains ownership of the `Available`, `Progressing` and `Degraded` conditions, and aggregates the upgradeable controller's sub-conditions into them like those of any other sub-controller.

The controller runs when `CRDCompatibilityRequirementOperator` is enabled. The ClusterOperator controller relinquishes its previous placeholder `Upgradeable=True` condition through server side apply.

## Assessment

The target is the highest semantic version for the next minor release (`X.(Y+1).0`, including prereleases) in either `ClusterVersion/version` update list. Conditional updates are included even when not recommended. Available updates win version ties. Without a target, the controller removes its condition and assessment resources.

The assessment uses two pods in `openshift-cluster-api-operator`, each running a dedicated binary from the current operator image:

1. `upgradeable-payload-images-extractor` runs `/upgradeable-extract-images`, mounts the target payload as an image volume and copies its provider image ConfigMap data into `upgradeable-payload-images`.
2. `upgradeable-compatibility-requirements` runs `/upgradeable-create-requirements`, mounts those provider images, discovers CRDs across every provider profile, and creates cluster scoped CompatibilityRequirements. The first profile alphabetically wins when CRDs repeat. Requirements cover all served versions and use `Warn`, never `Deny`.

Pod completion marks each step complete. Pods retry transient API failures internally, exit on terminal input or permission errors, and have a 30 minute deadline. The controller recreates failed pods with reconciliation backoff.

Every assessment resource carries current version, target version, and managed labels under `capi-operator.openshift.io`. Cleanup stops stale pods before deleting their outputs. Output reads bypass the informer cache so pod completion cannot race an incomplete cached requirement list.

## Conditions

| State | Upgradeable |
| --- | --- |
| No next minor target | Absent |
| Assessment running or verdicts pending | `Unknown`, `Progressing` |
| API error or failed assessment pod | `Unknown`, `EphemeralError` |
| All fresh requirement verdicts compatible | `True`, `Compatible`, message names target |
| Any fresh requirement verdict incompatible | `False`, `NotCompatible`, message names conflicting CRDs |

All requirements must be admitted and have a current, conclusive compatibility verdict before the controller reports a result. Requirement status changes trigger reassessment, including recovery after CRD remediation without changing the target version.

External edits or deletions of individual requirements are not detected or repaired within a completed assessment. A new assessment recreates the requirement set.

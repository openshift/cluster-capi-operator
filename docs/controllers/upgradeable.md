# Upgradeable controller

## Overview

[Upgradeable controller](../../pkg/controllers/upgradeable/upgradeable_controller.go) is responsible for assessing whether the cluster is compatible with the Cluster API components of the next minor OpenShift release, before an upgrade is initiated. The result of the assessment is reported in the `Upgradeable` condition of the `cluster-api` ClusterOperator, which the Cluster Version Operator uses as an upgrade precondition.

## Ownership

The upgradeable controller is the sole writer of the `Upgradeable` condition of the `cluster-api` ClusterOperator. The [ClusterOperator controller](clusteroperator.md) retains ownership of the `Available`, `Progressing` and `Degraded` conditions, and aggregates the upgradeable controller's sub-conditions into them like those of any other sub-controller.

The controller always runs. The ClusterOperator controller retains ownership of the `Available`, `Progressing` and `Degraded` conditions, never writes `Upgradeable`, and aggregates the upgradeable controller's sub-conditions into them like those of any other sub-controller.

## Assessment

The target is the highest semantic version for the next minor release (`X.(Y+1).0`, including prereleases) in either `ClusterVersion/version` update list. Conditional updates are included even when not recommended. Available updates win version ties. Without a target, the payload is empty: the extractor records no images, the creation pod converges the requirement set to empty, and the controller reports `Upgradeable=True` with reason `NoUpdate`.

The assessment uses two pods in `openshift-cluster-api-operator`, each running a dedicated binary from the current operator image:

1. `upgradeable-payload-images-extractor` runs `/upgradeable-payload-images-extractor`, mounts the target payload as an image volume and copies its provider image ConfigMap data into `upgradeable-payload-images`.
2. `upgradeable-compatibility-requirements` runs `/upgradeable-compatibility-requirements`, mounts those provider images, discovers CRDs across every provider profile, and converges cluster scoped CompatibilityRequirements on them. Image volumes, and then profiles within them, are read in name order. The first definition of a repeated CRD wins. Requirements cover all served versions and use `Warn`, never `Deny`. Provider images that ship no `capi-operator-manifests` directory are skipped: on a multiarch payload a provider that is not built for the current architecture is a placeholder image, exactly as the installer skips it. Assessing zero CRDs is a complete assessment, which reports `Upgradeable=True` with reason `Compatible`.

Pod completion marks each step complete. Pods retry transient API failures internally, exit on terminal input or permission errors, and have a 30 minute deadline. The controller recreates failed pods with reconciliation backoff.

Every assessment resource carries current version, target version, and managed labels under `capi-operator.openshift.io`. Pods have fixed names, so a pod created for different assessed versions is deleted and the new one is only created once that deletion completes.

The payload images ConfigMap is applied in place and never deleted, so its labels always identify the assessment whose images it holds. It holds no images when there is no target. The creation pod owns the requirement set: it deletes requirements whose CRDs the target payload no longer contains and applies the rest, so re-running it repairs labels, ownership and schema drift rather than preserving them. Before applying, it waits for any requirement it will apply to finish terminating, because an apply to a requirement that a previous assessment deleted would succeed and then vanish with its finalizer. The wait is bounded by the pod deadline and fails terminally, naming the requirements that never finalized.

## Conditions

| State | Upgradeable |
| --- | --- |
| No next minor target | `True`, `NoUpdate`, message names the absence of a next minor release |
| Assessment running or verdicts pending | `Unknown`, `Progressing` |
| API error or failed assessment pod | `Unknown`, `EphemeralError`, message includes the failure detail: for a failed pod the failed container's exit code and termination message |
| Complete assessment with no ownership or compatibility conflicts | `True`, `Compatible`, message names target |
| Foreign CRD, or missing or incompatible unmanaged CRD | `False`, `NotCompatible`, message names conflicting CRDs |

All requirements must be admitted and have a current, conclusive compatibility verdict before the controller reports a result. CRDs listed in `ClusterAPI/cluster.spec.unmanagedCustomResourceDefinitions` must exist and satisfy their target requirements. The declaration takes precedence over any remaining installer management label.

For CRDs not declared unmanaged, existing CRDs carrying the installer's `capi-operator.openshift.io/managed-by` label can be updated by the installer during upgrade, and absent CRDs can be created. Neither is blocked by the current CRD failing a target requirement. The same applies to an existing CRD whose target payload manifest declares `capi-operator.openshift.io/adopt-existing: always`: the installer adopts and rewrites it, so it is not a foreign-ownership conflict. The requirements pod carries that annotation from the manifest onto the requirement, because the installer strips it from the objects it applies and a live CRD never carries it. Existing foreign CRDs with neither marker block upgrade until explicitly declared unmanaged and compatible.

Requirement status changes, CRD creation, deletion or label changes, and Cluster API configuration changes trigger reassessment without changing the target version. A requirement update only triggers reassessment once it carries an admitted and current compatibility verdict, because no other status change can alter the published condition. All reads use the informer cache, so a read can lag a write: an assessment can briefly report a verdict before every requirement is visible, and the watch event for that write reconciles again to correct it.

Conflict reports retain the `NotCompatible` condition reason. Each CRD is a dash prefixed list entry with a discrete conflict reason, the CompatibilityRequirement's `Compatible` condition reason and message, and remediation. Multiline checker diagnostics are indented within their CRD's entry.

| Conflict reason | Remediation |
| --- | --- |
| `ForeignCRD` | Explicitly declare external management in `ClusterAPI/cluster.spec.unmanagedCustomResourceDefinitions` and resolve any reported compatibility errors. The declaration cannot be removed once added. |
| `UnmanagedCRDNotFound` | Have the external manager install the missing CRD before upgrading. |
| `UnmanagedCRDIncompatible` | Have the external manager update the CRD to resolve the reported compatibility errors before upgrading. |

For example, the condition message could contain:

```yaml
reason: NotCompatible
message: |-
  Incompatible CRDs:
  - clusters.cluster.x-k8s.io [UnmanagedCRDNotFound]: the CRD is declared unmanaged but is absent from the cluster.
    CompatibilityRequirement: CRDNotFound: The target CRD was not found
    Remediation: Have the external CRD manager install "clusters.cluster.x-k8s.io" before upgrading.
  - machines.cluster.x-k8s.io [UnmanagedCRDIncompatible]: the unmanaged CRD does not satisfy the target requirements.
    CompatibilityRequirement: RequirementsNotMet: served versions removed : v1beta2
    Remediation: Have the external CRD manager update "machines.cluster.x-k8s.io" to resolve the compatibility errors above before upgrading.
```

API lookup failures remain operational errors reported as `Unknown`, `EphemeralError`, rather than compatibility conflicts.

Within a completed assessment, external edits or deletions of individual requirements are not repaired: the creation pod runs once per assessment, and only a new assessment re-applies the requirement set.

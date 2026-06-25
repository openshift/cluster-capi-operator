---
name: credentials-request-audit
description: Use when auditing, reviewing, or refining cloud provider CredentialsRequest permissions in OpenShift. Use when asked to minimize IAM permissions, check for unused permissions, or verify a CredentialsRequest manifest against provider source code. Triggers on mentions of CredentialsRequest, IAM permissions audit, credential refinement, or cloud provider permission minimization.
---

# CredentialsRequest Permission Audit

Audit cloud provider permissions in OpenShift CredentialsRequest manifests by tracing code paths in the provider controller source, filtering for OpenShift's operational context, and validating with runtime evidence.

## Core Principles

**"Code exists" does not mean "code is reachable in OpenShift."** OpenShift uses unmanaged/pre-existing infrastructure (VPCs, networks, LBs). Many upstream CAPI provider code paths are behind early returns or mode checks that make them dead code in OpenShift. Every permission must be validated against the actual OpenShift execution context, not just grep results.

**"Conditional" does not mean "removable."** A permission gated by customer configuration (e.g., KMS encryption key, dedicated host tenancy, custom security groups) is a supported feature — not dead code. Only recommend REMOVE for permissions behind mode checks that are ALWAYS false in OpenShift (e.g., `IsUnmanaged()` early returns), never for permissions behind customer-facing feature toggles. When in doubt, default to KEEP.

**Default to KEEP.** The cost of a false REMOVE (breaking a customer) far exceeds the cost of a false KEEP (slightly broader IAM policy). Only recommend REMOVE when you have high-confidence evidence from BOTH static analysis AND runtime validation covering the full lifecycle. Never produce REMOVE recommendations from static analysis alone — runtime validation is mandatory.

## When to Use

- Auditing a CredentialsRequest manifest for unnecessary permissions
- Refining permissions for a specific cloud provider (AWS, GCP, Azure, or others)
- Responding to a Jira ticket about credential minimization
- Reviewing a PR that changes CredentialsRequest permissions

## Audit Process

### 1. Identify the Provider Source

The vendored code in `cluster-capi-operator` only contains API types, NOT controller logic. You must find the actual controller source:

- **AWS:** `openshift/cluster-api-provider-aws`
- **GCP:** `openshift/cluster-api-provider-gcp`
- **Azure:** `openshift/cluster-api-provider-azure`

For other providers, search for `openshift/cluster-api-provider-<cloud>` or ask the user for the controller repo.

Clone the OpenShift fork (not upstream `sigs.k8s.io`). The OpenShift fork may have patches that change which code paths are active.

### 2. Determine the OpenShift Execution Context

Before tracing any code, establish what mode the provider runs in on OpenShift:

- **VPC/Network mode:** Managed or unmanaged? OpenShift typically uses unmanaged (installer pre-creates infrastructure). This disables entire reconcile functions.
- **Feature gates:** Check active feature gates on the cluster and in the provider's feature gate definitions.
- **What CAPI manages vs what the installer manages:** CAPI manages worker machines. The installer manages control plane, VPCs, LBs, DNS. Permissions for installer-managed resources may be dead code.

This step is critical — skipping it leads to keeping permissions for unreachable code paths.

### 3. Static Analysis — Trace from Controller Entry Points

Trace every SDK/API call from controller reconcile functions through service layers. For each permission in the CredentialsRequest:

1. Find **every** SDK call that requires this permission — not just the first or most obvious one
2. Trace **each** call chain back to its controller entry point
3. Check if **all** code paths are unreachable given the OpenShift execution context (step 2)
4. Only mark as REMOVE candidate if **every** call site is behind an always-false guard

**CRITICAL: The single-gate fallacy.** Finding ONE early return that gates a permission does NOT make it safe to remove. A permission may be exercised through multiple independent code paths across different controllers or reconcile phases. You must verify ALL call sites are unreachable, not just the most obvious one. For example, `reconcileLBAttachment` may gate ELB calls behind `IsControlPlane()`, but another path in the cluster reconciler or a different reconcile phase may also call the same ELB APIs.

**Red flags for unreachable code (must apply to ALL call sites):**
```go
if s.scope.VPC().IsUnmanaged(s.scope.Name()) {
    return nil  // Everything below this is dead code in OpenShift
}
```

### 3a. Classify Each Permission

After tracing, classify each permission into one of three categories:

| Category | Definition | Action |
|---|---|---|
| **Unreachable** | ALL call sites are behind mode checks that are always false in OpenShift (e.g., `IsUnmanaged()` for VPC creation) | REMOVE candidate |
| **Conditional** | Call sites are behind customer-facing configuration (e.g., KMS encryption key, dedicated host tenancy, ELB type) | **KEEP** — this is a supported feature |
| **Active** | Call sites are on the default reconcile path | KEEP |

**A conditional permission is NOT a REMOVE candidate.** If a customer can set a field in the provider's Machine spec (e.g., AWSMachine, GCPMachine, AzureMachine) that triggers the code path, the permission must stay. Only "unreachable" — where OpenShift's architecture makes the code path impossible regardless of customer configuration — qualifies for removal.

### 4. Check for Implicit Permission Requirements

Some permissions are never called directly but are required by the cloud provider. Each provider has its own set — consult the provider's IAM documentation. Known examples:

- **AWS:** `iam:PassRole` is checked by EC2 when `RunInstances` specifies an `IamInstanceProfile`
- **GCP:** `compute.subnetworks.use` is checked by GCP when creating instances in a subnet
- **GCP:** `compute.disks.create` is implicit when `compute.instances.create` includes disk specs
- **Azure:** certain role assignments are checked transitively by ARM during resource creation

Do NOT remove these just because `grep` returns zero direct calls.

Distinguish between **ongoing implicit permissions** (checked by the cloud provider on every API call — e.g., `iam:PassRole` on `RunInstances`) and **one-time precautionary permissions** (only needed for initial account setup — e.g., `iam:CreateServiceLinkedRole`). One-time permissions that are already satisfied on any running cluster should be treated as zero-call-site candidates for removal, not as implicit requirements.

### 5. Runtime Validation (REQUIRED)

**Runtime validation is mandatory.** Static analysis alone is not sufficient to produce audit results. You MUST validate with runtime evidence by enabling SDK-level logging on the provider controller during an E2E test that covers machine create, steady-state reconcile, and delete. If a live cluster is not available, STOP the audit and ask the user for one before continuing. Do not produce a findings table without runtime evidence.

**How to capture:**
1. Edit the provider Deployment args to add `--v=9` (logs SDK HTTP headers including Content-Length)
2. Note: CVO will revert the Deployment args back to `--v=0`. The cluster-capi-operator (not just CVO) reconciles the provider deployment. To keep `--v=9` stable, you must:
   - Add a CVO override for the `cluster-capi-operator` Deployment (not just the provider deployment)
   - Scale down `cluster-capi-operator` to 0 replicas
   - THEN patch the provider deployment with `--v=9`
3. Run the E2E test suite to trigger a full machine lifecycle
4. Capture controller logs: `oc logs -n openshift-cluster-api deployment/<provider> > /tmp/provider-logs.log`

**How to analyze:**
- V=9 logs show HTTP POST requests with `Content-Length` headers but not the `Action=` parameter (that's in the POST body)
- Match Content-Length values to specific API calls based on encoded parameter sizes
- Cross-reference with application-level logs (e.g., "Looking for instance by id", "Terminating EC2 instance") that appear just before the SDK HTTP calls
- V=10 would show full request bodies with `Action=` but may be blocked by CVO reverting the deployment

**CRITICAL: Track runtime coverage explicitly.** Runtime validation only counts for the lifecycle phases you actually captured with verbose logging. You MUST track and report which phases were covered:

| Phase | Captured with --v=9? |
|---|---|
| Machine create | YES / NO |
| Steady-state reconcile | YES / NO |
| Machine delete | YES / NO |

If any phase was NOT captured (e.g., creation happened before --v=9 was stable), you MUST say so in the findings. Never state "runtime confirms static analysis" for phases you did not observe. A permission not seen during delete does not mean it's not called during create.

If runtime validation only partially covers the lifecycle, note which phases are missing and DO NOT classify permissions exercised in uncovered phases. If runtime validation is not possible at all, STOP — do not produce audit results. Ask the user for a live cluster or point to E2E logs from CI that could serve as evidence.

### 6. Check Production Signal and Customer Usage

Before recommending changes:

- **Permission in CR but not in code:** Safe to remove (confirmed by both static analysis and runtime)
- **Permission in code but not in CR:** If no failures have been reported in production, the code path is likely unreachable or implicitly covered. Do NOT recommend adding unless there's evidence of actual failures.
- **Permission in CR and in code but unreachable in OpenShift:** Remove — but document the specific early return or mode check that makes it unreachable.
- **Permission in CR and in code, conditional on customer configuration:** **KEEP.** Check whether the feature is documented, supported, or used in production. Examples:
  - KMS/encryption permissions for customer-managed encryption keys (e.g., AWSMachine.Spec.RootVolume.EncryptionKey, GCPMachine disk encryption)
  - Dedicated host/tenancy permissions (e.g., AWSMachine.Spec.DedicatedHostAllocation)
  - Load balancer registration permissions (e.g., ELB on AWS, internal LB on GCP/Azure)
  - If the provider's Machine/MachineTemplate API (AWSMachine, GCPMachine, AzureMachine, etc.) exposes a field that triggers the code path, it is a supported feature and the permission stays.

**Check production MachineTemplates.** If a live cluster is available, inspect existing MachineTemplates and MachineSets for fields that would exercise conditional permissions (e.g., `hostAffinity`, `encryptionKey`, security group overrides). Do not assume "standard deployments" don't use a feature without checking.

### 7. Check Feature Gate Annotations

The CredentialsRequest manifest has a `release.openshift.io/feature-gate` annotation. Verify:

- Does the annotation include all required feature gates? (e.g., both `ClusterAPIMachineManagement` AND a provider-specific gate like `ClusterAPIMachineManagementAWS`, `ClusterAPIMachineManagementGCP`, etc.)
- Check `vendor/github.com/openshift/api/features/features.go` for defined feature gates
- Check the provider's `featuregates.go` for usage

## Output Format

### Confidence Level

Every audit MUST declare its confidence level upfront based on the evidence available:

| Level | Criteria | What you can recommend |
|---|---|---|
| **High** | Static analysis + runtime validation covering ALL phases (create, steady-state, delete) with --v=9 | REMOVE for unreachable permissions |
| **Medium** | Static analysis + partial runtime (some phases missing) | REMOVE only for permissions with zero call sites in code AND whose implicit triggers are also unreachable. Flag partial coverage explicitly — do not classify permissions exercised in uncovered phases. |
| **Incomplete** | No runtime validation available | **DO NOT produce audit results.** Stop and ask the user for a live cluster or CI E2E logs before continuing. |

**Runtime is not optional.** An audit without runtime validation is not an audit — it is speculation. If a live cluster is not available, do not produce a findings table, summary, or REMOVE recommendations. Instead, report what static analysis you have completed so far and block on the user providing runtime evidence.

### Summary Table

Produce a summary table for ALL permissions:

```markdown
| # | Permission | Category | Action | Reason |
|---|---|---|---|---|
| 1 | `permission:Name` | Active | **KEEP** | Why it's needed, with code reference |
| 2 | `permission:Name` | Unreachable | **REMOVE** | Why it's unreachable, with code evidence |
| 3 | `permission:Name` | Conditional | **KEEP** | Customer feature: field X triggers this path |
```

Then a net change summary:

```markdown
| Category | Count |
|---|---|
| Current permissions | N |
| Active (keep) | A |
| Conditional (keep) | C |
| Unreachable (remove) | R |
| Final total | N-R |
```

### Runtime Coverage Report

If runtime validation was performed, include an explicit coverage report:

```markdown
| Phase | Captured? | Services observed |
|---|---|---|
| Machine create | YES/NO | ec2, elb, ... |
| Steady-state | YES/NO | ec2, ... |
| Machine delete | YES/NO | ec2, ... |
```

For permissions found in code but not in the CR, list them separately with a note on whether action is needed (usually not — see step 6).

## Common Mistakes

| Mistake | Reality |
|---|---|
| Treating all code paths as reachable | OpenShift's unmanaged mode disables large sections of provider code |
| Relying only on grep for SDK calls | Implicit permissions (PassRole, subnetworks.use) won't appear in grep |
| Recommending adding "missing" permissions | If nobody reported failures, the code path is likely dead |
| Ignoring feature gate annotations | The CR annotation must match the required feature gates |
| Using vendored types as controller source | Vendor in cluster-capi-operator has API types only, not controllers |
| Treating static analysis as sufficient | Runtime validation is mandatory. **Without runtime, do not produce audit results — stop and ask for a cluster.** An audit without runtime is not an audit. |
| Assuming upstream CAPI = OpenShift CAPI | OpenShift forks may patch provider behavior |
| **Finding one gate and concluding "dead code"** | A permission may have multiple independent call sites across controllers and reconcile phases. Finding `IsControlPlane()` on one path doesn't mean another path doesn't exist. Verify ALL call sites. |
| **Treating conditional features as removable** | KMS encryption, dedicated hosts, ELB registration — if a customer can configure the provider's Machine spec (AWSMachine, GCPMachine, AzureMachine) to trigger the path, the permission is a supported feature and must stay. "Conditional" ≠ "unreachable". |
| **Claiming "runtime confirms" with partial coverage** | If you only captured delete-phase logs, you cannot confirm anything about the create phase. Track and report exactly which phases you observed. Never extrapolate. |
| **Assuming "standard deployments" don't use a feature** | Check production MachineTemplates before assuming a feature isn't used. Dedicated tenancy, custom KMS keys, and ELB registration are all actively used. |

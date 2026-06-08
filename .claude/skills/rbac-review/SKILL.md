---
name: rbac-review
description: "Use when the user wants to audit, update, or regenerate RBAC permissions for a controller or service account. Covers: adding new permissions after code changes, auditing a service account for least privilege, periodic RBAC reviews, or regenerating RBAC manifests after adding new CRD types or controllers. Trigger on any mention of \"rbac\" in the context of updating or reviewing permissions."
disable-model-invocation: false
---

Audit and regenerate RBAC permissions for a ServiceAccount. Requires an active cluster connection (`oc whoami` must succeed).

The target ServiceAccount is specified via `$ARGUMENTS` (e.g., `/rbac-review openshift-cluster-api:capi-controllers`). If not provided, ask the user.

## Prerequisites

Verify before starting:
```bash
oc whoami
which audit2rbac || echo "audit2rbac not found — install with: go install github.com/liggitt/audit2rbac/cmd/audit2rbac@latest"
```

If `oc whoami` fails, stop and ask the user to connect to a cluster first.

If `audit2rbac` is not found, build from source (go install fails due to replace directives):
```bash
cd /tmp && git clone https://github.com/liggitt/audit2rbac.git audit2rbac-build \
  && cd audit2rbac-build && go build -o ~/go/bin/audit2rbac ./cmd/audit2rbac
```

## Steps 1 & 2: Run in parallel

Step 1 (e2e + audit2rbac) and Step 2 (static analysis) are independent. Start e2e first (it takes ~20 minutes), then do static analysis while it runs. Merge results in Step 3.

### Step 1: Run e2e tests and collect audit2rbac baseline

The cluster must have RBAC that doesn't cause controllers to crash on 403s (so all code paths are exercised).

**Restart the target Deployment before running e2e** so that startup-only operations (pod self-read, initial lease create, ClusterOperator create) appear in the audit window. Without this, those operations are "Code only" in the gap report.

```bash
oc rollout restart deployment/<name> -n <namespace>
oc rollout status deployment/<name> -n <namespace> --timeout=120s
make e2e 2>&1 | tee /tmp/e2e-baseline.log
```

Collect and clean audit logs. `oc adm node-logs` prefixes each line with the node hostname, which breaks audit2rbac's JSON parser — strip it with `sed`:

```bash
oc adm node-logs --role=master --path=kube-apiserver/audit.log \
  | sed 's/^[^ ]* //' > /tmp/audit-clean.log
audit2rbac --filename=/tmp/audit-clean.log \
  --serviceaccount=<namespace>:<name> | tee /tmp/audit2rbac-output.yaml
```

### Step 2: Static analysis of controller code

Find all Deployments that use the target ServiceAccount (search `manifests/` for `serviceAccountName`). From each Deployment, identify its containers and binaries, then find all controllers each binary registers. For each controller, trace resource access:

- `client.Get/List/Create/Update/Patch/Delete` — determines verbs. Note: controller-runtime backs `client.Get` with an informer cache, so any resource accessed via `Get` also needs `list` and `watch`
- `Status().Patch/Update` — requires `/status` subresource rule
- `builder.For/Watches/Owns` in `SetupWithManager()` — requires `get, list, watch`
- `Recorder.Event/Eventf` — requires `events` create/patch
- `ownerReference` with `blockOwnerDeletion: true` — requires `update` on the owner's `/finalizers` subresource (invisible to audit2rbac)
- `ValidatingAdmissionPolicyBinding` with `spec.paramRef` — the API server requires `list` permission on the `spec.paramKind` resource type in the `spec.paramRef.namespace` when creating or updating the binding. This is invisible to both audit2rbac and naive code tracing. Check what `paramKind` each VAP uses and add `list` for those resource types.
- Leader election leases and feature gate informers

For vendored libraries (boxcutter, controller-runtime, etc.), trust audit2rbac over static analysis for verb accuracy. These libraries may use `update` (PUT) internally even when the calling code only shows `patch` calls.

Map each resource to the namespace where it's accessed. Check `pkg/util/platform.go` for the full list of platform-specific infra types.

## Step 3: Gap report

Wait for **both** Step 1 and Step 2 to complete before presenting the gap report. Present it once, not incrementally.

Compare audit2rbac output against static analysis. Produce a table with columns: API Group, Resource, Verbs, Scope, Code evidence, Status.

Status values:
- **Confirmed** — in both audit2rbac and code
- **Code only** — in code but not audit2rbac. Must be justified (different platform, first-install path, `blockOwnerDeletion`, VAP paramRef authorization, etc.)
- **Audit only** — in audit2rbac but not code. Investigate.

Present the gap report and wait for user approval before proceeding.

## Step 4: Build new RBAC

Find the existing RBAC manifests for the target ServiceAccount (search `manifests/` for ClusterRole/Role and ClusterRoleBinding/RoleBinding referencing the SA). Update them following the scoping principle: ClusterRole for cluster-scoped resources, namespace-scoped Roles for everything else.

## Step 5: Verify by running e2e tests

Use `oc replace` (not `oc apply`) to deploy the updated RBAC manifests — `oc apply` on CVO-managed resources does not fully replace rules because the `last-applied-configuration` annotation is missing, so old wildcard rules survive alongside new enumerated rules, giving false confidence.

```bash
oc replace -f <roles-manifest>
oc replace -f <bindings-manifest>
```

For new Roles/RoleBindings that don't exist yet on the cluster, use `oc create` first or `oc replace --force` (which deletes and recreates).

Then restart the Deployments that use the SA, check controller logs for `forbidden`/`403` errors, and run `make e2e`.

## Step 6: Update docs/rbac.md

Update `docs/rbac.md` to reflect the new RBAC structure — which Roles/ClusterRoles exist, their scopes, and what they cover.

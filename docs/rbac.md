# RBAC Management

## Structure

The `capi-controllers` ServiceAccount (used by both the `capi-controllers` and `machine-api-migration` containers) has permissions split across scopes:

| Manifest | Kind | Scope | Purpose |
|----------|------|-------|---------|
| `03_rbac_roles.yaml` | ClusterRole `openshift-capi-controllers` | cluster-wide | Cluster-scoped resources only: infrastructures, clusteroperators, featuregates, clusterversions, nodes, CRDs |
| `03_rbac_roles.yaml` | Role `capi-controllers` | `openshift-cluster-api` | CAPI core + infra provider resources, secrets, events, leases |
| `03_rbac_roles.yaml` | Role `capi-controllers` | `openshift-machine-api` | MAPI machines, machinesets, controlplanemachinesets, secrets, events |
| `03_rbac_roles.yaml` | Role `capi-controllers-kube-system` | `kube-system` | Secrets (vSphere credentials) |
| `03_rbac_roles.yaml` | Role `cluster-capi-operator-pull-secret` | `openshift-config` | Pull-secret read |

The principle: each permission lives in the narrowest scope where it's used. Cluster-scoped Kubernetes objects (nodes, CRDs, clusteroperators) must be in the ClusterRole. Namespaced resources go into a Role in the specific namespace where they're accessed.

## Updating RBAC

Use the `/rbac-review` skill to audit and regenerate RBAC permissions. It requires a live cluster and uses audit2rbac + static code analysis to derive least-privilege rules.

# RBAC Management

## Structure

### capi-operator and capi-installer

The `capi-operator` and `capi-installer` ServiceAccounts (in `openshift-cluster-api-operator`) have separate, scoped permissions:

| Manifest | Kind | Scope | SA | Purpose |
|----------|------|-------|----|---------|
| `0000_30_cluster-api-operator_03_clusterrole.yaml` | ClusterRole `openshift-capi-operator` | cluster-wide | `capi-operator` | config.openshift.io resources, ClusterOperator management, ClusterAPI CR read |
| `0000_30_cluster-api-operator_03_clusterrole.yaml` | Role `capi-operator` | `openshift-cluster-api-operator` | `capi-operator` | Leader election leases, Deployment write (capi-installer), ConfigMap read, pod self-read, events |
| `0000_30_cluster-api-operator_03_clusterrole.yaml` | Role `capi-operator` | `openshift-cluster-api` | `capi-operator` | Deployment read, ConfigMap read |
| `0000_30_cluster-api-operator_03_capi-installer-clusterrole.yaml` | ClusterRole `openshift-capi-installer` | cluster-wide | `capi-installer` | CRDs, admission resources, cluster RBAC, config.openshift.io, ClusterAPI/ClusterOperator status, tracking cache informers |
| `0000_30_cluster-api-operator_03_capi-installer-clusterrole.yaml` | Role `capi-installer` | `openshift-cluster-api-operator` | `capi-installer` | Leader election leases, pod self-read, ConfigMap read, events |
| `0000_30_cluster-api-operator_03_capi-installer-clusterrole.yaml` | Role `capi-installer` | `openshift-cluster-api` | `capi-installer` | boxcutter-managed resources (ConfigMaps, ServiceAccounts, Services, Deployments, Roles, RoleBindings), VAP paramRef list |
| `0000_30_cluster-api-operator_03_capi-installer-clusterrole.yaml` | Role `capi-installer` | `openshift-machine-api` | `capi-installer` | VAP paramRef list (machines, machinesets) |

Both SAs also bind to ClusterRole `system:openshift:openshift-cluster-api:read-tls-configuration` for APIServer TLS profile reading.

### capi-controllers

The `capi-controllers` ServiceAccount (used by both the `capi-controllers` and `machine-api-migration` containers) has permissions split across scopes:

| Manifest | Kind | Scope | Purpose |
|----------|------|-------|---------|
| `0000_30_cluster-api_03_rbac_roles.yaml` | ClusterRole `openshift-capi-controllers` | cluster-wide | Cluster-scoped resources only: infrastructures, clusteroperators, featuregates, clusterversions, nodes, CRDs |
| `0000_30_cluster-api_03_rbac_roles.yaml` | Role `capi-controllers` | `openshift-cluster-api` | CAPI core + infra provider resources, secrets, pod self-read, events, leases |
| `0000_30_cluster-api_03_rbac_roles.yaml` | Role `capi-controllers` | `openshift-machine-api` | MAPI machines, machinesets, controlplanemachinesets, secrets, events |
| `0000_30_cluster-api_03_rbac_roles.yaml` | Role `capi-controllers-kube-system` | `kube-system` | Secrets (vSphere credentials) |
| `0000_30_cluster-api_03_rbac_roles.yaml` | Role `cluster-capi-operator-pull-secret` | `openshift-config` | Pull-secret read |

This SA also binds to ClusterRole `system:openshift:openshift-cluster-api:read-tls-configuration` for APIServer TLS profile reading.

## Principles

- Each permission lives in the narrowest scope where it's used
- Cluster-scoped Kubernetes objects (CRDs, ClusterRoles, ClusterOperators) go in ClusterRoles
- Namespaced resources go into Roles in the specific namespace where they're accessed
- Read-only informer permissions may use ClusterRoles when the cache watches multiple namespaces

## Updating RBAC

Use the `/rbac-review` skill to audit and regenerate RBAC permissions. It requires a live cluster and uses audit2rbac + static code analysis to derive least-privilege rules.

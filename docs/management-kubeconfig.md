# Management kubeconfig Secret

The Cluster CAPI Operator creates a management-cluster kubeconfig Secret
named `<InfrastructureName>-kubeconfig` in the `openshift-cluster-api`
namespace.

The Secret exists to satisfy the standard Cluster API kubeconfig contract.
Cluster API components use this Secret when creating a client for the Cluster
resource representing the current OpenShift cluster. In this deployment, the
management cluster and workload cluster are the same cluster.

This is not a portable or workstation-usable kubeconfig. It contains only
static in-cluster connection information:

```yaml
server: https://kubernetes.default.svc:443
certificate-authority: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
```

The kubeconfig does not contain an embedded bearer token or embedded CA data.
Instead, its `tokenFile` and `certificate-authority` fields point to files in
the consumer Pod's projected ServiceAccount volume. Kubernetes mounts the CA
certificate and the token for that Pod, and the Kubernetes client reads those
files when authenticating. Consequently, the Secret contains no reusable
credential material; access is determined by the consumer's ServiceAccount and
its RBAC:

- Each consumer authenticates as its own ServiceAccount.
- Each consumer requires the runtime RBAC needed for its operations.
- Kubernetes rotates the projected ServiceAccount token automatically.
- The Secret does not need to be rewritten for token rotation.

## Self-managed cluster access

Cluster API's ClusterCache detects when the CAPI controller is running on the
same cluster represented by the Cluster resource. In that case it switches to
the controller's in-cluster API endpoint and ServiceAccount identity. The
kubeconfig Secret remains required as the standard Cluster API connection
contract and as the initial source of the connection configuration.

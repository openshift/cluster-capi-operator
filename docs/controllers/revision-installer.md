# Revision installer

## Overview

The revision controller renders the resources for an installed CAPI revision. The [revision installer reconciler](../../pkg/controllers/installer/revision_reconciler.go) applies those resources and is the sole owner of the management kubeconfig Secret named `<InfrastructureName>-kubeconfig` in `openshift-cluster-api`.

## Management kubeconfig

The Secret is intended only for CAPI and infrastructure-provider Pods running in the cluster. It is not a portable or workstation-usable kubeconfig. Its credentials are projected ServiceAccount files rather than an embedded bearer token:

```yaml
server: https://kubernetes.default.svc:443
certificate-authority: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
```

The paths resolve inside each consumer Pod. Consequently, each consumer authenticates as its own ServiceAccount and must have the runtime RBAC required by the client. Kubernetes automatically rotates the projected token file; no Secret rewrite or legacy token-rotation controller is required.

## Ownership and repair

During revision reconciliation, the installer adopts the expected Secret when necessary and updates it when the generated profile changes. It also repairs drift or a stale legacy form. No separate kubeconfig controller or shared `capi-controllers-token` is used. Installer RBAC is limited to managing the generated Secret and is separate from the broader permissions granted to runtime consumers.

# Cluster controller

## Overview

Cluster controller is responsible managing Cluster and InfrastructureCluster(AWSCluster, etc.) CRs. Both objects will
represent the current cluster where operator is running because we treat this cluster as both management and workload.

## Behavior

```mermaid
stateDiagram-v2
    [*] --> IsCurrentPlatformSupported
    state IsCurrentPlatformSupported <<choice>>
    IsCurrentPlatformSupported --> NoOp: False
    IsCurrentPlatformSupported --> CreateOrUpdateInfrastructureCluster: True
    CreateOrUpdateInfrastructureCluster --> SetInfrastructureClusterSpec
    SetInfrastructureClusterSpec --> SetInfrastructureClusterStatusReady
    SetInfrastructureClusterStatusReady --> CreateOrUpdateCoreCluster
    CreateOrUpdateCoreCluster --> SetCoreClusterInfrastructureRef
    SetCoreClusterInfrastructureRef --> [*]
    NoOp --> [*]
```

If the current platform is not supported, the controller will not create any cluster objects and allow 
"bring your own" scenarios. In cases when the platform is supported, the controller will create the 
appropriate infrastructure cluster object. It will set the cluster externally managed annotation
`"cluster.x-k8s.io/managed-by"` and `Status.Ready` to `true` which indicates that the cluster is managed 
by the urrent controller and not by the CAPI infrastructure provider. It will also set the cluster spec
to contain appropriate values like region, etc. if required.

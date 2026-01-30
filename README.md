# Cluster CAPI Operator

The Cluster CAPI Operator manages the installation and lifecycle of the Cluster API Components on Openshift clusters.

```
Note: This operator only runs on TechPreview clusters.
```

# Managed resources

- [CoreProvider](https://github.com/kubernetes-sigs/cluster-api-operator/blob/main/api/v1alpha2/coreprovider_types.go) - an object that represents core Cluster API and is later reconciled by the upstream operator.
- [InfrastructureProvider](https://github.com/kubernetes-sigs/cluster-api-operator/blob/main/api/v1alpha2/infrastructureprovider_types.go) - an object that represents Cluster API infrastructure provider(AWS, GCP, Azure, etc.) 
and is later reconciled by the upstream operator.
- [Cluster](https://cluster-api.sigs.k8s.io/developer/architecture/controllers/cluster.html) - CAPI Cluster CR that
represents current cluster, it is treated as management and workload cluster at the same time.
- [InfrastructureCluster](https://cluster-api.sigs.k8s.io/developer/providers/cluster-infrastructure.html) - CAPI Infrastructure Cluster CR that represents the infrastructure cluster.
- Worker userdata secret - a secret that contains ignition configuration to be used by the worker nodes.
- Kubeconfig secret - a secret that contains kubeconfig for the cluster.

## Controllers

Controllers design can be found here:
- [ClusterOperator Controller](docs/controllers/clusteroperator.md)
- [Core cluster Controller](docs/controllers/core-cluster.md)
- [Infra cluster Controller](docs/controllers/infra-cluster.md)
- [Secret sync Controller](docs/controllers/secretsync.md)
- [Kubeconfig Controller](docs/controllers/kubeconfig.md)

## New infrastructure provider onboarding

Steps for infrastructure provider onboarding are documented [here](docs/provideronboarding.md).

## Running operator locally

Downscale cluster version operator deployment;

```sh
kubectl scale deployment cluster-version-operator -nopenshift-cluster-version --replicas=0
```

Downscale capi-operator deployment:

```sh
kubectl scale deployment capi-operator -n openshift-cluster-api-operator --replicas=0
```

Downscale capi-controllers deployment:

```sh
kubectl scale deployment capi-controllers -n openshift-cluster-api --replicas=0
```

Compile and run operator:

```sh
make build && ./bin/capi-operator
```

Or run the controllers:

```sh
make build && ./bin/capi-controllers
```

## Unit tests

```sh
make test
```

### Enabling technical preview featureset

```sh
kubectl edit featuregate
```

Set the spec to the following

```yaml
spec:
  featureSet: TechPreviewNoUpgrade
```

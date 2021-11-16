# Cluster CAPI Operator

The Cluster CAPI Operator manages the installation of the CAPI Operator and the provider
resources that it requires.

## Controllers

- ClusterOperator Controller

When the featuregate is DevPreviewNoUpgrade
1. Install the CAPI Operator
2. Install all the supported provider configmaps
3. Install the CoreProvider and InfractureProvider CRs (with image overrides)

## Updating manifests and assets

- Import capi-operator and provider manifests:

  ```sh
  $ make import-assets
  ```

This command does 2 main things:
1. get capi-operator configuration and moves the rbac resources to /manifests/ whilst
   placing the remainder in /assets/capi-operator/
   It also replaces the default rbac with a smaller subset.
2. use clusterctl to get the provider resources
   a. convert from cert-manager to service-ca
   b. place provider rbac resources in /manifests
   c. place all other resources in /assets/providers as configmaps (to be consumed by capi-operator)

To update the version of a provider, edit hack/import-assets/providers.go and bump
the versions as required.

# Provider onboarding

In order to onboard a new CAPI provider, the following steps are required.

## Set up provider repository and builds

- Create a provider fork in OpenShift github organization. Provider fork for reference - https://github.com/openshift/cluster-api-provider-azure/
- Remove all upstream OWNERS and replace with downstream OWNERS.
- Create vendor directory in provider repository.
- Create an `openshift/` directory in the provider repository and make sure it includes:
  - A [script]((https://github.com/openshift/cluster-api-provider-azure/blob/master/openshift/unit-tests.sh)) for running unit tests, it's required because of issue with $HOME in CI container.
  - `Dockerfile.openshift` this Dockerfile will be used for downstream builds. Provider controller binary must be called
  `cluster-api-provider-$providername-controller-manager` and be located in `/bin/` directory. [Example Dockerfile](https://github.com/openshift/cluster-api-provider-azure/blob/master/openshift/Dockerfile.openshift).

After provider fork is set up, you should onboard it to [Openshift CI](https://docs.ci.openshift.org/docs/how-tos/onboarding-a-new-component/) and make appropriate ART requests for downstream builds.

## Add provider metadata to your provider

See [Provider Contract](provider-contract.md)

## Add a reference to your provider image to CAPI operator

* The provider image MUST be included in the OpenShift release payload.
* Add an entry for the provider image in `/manifests/image-references`.
* Add an entry for the provider image to the `capi-installer-images` ConfigMap in `/manifests`.

## Add infrastructure cluster to the cluster controller

Cluster API requires an infrastructure cluster object to be present. We are using [externally managed infrastructure](https://github.com/kubernetes-sigs/cluster-api/blob/main/docs/proposals/20210203-externally-managed-cluster-infrastructure.md)
feature to manage all the infrastructure clusters on Openshift. It means that
the cluster must have externally managed annotation `"cluster.x-k8s.io/managed-by"`(clusterv1.ManagedByAnnotation)
and `Status.Ready=true` to indicate that cluster object is managed by this controller and not by the CAPI infrastructure provider.
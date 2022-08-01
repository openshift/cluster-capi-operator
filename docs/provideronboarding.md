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

## Add provider assets to the operator

- Add your provider to `provider-list.yaml` located in root of the operator. For example:
  ```
  - name: aws
  type: InfrastructureProvider
  branch: release-4.11 # Openshift release branch to be used
  version: v1.3.0 # Version of the provider in your fork
  ```
- Run `make assets`
- Include your provider image to `manifests/image-references` and `manifests/0000_30_cluster-api_capi-operator_01_images.configmap.yaml`

At this point your provider will have CRDs and RBAC resources automatically imported to the `manifests/` directory and
managed by the CVO, all other resources will be imported to the `assets` directory and managed by the upstream operator.

If you wish to make development of your provider easier, you can include a public provider image to the `dev-images.json`.

## Add infrastructure cluster to the cluster controller

Cluster API requires an infrastructure cluster object to be present. We are using [externally managed infrastructure](https://github.com/kubernetes-sigs/cluster-api/blob/main/docs/proposals/20210203-externally-managed-cluster-infrastructure.md)
feature to manage all the infrastructure clusters on Openshift. It means that 
the cluster must have externally managed annotation `"cluster.x-k8s.io/managed-by"`(clusterv1.ManagedByAnnotation)
and `Status.Ready=true` to indicate that cluster object is managed by this controller and not by the 
CAPI infrastructure provider.

In order to add a new infrastructure cluster to the cluster controller, you need setup the reconciler in `main.go`
like this:

```golang
func setupInfraClusterReconciler(mgr manager.Manager, platform configv1.PlatformType) {
	switch platform {
  ...
	case configv1.YourPlatformType:
		if err := (&cluster.GenericInfraClusterReconciler{
			ClusterOperatorStatusClient: getClusterOperatorStatusClient(mgr, "cluster-capi-operator-infra-cluster-resource-controller"),
			InfraCluster:                &platformv1.YourPlatformCluster{},
		}).SetupWithManager(mgr); err != nil {
			klog.Error(err, "unable to create controller", "controller", "YourPlatformCluster")
			os.Exit(1)
		}
  ...
	}
}
```

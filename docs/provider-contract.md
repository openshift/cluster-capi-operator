## Provider Contract

### Invoking manifests-gen

`manifests-gen` is typically invoked in a provider repo from a `ocp-manifests` target in `openshift/Makefile`.
For example, the invocation for cluster-api-provider-aws might look like this:
```make
.PHONY: ocp-manifests
ocp-manifests: | $(RELEASE_DIR) ## Builds openshift specific manifests
        # Generate provider manifests.
        cd tools && $(MANIFESTS_GEN) --manifests-path "../capi-operator-manifests" --kustomize-dir="../../openshift" \
                --name cluster-api-provider-aws \
                --attribute type=infrastructure \
                --attribute version="${PROVIDER_VERSION}" \
                --self-image-ref registry.ci.openshift.org/openshift:aws-cluster-api-controllers \
                --platform AWS \
                --protect-cluster-resource awscluster
```

`manifests-path` specifies the directory where the manifests should be written.

`kustomize-dir` is a directory relative to the process's current working directory containing a `kustomization.yaml` which will be used to generate the manifests.
The form of this `kustomization.yaml` is expected to be common across all providers.
An example of the common parts is given below.

`name` and `platform` are written as metadata. This metadata is used by the installer to determine which manifests to install.

`attribute`s are purely informational, and do not affect the function of CAPI operator.
In the future they may be used to enhance discoverability of what is being installed, e.g. including a component version in a user-visible revision phase name, but they will never affect behaviour.

`self-image-ref` is the image reference of the provider image as referenced in the generated manifests.
The installer will substitute this reference with the actual release image during installation.
This must use the `registry.ci.openshift.org` registry.
`manifests-gen` will return an error if it detects any image reference which does not use `registry.ci.openshift.org`.

`protect-cluster-resource` indicates the name of the infrastructure cluster resource type which the CAPI operator will create for this provider.
`manifests-gen` will generate a VAP for this resource to ensure that it cannot be modified.

Each provider is expected to define a `kustomization.yaml` with a form similar to:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

components:
- tools/vendor/github.com/openshift/cluster-capi-operator/manifests-gen

resources:
- ../config/default

images:
- name: gcr.io/k8s-staging-cluster-api-aws/cluster-api-aws-controller
  newName: registry.ci.openshift.org/openshift
  newTag: aws-cluster-api-controllers
```

It must include the kustomize `Component` provided in the `manifests-gen` go module.
If the `tools` module uses vendoring, this can be included directly as shown above.
If the `tools` module does not use vendoring, this will have to be dynamically substituted with the location of the `manifests-gen` go module using an appropriate invocation of `go list`.

Assuming the upstream provider uses the typical `kubebuilder` scaffolding, it should include `config/default` from the upstream repo as the base resource.

Whatever value the upstream manifests use as the default image reference should be substituted with a new image reference in `registry.ci.openshift.org`.

If a provider requires any provider-specific modifications to the upstream manifests, they should also be included in this `kustomization.yaml`.
The standard modifications made by `manifests-gen` are detailed below.

### Standard modifications made by manifests-gen

`manifests-gen` makes the following set of modifications to provider manifests automatically.

* Set the namespace of all namespaced objects to `openshift-cluster-api`
* Replace cert-manager with OpenShift Service CA:
  Upstream CAPI providers typically include `cert-manager` metadata and manifests for webhooks.
  `manifests-gen` will automatically drop `cert-manager.io` resources and rewrite cert-manager annotations
  on webhook configurations and CRDs to use OpenShift Service CA instead.
* Exclude `Namespace` and `Secret` objects from the manifests: we expect these to be created by other means.
* The following set of changes to all Deployments:
  * Set the following annotations:
    * `target.workload.openshift.io/management: {"effect": "PreferredDuringScheduling"}`.
    * `openshift.io/required-scc: "restricted-v2"`
  * Set resource requests of all containers to `cpu: 10m` and `memory: 50Mi`.
  * Remove resource limits from all containers.
  * Set the terminationMessagePolicy of all containers to `FallbackToLogsOnError`.
  * Set priorityClassName of all pods to `system-cluster-critical`
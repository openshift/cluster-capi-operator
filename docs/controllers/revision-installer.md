# Revision installer

The revision installer reconciles the resources rendered for an installed CAPI
revision. It applies the revision's managed resources, tracks their readiness,
and repairs or removes them as revisions change.

## Architecture requirements

The control plane must be homogeneous, and its architecture must be supported by
the target Cluster API infrastructure provider. Worker nodes may use other
architectures.

The generated `capi-installer` Deployment is restricted to control-plane nodes.
Image volumes resolve to the architecture of the node on which the pod runs.
Some provider images contain only a placeholder on unsupported architectures,
without Cluster API manifests. Running the installer on such a node could omit a
provider from the desired revision and cause removal or loss of management of
existing provider resources.

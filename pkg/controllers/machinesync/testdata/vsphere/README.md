# CAPV admission CRD fixtures

Unmodified upstream CAPV v1.16.1 CRDs, matching the Go module dependency:

- https://raw.githubusercontent.com/kubernetes-sigs/cluster-api-provider-vsphere/v1.16.1/config/default/crd/bases/infrastructure.cluster.x-k8s.io_vspheremachines.yaml
- https://raw.githubusercontent.com/kubernetes-sigs/cluster-api-provider-vsphere/v1.16.1/config/default/crd/bases/infrastructure.cluster.x-k8s.io_vspheremachinetemplates.yaml

Both serve v1beta1 and v1beta2. Base CRDs have no conversion webhook configuration;
these tests exercise API-server admission version matching, not CAPV webhook
conversion or defaulting. Refresh both fixtures when updating CAPV dependency.

Schemaless tests separately isolate CEL CREATE/UPDATE behavior. Real-CRD tests
check schema-compatible CREATE denials attributable to generated policy and
unchanged UPDATE acceptance. Schema/immutability rejection is not CEL evidence.

# OpenShift Tests Extension (OTE)

This directory contains a separate Go module that builds the OTE binary
(`cluster-capi-operator-tests-ext`). The binary is embedded gzipped in the
operator image at `/usr/bin/cluster-capi-operator-tests-ext.gz` and used by
`openshift-tests` to discover and run e2e tests as part of the OpenShift test
infrastructure.

## Adding a test to OTE

Whether a test file is visible to OTE depends entirely on its file suffix:

| File | `make e2e` | OTE (`list tests`) |
|---|---|---|
| `e2e/my_test.go` | ✅ | ❌ |
| `e2e/my.go` | ✅ | ✅ |
| `e2e/my.go` + `//go:build !e2e` | ❌ | ✅ |

**To make a test discoverable by OTE:** write it in a regular `.go` file (not
`_test.go`). No changes to the extension binary are needed — ginkgo
auto-discovery picks it up on the next build.

**To keep a test out of OTE:** use the standard `_test.go` suffix. It still
runs via `make e2e`.

**To make a test run via OTE only (not `make e2e`):** use a regular `.go` file
with `//go:build !e2e`. `make e2e` passes `--tags=e2e` internally which causes
`!e2e` files to be excluded from compilation. The OTE binary is built without
that tag, so the file is included.

## Test labels and suite routing

Labels on a test control which OTE suite it lands in, and therefore which
nightly conformance job picks it up via the `Parents` field:

| Label on test | OTE suite | Parent suite |
|---|---|---|
| none (default) | `capio/parallel` | `openshift/conformance/parallel` |
| `Label("Serial")` | `capio/serial` | `openshift/conformance/serial` |
| `Label("Disruptive")` | `capio/disruptive` | `openshift/disruptive-longrunning` |

A test is included in a periodic run when **both** of these are true:
1. The cluster has the feature set that enables the test's feature gate (e.g. `TechPreviewNoUpgrade`)
2. The job runs the matching parent suite (e.g. `openshift/conformance/serial`)

Example — a serial test that lands in `capio/serial → openshift/conformance/serial`:

```go
// e2e/aws_machineset.go  (regular .go, not _test.go — visible to OTE)
var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIMachineManagementAWS] Cluster API AWS", func() {
    It("should scale a MachineSet", Label("Serial"), func() {
        // test body
    })
})
```

Example — a parallel test (no label) that lands in `capio/parallel → openshift/conformance/parallel`:

```go
var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIMachineManagementAWS] Cluster API AWS", func() {
    It("should have a running cluster", func() {
        // test body
    })
})
```

## Feature gate labeling

Every e2e test must carry the appropriate `[OCPFeatureGate:FeatureName]` tag in
its `Describe` block name. This tells `openshift-tests` to skip the test
automatically on clusters where the feature gate is not enabled:

```go
var _ = Describe("[sig-cluster-lifecycle][OCPFeatureGate:ClusterAPIMachineManagementAWS] Cluster API AWS", func() {
    It("should run a machine", func() { ... })
})
```

Without this tag, a test runs on every cluster regardless of feature gate
status, which will cause failures on clusters where the feature is not active.

## Blocking vs informing lifecycle

By default every test is `blocking` — a failure causes the suite exit code to
be non-zero. To mark a test as `informing` (failure recorded but non-blocking):

```go
import g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

It("should be stable but not yet required", g.Informing(), func() { ... })
```

Informing failures appear in the JSON output and Sippy dashboards but do not
gate merges or promotions. Use this temporarily to gather stability data before
promoting a test to blocking. Tests must not remain informing indefinitely.

**Important:** `g.Informing()` is only honored when the test runs through the
OTE binary (`run-suite`, `run-test`). When running via `make e2e`, ginkgo has
no concept of informing lifecycle — a failing informing test will still cause
the `make e2e` run to exit with a non-zero code.


## Running locally

`list tests` and `list suites` work without a cluster. `run-suite` and
`run-test` require a reachable OCP cluster because `InitCommonVariables()`
fetches the `cluster` Infrastructure object.

```bash
# Build the binary
make bin/cluster-capi-operator-tests-ext

# List all discovered tests (no cluster needed)
./bin/cluster-capi-operator-tests-ext list tests

# Run a specific suite against a cluster
./bin/cluster-capi-operator-tests-ext run-suite capio/serial

# Run a single test by exact name
./bin/cluster-capi-operator-tests-ext run-test "[sig-cluster-lifecycle]... test name"
```

The `KUBECONFIG` env var is read directly by `config.GetConfig()` — it must
point to a reachable OCP cluster, not just whatever `kubectl` has as
current-context.

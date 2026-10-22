# OCPCLOUD-3721 implementation

- [x] Review Jira scope, API graduation PR, and existing consumers.
- [x] Rebase onto upstream main at `e978f8ae355d09e8b0757a597c6c6457b35bfe71`, including merged PR #686, while preserving the v1 migration.
- [x] Pin openshift/api to graduation PR head through a temporary fork replacement.
- [x] Migrate ClusterAPI consumers, schemes, manifests, and test CRD source to v1.
- [x] Regenerate dependencies and vendor tree with `make vendor`.
- [x] Pass formatting, lint, full build, and focused unit tests on Kubernetes 1.37.

## Implementation

The upstream API retains both served versions; removing v1alpha1 upstream is out of scope.
PR #3117 is still open. The replacement pins `simkam/openshift-api` at
`75fb5ad945ed7a821214933db8294c2a562a0b1d`. Before merging, replace this with the
merged openshift/api version and regenerate dependencies.

The current openshift/client-go does not have v1 ClusterAPI apply configurations.
The two controllers now construct partial v1 status apply patches directly,
retaining their existing field managers, force ownership, and UID precondition.
The JSON round-trip conversion helper and its now-obsolete tests were removed;
new tests check both controllers' v1 status payloads and field isolation.

The newer API also renamed the infrastructure test CRD profile; envtest now uses
the available TechPreviewNoUpgrade CRD.

## Prerequisite baseline

The branch is based on upstream main at
`e978f8ae355d09e8b0757a597c6c6457b35bfe71` (merged PR #686). Kubernetes 1.37
prerequisites are inherited from main, not duplicated in the migration diff.
The uncommitted migration was preserved through a safety stash and reapplied;
lockfile and vendor conflicts were resolved by regenerating dependencies.

After openshift/api #3117 merges, replace the temporary fork replacement with
the merged upstream version.

## Updated API PR head

The refreshed API nests v1 component source fields under `source`, rather than
embedding them inline. Component construction, profile matching, provider image
extraction, and test fixtures now use `Source`. The status-patch regression test
also asserts the nested JSON representation. The refreshed schema bounds revision
numbers below 2^53.

Before merging, update openshift/client-go to its regenerated v1 apply
configurations and replace the temporary handwritten status payloads. Remove this
implementation-plan document as well.

## Verification against updated API PR head

- `make vendor`: PASS.
- `make fmt`: PASS (0 issues).
- `make lint`: PASS (0 issues).
- `make build`: PASS, including the tests-extension and manifests-gen binaries.
- `make unit TEST_DIRS='./pkg/controllers/installer/... ./pkg/controllers/installerdeployment/... ./pkg/controllers/revision/... ./pkg/revisiongenerator/... ./pkg/providerimages/...' GINKGO_EXTRA_ARGS='--procs=2'`: PASS (all five suites, using Kubernetes 1.37.1 envtest assets).
- `git diff --check -- ':!vendor'`: PASS.
- E2e cluster tests were not run (no cluster configured).

Logs for the updated API pin are in `/tmp/3721-api-refresh-{vendor,fmt,lint,build,unit}.log`.
The pre-rebase safety stash `ad93e411a835cb15e0fb7f686a67bce2c7295668` is retained;
its changes have been restored to the working tree. No changes are staged.

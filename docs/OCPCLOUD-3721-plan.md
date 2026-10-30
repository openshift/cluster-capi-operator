# OCPCLOUD-3721 implementation

- [x] Review Jira scope, API graduation PR, and existing consumers.
- [x] Rebase onto upstream main at `0dfdd384ad91cff11a2b5e6c8ccabe262fd5b5be`, preserving the v1 migration.
- [x] Pin openshift/api to graduation PR head through a temporary fork replacement.
- [x] Migrate ClusterAPI consumers, schemes, manifests, and test CRD source to v1.
- [x] Regenerate dependencies and vendor tree with `make vendor`.
- [x] Pass formatting, lint, full build, and focused unit tests on Kubernetes 1.37.

## Implementation

The upstream API retains both served versions; removing v1alpha1 upstream is out of scope.
PR #3117 is still open. The replacement pins `simkam/openshift-api` at
`eb73c6a14b60ad08481bb1325a9f9bc95b68f235`. Before merging, replace this with the
merged openshift/api version and regenerate dependencies.

The current openshift/client-go does not have v1 ClusterAPI apply configurations.
The two controllers now construct partial v1 status apply patches directly,
retaining their existing field managers, force ownership, and UID precondition.
The JSON round-trip conversion helper and its now-obsolete tests were removed;
new tests check both controllers' v1 status payloads and field isolation.

The newer API also renamed the infrastructure test CRD profile; envtest now uses
the available TechPreviewNoUpgrade CRD.

## Prerequisite baseline

The branch is rebased onto upstream main at
`0dfdd384ad91cff11a2b5e6c8ccabe262fd5b5be`, preserving the CompatibilityRequirement
v1 migration, single-test CRD handoff fixes, ClusterAPIOperator manifest gating,
installer watch permissions, and Upgradeable controller scaffolding. Kubernetes
1.37 prerequisites are inherited from main, not duplicated in the migration diff.

The refreshed API uses the `ClusterAPIOperator` gate for the ClusterAPI CRD.
The singleton manifest matches that gate, and envtest loads the SelfManagedHA
CRD, which serves both v1 and v1alpha1 with v1 as storage.
Handwritten envtest revisions use valid revision names, content digests, and
component names to satisfy the refreshed v1 validation rules.

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

## Verification after the rebase (API head b0bee5f7b0d1)

- `make vendor`: PASS.
- `make fmt`: PASS (0 issues).
- `make lint`: PASS (0 issues).
- `make build`: PASS, including the tests-extension and manifests-gen binaries.
- `make unit TEST_DIRS='./pkg/controllers/installer/... ./pkg/controllers/installerdeployment/... ./pkg/controllers/revision/... ./pkg/revisiongenerator/... ./pkg/providerimages/...' GINKGO_EXTRA_ARGS='--procs=2'`: PASS (all five suites, using Kubernetes 1.37.1 envtest assets).
- `git diff --check -- ':!vendor'`: PASS.
- E2e cluster tests were not run (no cluster configured).

Logs for that API pin are in `/tmp/pr691-{vendor,fmt,lint,build,unit}.log`.

## Comment-only API refresh (993ea7775bd2)

Updated the fork pin and regenerated vendor to correct CustomResourceDefinition
terminology in the API comments and generated descriptions. No functional changes.

- `make vendor`, `make fmt`, `make lint`, and `make build`: PASS.
- `git diff --check`: PASS.
- Unit tests were not rerun for this comment-only update.

Logs: `/tmp/pr691-comment-{vendor,fmt,lint,build}.log`.

## Rebase verification (upstream 0dfdd384a, API eb73c6a14b60)

- `make vendor`, `make fmt`, `make lint`, and `make build`: PASS.
- Focused unit tests for installer, installerdeployment, revision, clusteroperator,
  revisiongenerator, and providerimages: PASS (six suites, `--procs=2`).
- `git diff --check -- ':!vendor'`: PASS.
- E2e cluster tests were not run.

Logs: `/tmp/pr691-rebase-{vendor,fmt,lint,build,unit}.log`.

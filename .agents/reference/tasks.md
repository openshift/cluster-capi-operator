# Tasks

## Essential Commands
```bash
# Build and test
make build          # Build all binaries
make test           # Run verification (fmt + lint) then unit tests
make unit           # Run unit tests with coverage
make verify         # Run fmt, lint, and verify-ocp-manifests
make lint           # Run linting (golangci-lint)
make fmt            # Format code (golangci-lint --fix)
make vendor         # Vendor dependencies
make ocp-manifests  # Generate admission policy profiles
```

## Running Tests

**Do not use `go test` or `ginkgo` directly.** Tests use `envtest` which requires `KUBEBUILDER_ASSETS`
to point at downloaded API server and etcd binaries. The Makefile handles this: `make unit` depends on
the `.localtestenv` target (which runs `setup-envtest` to download binaries and writes their path to
`.localtestenv`), and `hack/test.sh` sources that file before invoking ginkgo. Running `go test`
directly will fail because the envtest `Environment` cannot locate the binaries.

```bash
make unit                                                # All unit tests
make unit TEST_DIRS="./pkg/controllers/installer/..."    # Specific package
make unit TEST_DIRS="./pkg/controllers/machinesync/..."  # Another specific package
```

**Important:** Ginkgo functional tests are slow and produce verbose output that will exceed
context limits. Always redirect output to a log file and use multi-pass processing:
```bash
make unit TEST_DIRS="./pkg/..." 2>&1 | tee /tmp/test-output.log
# Then check results:
tail -20 /tmp/test-output.log        # Summary
grep -E 'FAIL|PASSED' /tmp/test-output.log  # Pass/fail status
grep 'FAIL' /tmp/test-output.log     # Find failures
```

### Default ginkgo arguments
The default ginkgo args in `hack/test.sh` are:
- `-r -v -p --randomize-all --randomize-suites --keep-going --race --trace --timeout=${TIMEOUT}`
- The timeout defaults to `20m` for unit tests (set by the Makefile) and `120m` for e2e tests.
- In CI (`OPENSHIFT_CI=true`), `-p` is replaced with `--procs=4`.

Prefer using `GINKGO_EXTRA_ARGS` to pass additional arguments to ginkgo. Use `GINKGO_ARGS` when you need to override the default values entirely.

### Focused Testing
```go
// Focus specific tests (REMOVE before committing!)
FIt("test name", func() { /* test */ })
FContext("context name", func() { /* tests */ })
```

### Test Environment
- Each controller has a `suite_test.go` that bootstraps an `envtest.Environment`
- See "Running Tests" above for why `make unit` is required

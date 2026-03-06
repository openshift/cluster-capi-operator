# Running OTE Tests

## Prerequisites

```bash
# 1. Connect to an OpenShift cluster
export KUBECONFIG=/path/to/your/kubeconfig
oc whoami

# 2. Build the extension binary
make extension
```

## Running Tests

### 1. List all available tests

```bash
# List all tests without running them
./bin/extension --ginkgo.dry-run --ginkgo.v

# Count the number of tests
./bin/extension --ginkgo.dry-run | grep "Will run"
```

### 2. Run all tests

```bash
# Run all e2e tests
./bin/extension

# Using Makefile (equivalent)
make e2e
```

### 3. Run platform-specific tests

```bash
# Run only AWS tests
./bin/extension --ginkgo.focus="AWS"

# Run only GCP tests
./bin/extension --ginkgo.focus="GCP"

# Run only Azure tests
./bin/extension --ginkgo.focus="Azure"

# Run only vSphere tests
./bin/extension --ginkgo.focus="vSphere"

# Run only Baremetal tests
./bin/extension --ginkgo.focus="Baremetal"
```

### 4. Run migration-related tests

```bash
# Run all Machine Migration tests
./bin/extension --ginkgo.focus="Machine Migration"

# Run all MachineSet Migration tests
./bin/extension --ginkgo.focus="MachineSet Migration"

# Run MAPI Authoritative tests
./bin/extension --ginkgo.focus="MAPI Authoritative"

# Run CAPI Authoritative tests
./bin/extension --ginkgo.focus="CAPI Authoritative"

# Run VAP (Validation Admission Policy) tests
./bin/extension --ginkgo.focus="VAP"
```

### 5. Filter tests with regular expressions

```bash
# Run all tests containing "create"
./bin/extension --ginkgo.focus="create"

# Run all tests containing "update"
./bin/extension --ginkgo.focus="update"

# Skip slow tests
./bin/extension --ginkgo.skip="Slow"

# Skip disruptive tests
./bin/extension --ginkgo.skip="Disruptive"
```

### 6. Run tests in parallel

```bash
# Run tests with 4 parallel processes
./bin/extension --ginkgo.procs=4

# Note: Tests marked as Ordered will automatically run serially
```

### 7. Set timeouts

```bash
# Set timeout to 10 minutes per test
./bin/extension --ginkgo.timeout=10m

# Set timeout for the entire suite
./bin/extension --ginkgo.timeout=2h
```

### 8. Verbose output and debugging

```bash
# Show verbose output
./bin/extension --ginkgo.v

# Show test progress
./bin/extension --ginkgo.progress

# Show full stack trace on failure
./bin/extension --ginkgo.trace

# Combine options
./bin/extension --ginkgo.v --ginkgo.progress --ginkgo.trace
```

### 9. Generate test reports

```bash
# Generate JUnit XML report
./bin/extension --ginkgo.junit-report=junit.xml

# Generate JSON report
./bin/extension --ginkgo.json-report=report.json

# Generate both reports
./bin/extension \
  --ginkgo.junit-report=junit.xml \
  --ginkgo.json-report=report.json
```

### 10. Fail fast

```bash
# Stop after the first test failure
./bin/extension --ginkgo.fail-fast

# Stop after 3 test failures
./bin/extension --ginkgo.fail-on-pending --ginkgo.flake-attempts=3
```

## Common Usage Patterns

### Quick validation (single platform)

```bash
# Quick validation on AWS
./bin/extension \
  --ginkgo.focus="AWS" \
  --ginkgo.fail-fast \
  --ginkgo.v
```

### Full CI run

```bash
# Run all tests with reports in CI environment
./bin/extension \
  --ginkgo.v \
  --ginkgo.progress \
  --ginkgo.junit-report=junit.xml \
  --ginkgo.timeout=3h \
  --ginkgo.flake-attempts=2
```

### Debug a single test

```bash
# Run a specific test with verbose output
./bin/extension \
  --ginkgo.focus="should be able to run a machine with a default provider spec" \
  --ginkgo.v \
  --ginkgo.trace
```

### Migration feature tests

```bash
# Test only migration features (requires MachineAPIMigration feature gate)
./bin/extension \
  --ginkgo.focus="MachineAPIMigration" \
  --ginkgo.v \
  --ginkgo.progress
```

## Filter by test labels

Current test labels:
- `[sig-cluster-lifecycle]` - Cluster lifecycle related
- `[OCPFeatureGate:MachineAPIMigration]` - Requires MachineAPIMigration feature gate

```bash
# Run all sig-cluster-lifecycle tests
./bin/extension --ginkgo.focus="sig-cluster-lifecycle"

# Run tests requiring feature gates
./bin/extension --ginkgo.focus="OCPFeatureGate"
```

## Output Formats

### Default output
```
Running Suite: Cluster CAPI Operator E2E Suite
Will run 42 of 44 specs
• • • • • • • • • • • ... (42 tests)
Ran 42 of 44 Specs in 45.123 seconds
SUCCESS! -- 42 Passed | 0 Failed | 2 Pending | 0 Skipped
```

### Verbose output (-v)
```
[It] should be able to run a machine with a default provider spec
  /path/to/test.go:123
  • [5.234 seconds]
```

## Test result files

After running tests, the following files may be generated:
```
junit.xml           # JUnit format report (for CI)
report.json         # JSON format report
```

## Troubleshooting

### Issue: Tests are skipped
```bash
# Check why tests are skipped
./bin/extension --ginkgo.v --ginkgo.focus="YOUR_TEST"
```

Common reasons:
- Platform mismatch (AWS tests will be skipped on GCP clusters)
- Feature gate not enabled
- Cluster doesn't meet test requirements (e.g., SNO clusters)

### Issue: Test timeout
```bash
# Increase timeout
./bin/extension --ginkgo.timeout=30m
```

### Issue: KUBECONFIG not found
```bash
# Ensure KUBECONFIG is set
export KUBECONFIG=/path/to/kubeconfig
oc cluster-info
```

## Best Practices

1. **Local development**: Use `--ginkgo.focus` to run only the tests you care about
2. **CI environment**: Generate reports and set reasonable timeouts
3. **Debugging**: Use `-v` and `--trace` for detailed information
4. **Quick validation**: Use `--fail-fast` and focus on a specific platform

## More Options

View all available options:
```bash
./bin/extension --help
```

Ginkgo official documentation:
- https://onsi.github.io/ginkgo/

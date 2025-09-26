# AGENTS.md

Instructions for AI Agents when working with the cluster-capi-operator project.

## Quick Reference

### Essential Commands
```bash
# Build and test
make build          # Build all binaries
make test           # Run all tests
make unit           # Run unit tests with coverage
make lint           # Run linting
make fmt            # Format code

## Project Overview

The Cluster CAPI Operator manages the installation and lifecycle of Cluster API components on OpenShift clusters. It serves as a bridge between OpenShift's Machine API (MAPI) and the upstream Cluster API (CAPI), providing forward compatibility and migration capabilities.

### Architecture 

The operator consists of two main binaries:

1. **cluster-capi-operator** - Main operator that manages CAPI component installation and lifecycle
2. **machine-api-migration** - Handles migration between MAPI and CAPI resources

The repository also includes:

3. **manifests-gen** (`manifests-gen/`) - Standalone tool that transforms upstream Cluster API provider manifests into OpenShift-compatible format, applying OpenShift-specific annotations, replacing cert-manager with service-ca, and generating provider ConfigMaps with compressed components

### Key Controllers

#### Core Controllers
- **ClusterOperator Controller** (`pkg/controllers/clusteroperator/`) - Manages the operator's status in the cluster
- **CAPI Installer Controller** (`pkg/controllers/capiinstaller/`) - Handles installation of CAPI components
- **Core Cluster Controller** (`pkg/controllers/corecluster/`) - Manages CAPI Cluster resources representing the OpenShift cluster
- **Infra Cluster Controller** (`pkg/controllers/infracluster/`) - Manages infrastructure-specific cluster resources (AWS, Azure, GCP, etc.)
- **Secret Sync Controller** (`pkg/controllers/secretsync/`) - Synchronizes secrets between MAPI and CAPI namespaces
- **Kubeconfig Controller** (`pkg/controllers/kubeconfig/`) - Manages kubeconfig secrets for cluster access


#### Migration Controllers
- **Machine Migration Controller** (`pkg/controllers/machinemigration/`) - Handles handover of AuthoritativeAPI and object pausing for machine migration
- **MachineSet Migration Controller** (`pkg/controllers/machinesetmigration/`) - Handles handover of AuthoritativeAPI and object pausing for machineset migration
- **Machine Sync Controller** (`pkg/controllers/machinesync/`) - Synchronizes individual machine related resources between APIs
- **MachineSet Sync Controller** (`pkg/controllers/machinesetsync/`) - Synchronizes machineset related objects resources between APIs

#### Conversion Framework
- **MAPI to CAPI Conversion** (`pkg/conversion/mapi2capi/`) - Library implementing Conversion of MAPI resources to CAPI
- **CAPI to MAPI Conversion** (`pkg/conversion/capi2mapi/`) - Library implementing conversion of CAPI resources to MAPI

### File Structure
- `manifests/` - Contains OpenShift manifests for operator deployment
- `manifests-gen/` - Tool for generating customized provider manifests
- `hack/` - Development and testing scripts
- `docs/controllers/` - Detailed controller documentation
- `e2e/` - End-to-end tests for each supported platform

## Development Rules
**- ALWAYS check for existing patterns, and use them if found**

### Coding Style
- Use early returns
- Descriptive names
- Helper functions over inline code
- Minimal comments (only for non-obvious decisions)
- Simple code over complex language features
- For user-facing text like logs and errors, use "Cluster API" and "Machine API". For code and internal identifiers, use "CAPI" and "MAPI".

## Testing

### Running Tests
```bash
make unit                                    # All unit tests
make unit TEST_DIRS="./pkg/controllers/machinesync/..."  # Specific package directories/dirs
./hack/test.sh "./pkg/..." 10m             # With timeout
```
#### Default ginkgo arguments
- `GINKGO_ARGS="-r -v --randomize-all --randomize-suites --keep-going --race --trace --timeout=10m"`
Prefer using `GINKGO_EXTRA_ARGS` to pass additional arguments to ginkgo. Use `GINKGO_ARGS` when you need to override the default values entirely.

### Test Patterns
- Use **Ginkgo/Gomega** framework
- Use **Komega** for async assertions
- Use **WithTransform** + helper functions
- Provide detailed error context
- Check existing test patterns before writing new ones

### Focused Testing
```go
// Focus specific tests (REMOVE before committing!)
FIt("test name", func() { /* test */ })
FContext("context name", func() { /* tests */ })
```

### Test Environment
- Tests MUST use `make unit` (requires kubebuilder assets)
- Each controller has `suite_test.go`


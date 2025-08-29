# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Table of Contents

- [Development Commands](#development-commands)
  - [Building and Testing](#building-and-testing)
  - [Local Development](#local-development)
  - [Testing](#testing)
- [Architecture Overview](#architecture-overview)
  - [Key Controllers](#key-controllers)
    - [Core Management Controllers](#core-management-controllers)
    - [Migration Controllers](#migration-controllers)
    - [Platform Support](#platform-support)
  - [Package Structure](#package-structure)
    - [Core Packages](#core-packages)
    - [Key Files](#key-files)
  - [Development Patterns](#development-patterns)
    - [Controller Pattern](#controller-pattern)
    - [Platform Detection](#platform-detection)
    - [Status Management](#status-management)
  - [Testing](#testing-1)
    - [Test Structure](#test-structure)
    - [Test Execution](#test-execution)
    - [Environment Setup](#environment-setup)
    - [Running Individual Tests](#running-individual-tests)
    - [Testing Conventions](#testing-conventions)
  - [Before Making Changes](#before-making-changes)
  - [Important Conventions](#important-conventions)
    - [Migration Annotations](#migration-annotations)
- [Coding Style Guide](#coding-style-guide)
- [Code Review Guidelines](#code-review-guidelines)
  - [Line Number References](#line-number-references)
  - [Example](#example)
  - [Strategy for performing the review](#strategy-for-performing-the-review)

## Development Commands

### Building and Testing
- `make build` - Build all binaries (cluster-capi-operator, machine-api-migration, manifests-gen)
- `make test` - Run verification and unit tests
- `make unit` - Run unit tests only with coverage
- `make lint` - Run golangci-lint checks
- `make fmt` - Format code and apply fixes
- `make verify` - Run formatting and linting verification

### Local Development
- `make run` - Run operator locally against configured cluster
- `make localtestenv` - Set up local test environment with kubebuilder assets

### Testing
- `./hack/test.sh "./pkg/..." 10m` - Run specific test directories with timeout
- `./hack/unit-tests.sh` - Run unit tests via script

## Architecture Overview

The Cluster CAPI Operator manages Cluster API (CAPI) components lifecycle on OpenShift clusters. It operates on TechPreview clusters only and treats the management cluster as both management and workload cluster.

### Key Controllers

#### Core Management Controllers
- **ClusterOperatorController** (`pkg/controllers/clusteroperator/`) - Manages CoreProvider and InfrastructureProvider CRs, runs on all platforms
- **CoreClusterController** (`pkg/controllers/corecluster/`) - Manages CAPI Cluster CRs and sets ControlPlaneInitialized condition
- **InfraClusterController** (`pkg/controllers/infracluster/`) - Manages infrastructure-specific cluster objects using "externally managed" pattern
- **CapiInstallerController** (`pkg/controllers/capiinstaller/`) - Handles CAPI component installation and lifecycle
- **UserDataSecretController** (`pkg/controllers/secretsync/`) - Syncs user data secrets between namespaces
- **KubeconfigReconciler** (`pkg/controllers/kubeconfig/`) - Manages kubeconfig generation and syncing

#### Migration Controllers
These controllers implement the Machine API to Cluster API migration strategy as defined in the [OpenShift Enhancement Proposal](https://github.com/openshift/enhancements/blob/master/enhancements/machine-api/converting-machine-api-to-cluster-api.md):

- **MachineSyncReconciler** (`pkg/controllers/machinesync/`) - Bidirectional sync controller that translates individual Machine resources between Machine API and Cluster API. Handles synchronization based on `spec.authoritativeAPI` field.

- **MachineSetSyncReconciler** (`pkg/controllers/machinesetsync/`) - Bidirectional sync controller for MachineSet resources. Handles synchronization based on `spec.authoritativeAPI` field.

- **MachineMigrationReconciler** (`pkg/controllers/machinemigration/`) - Migration controller that orchestrates the transition between `spec.authoritativeAPI` states for Machines. Also handles object pausing for machine migration.

- **MachineSetMigrationReconciler** (`pkg/controllers/machinesetmigration/`) - Migration controller for MachineSet resources. Manages the transition between `spec.authoritativeAPI` states for MachineSets. Also handles object pausing for machine migration.


#### Platform Support
Supported platforms configured in `setupPlatformReconcilers()`:
- AWS (`AWSCluster`)
- Azure (`AzureCluster`) - except AzureStack
- GCP (`GCPCluster`) 
- PowerVS (`IBMPowerVSCluster`)
- vSphere (`VSphereCluster`)
- OpenStack (`OpenStackCluster`)
- BareMetal (`Metal3Cluster`)

### Package Structure

#### Core Packages
- `pkg/controllers/` - All controller implementations with platform-specific logic
- `pkg/controllers/synccommon/` - Shared utilities for sync and migration controllers
- `pkg/conversion/` - Bidirectional conversion between CAPI and MAPI objects (capi2mapi, mapi2capi)
- `pkg/util/` - Common utilities for annotations, conditions, platform detection, etc.
- `pkg/operatorstatus/` - Cluster operator status management
- `pkg/webhook/` - Admission webhooks for cluster validation

#### Key Files
- `cmd/cluster-capi-operator/main.go` - Main operator entry point with platform detection and controller setup
- `cmd/machine-api-migration/main.go` - Migration between Machine API and Cluster API
- `manifests-gen/` - Tool for generating OpenShift manifests from CAPI assets

### Development Patterns

#### Controller Pattern
All controllers follow standard controller-runtime patterns:
- Use `Reconcile(ctx context.Context, req ctrl.Request)` method signature
- Return `ctrl.Result{}` with optional requeue timing and error handling
- Leverage controller-runtime's manager, client, and scheme for resource management
- Implement proper RBAC markers and controller registration with the manager

#### Platform Detection
Platform-specific logic uses `configv1.PlatformType` for feature detection and appropriate resource creation.

#### Status Management
Controllers use `operatorstatus.ClusterOperatorStatusClient` for consistent status reporting.

### Testing

#### Test Structure
- Unit tests use Ginkgo/Gomega framework
- Each controller package has `suite_test.go` with test suite setup
- Fuzz testing for conversion packages
- E2E tests for platform-specific scenarios

#### Test Execution
- Tests require kubebuilder test environment (managed via `.localtestenv`)
- Coverage reporting integrated with CI/CD
- Platform-specific test files (e.g., `aws_test.go`, `azure_test.go`)

#### Environment Setup
- Tests use envtest framework with kubebuilder assets and MUST be executed using `make unit` target to work

#### Running Individual Tests
For complex nested Ginkgo tests, programmatic focus is most reliable:
```go
// Change It to FIt, Context to FContext, or Describe to FDescribe
FIt("should have the desired behavior", func() {
    // test code
})
```
Then run only on the files with focus: `make unit TEST_PACKAGES="./pkg/controllers/machinesetsync/..."`

**Important:** Always revert programmatic focus changes (remove `F` prefix) before committing.

#### Testing Conventions
When writing tests, follow these important patterns:

1. **Use Komega for async assertions** - Prefer Komega's Eventually/Consistently over standard Gomega
2. **Leverage matchers** - Use existing Gomega matchers and create custom matchers when appropriate
3. **Use WithTransform + helper functions** - Extract complex object transformations into reusable helper functions
4. **Provide detailed error context** - Tests run in CI environments, so ensure failures provide sufficient debugging information
5. **Research existing patterns** - Look at existing test files in the codebase to understand established conventions before writing new tests



### Important Conventions

#### Migration Annotations
- `cluster.x-k8s.io/managed-by` - Marks externally managed infrastructure
- Migration controllers use specific annotations for tracking state
- `spec.authoritativeAPI` field determines which API is authoritative during migration

## Coding Style Guide

### Before Making Changes
1. **Propose a plan** and get user approval before implementing
2. **Ensure clean git state** - nothing staged (`git status` should be clean)
3. **Research existing implementations** in the codebase to justify changes
4. **Find similar patterns** in the codebase to follow established conventions

### Style rules

- Utilize early returns for code readability.
- Add comments sparingly, when you ensure they're useful (e.g explaining a decision that is not obvious)
- Employ descriptive variable and function/const names.
- Utilize helper functions wherever possible.
- Don't define local functions or inline structs where a helper function will make the code easier to read and test.
- Avoid closures and complex features of the programming language unless they actually provide an advantage over more simple coding styles. Generally keep things simple!
- If there is any ambiguity around a decision, first ask for clarification and document where any assumptions have been made.

## Code Review Guidelines

### Line Number References
 - When reviewing PRs, always reference line numbers from the **PR diff context** (using `+` prefix for added lines)
 - Use the format: `+LineNumber: description` when referencing specific lines in the diff
 - For multi-line issues, reference the starting line of the problematic section
 - When suggesting changes, quote the actual problematic code rather than just line numbers

### Example
Instead of: "Line 158 has an issue"
Use: "+80: The Context description `Context("with spec.authoritativeAPI: ClusterAPI, Prevent changes to non-authoritative
   Machines except from sync controller;", func() {` is too long"

### Strategy for performing the review

- When given a PR to review, get the code changes from GitHub, either by adding the fork as a remote and pulling the branch, or directly using the GitHub API.

- The aim of the code review is to provide actionable feedback. If making a suggestion, provide example code changes, with an explanation of why.

- We want the code not to be surprising, and easy to maintain. We don't want multiple approaches to do the same thing. Look for prior art, if it exists use it to inform the review. Ensure the code follows the coding style guide.

- Methodically review all of the changes made. If existing patterns already exist in the code base, ensure they follow them. If tests are added, ensure they follow the testing conventions outlined above. 

- Make sure you're only making suggestions to changes in the PR provided, and not existing code, unless explicitly asked.

# CLAUDE.md

Instructions for Claude Code when working with the cluster-capi-operator project.

## Quick Reference

### Essential Commands
```bash
# Build and test
make build          # Build all binaries
make test           # Run all tests
make unit           # Run unit tests with coverage
make lint           # Run linting
make fmt            # Format code

# Development
make run            # Run operator locally
make localtestenv   # Setup test environment
```

### Before Any Changes
1. **Always propose a plan** and get user approval
2. **Check git status is clean** - no staged changes
3. **Research existing patterns** in the codebase
4. **Run `make lint` and `make unit`** after changes

## Project Overview

**Cluster CAPI Operator** manages Cluster API (CAPI) lifecycle on OpenShift TechPreview clusters.

### Key Controllers

**Core Controllers:**
- `ClusterOperatorController` - Manages CoreProvider/InfrastructureProvider CRs
- `CoreClusterController` - Manages CAPI Cluster CRs
- `InfraClusterController` - Platform-specific cluster objects
- `CapiInstallerController` - CAPI component installation
- `UserDataSecretController` - Secret syncing
- `KubeconfigReconciler` - Kubeconfig management

**Migration Controllers:**
- `MachineSyncReconciler` - Sync individual Machines between APIs
- `MachineSetSyncReconciler` - Sync MachineSets between APIs  
- `MachineMigrationReconciler` - Orchestrate Machine migration
- `MachineSetMigrationReconciler` - Orchestrate MachineSet migration

**Supported Platforms:** AWS, Azure, GCP, PowerVS, vSphere, OpenStack, BareMetal

### Critical Directories
```
pkg/controllers/        # All controllers
pkg/conversion/         # CAPI ↔ MAPI conversion
pkg/util/              # Common utilities
pkg/operatorstatus/    # Status management
```

## Development Rules

### Controller Patterns
- Use `Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error)`
- Include RBAC markers
- Register with manager
- Use `operatorstatus.ClusterOperatorStatusClient` for status

### Platform Detection
Use `configv1.PlatformType` for platform-specific logic.

### Migration Conventions
- `spec.authoritativeAPI` determines active API
- `cluster.x-k8s.io/managed-by` marks external management

### Coding Style
- Use early returns
- Descriptive names
- Helper functions over inline code
- Minimal comments (only for non-obvious decisions)
- Simple code over complex language features

## Testing

### Running Tests
```bash
make unit                                    # All unit tests
make unit TEST_PACKAGES="./pkg/controllers/machinesync/..."  # Specific package
./hack/test.sh "./pkg/..." 10m             # With timeout
```

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

## Agent Usage Guidelines

### Code-Reviewer Agent
**ALWAYS use for:**
- Code reviews and PR reviews
- Change analysis and compliance checking

### Thinker Agent (Expert Feedback)
**Use proactively for:**
- **Plan validation** - Before implementing complex changes
- **Design review** - After proposing architectural changes
- **Implementation feedback** - After completing significant features
- **Problem-solving approach** - When tackling complex issues

### Gemini Agent (Verification)
**Use proactively for:**
- **API/function signatures** - Verify controller-runtime, Kubernetes APIs
- **Design pattern validation** - Confirm architectural approaches
- **Technical accuracy** - Verify Go patterns, testing frameworks
- **Concept validation** - Check understanding of CAPI, operator patterns

### Agent Response Handling Rules
**CRITICAL:** When using any specialized agent:
- **Return agent response VERBATIM** - no summarizing
- **DO NOT apply conciseness rules** 
- **Preserve ALL formatting and examples**
- Agent responses are exempt from "be concise" instruction

### Required Review Format
The code-reviewer agent MUST use this format:

```
+LineNumber: Description

**Current (problematic) code:**
```go
[exact code from PR]
```

**Suggested change:**
```diff
- [old code]
+ [new code]
```

**Explanation:** [reason for change]
```

## Workflow Checklist

### Making Changes
- [ ] Propose plan to user
- [ ] **Use thinker agent** to validate complex plans
- [ ] Verify git status clean
- [ ] Research existing patterns
- [ ] **Use gemini agent** to verify API signatures/patterns
- [ ] Implement changes
- [ ] **Use thinker agent** for implementation feedback
- [ ] Run `make lint`
- [ ] Run `make unit`
- [ ] Commit only when user explicitly requests

### Testing Changes
- [ ] **Use gemini agent** to verify testing frameworks/patterns
- [ ] Use `make unit` for test execution
- [ ] Follow existing test patterns
- [ ] Add tests for new functionality
- [ ] Remove `F` prefixes before committing

### Code Reviews
- [ ] Use code-reviewer agent
- [ ] **Use thinker agent** for design review
- [ ] Return agent responses verbatim
- [ ] Include line numbers, code quotes, and diffs
- [ ] Check project conventions compliance

## Important Notes

- **Never commit** unless explicitly asked
- **Never create files** unless absolutely necessary  
- **Always edit existing files** instead of creating new ones
- **No documentation files** unless explicitly requested
- **Research first** - look for existing patterns before implementing
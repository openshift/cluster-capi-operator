# AGENTS.md

Instructions for AI Agents when working with the cluster-capi-operator project.

## Project Overview

The Cluster CAPI Operator manages the installation and lifecycle of Cluster API components on OpenShift clusters. It serves as a bridge between OpenShift's Machine API (MAPI) and the upstream Cluster API (CAPI), providing forward compatibility and migration capabilities.

## Development Rules

- **ALWAYS check for existing patterns, and use them if found**
- Before writing new test utilities, builders, matchers, or setup code, search the repo for existing ones
- Never commit focused tests (`FIt`, `FContext`, `FDescribe`)

## Reference

Detailed guidance is split into reference files. Consult these when working in the relevant area:

- [Code Structure](.agents/reference/code-structure.md) — Architecture, binaries, controllers, conversion framework, file structure
- [Style Guide](.agents/reference/style-guide.md) — Coding conventions and naming rules
- [Tasks](.agents/reference/tasks.md) — Make targets, running tests, ginkgo arguments
- [Testing](.agents/reference/testing.md) — Test patterns, assertion conventions, resource builders, test-level selection

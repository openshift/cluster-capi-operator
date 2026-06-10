# Code Structure

## Architecture

The operator consists of four main binaries:

1. **capi-operator** (`cmd/capi-operator/`) - Manages installation of Cluster API components and providers. Runs the ClusterOperator, Revision, and Installer controllers.
2. **capi-controllers** (`cmd/capi-controllers/`) - Manages CAPI cluster resources, infrastructure integration, secret synchronization, and kubeconfig management. Runs the CoreCluster, InfraCluster, SecretSync, and Kubeconfig controllers plus webhooks.
3. **machine-api-migration** (`cmd/machine-api-migration/`) - Handles migration between Machine API and Cluster API resources. Only runs when the MachineAPIMigration feature gate is enabled. Currently supports AWS and OpenStack platforms.
4. **crd-compatibility-checker** (`cmd/crd-compatibility-checker/`) - Validates CRD compatibility requirements, runs object validation/pruning webhooks, and installs static resources for the compatibility requirements system.

The repository also includes:

5. **manifests-gen** (`manifests-gen/`) - Standalone tool that generates admission policy profiles from upstream Cluster API provider manifests for embedding into the operator image.

## Key Controllers

### capi-operator Controllers
- **ClusterOperator Controller** (`pkg/controllers/clusteroperator/`) - Manages the operator's ClusterOperator status resource. Always runs, even on unsupported platforms.
- **Revision Controller** (`pkg/controllers/revision/`) - Manages OLM revision resources for tracking installed provider versions and triggering upgrades.
- **Installer Controller** (`pkg/controllers/installer/`) - Handles installation and lifecycle management of CAPI components and providers using the boxcutter framework.

### capi-controllers Controllers
- **Core Cluster Controller** (`pkg/controllers/corecluster/`) - Manages CAPI Cluster resources representing the OpenShift cluster
- **Infra Cluster Controller** (`pkg/controllers/infracluster/`) - Manages infrastructure-specific cluster resources (AWS, Azure, GCP, etc.)
- **Secret Sync Controller** (`pkg/controllers/secretsync/`) - Synchronizes secrets between MAPI and CAPI namespaces
- **Kubeconfig Controller** (`pkg/controllers/kubeconfig/`) - Manages kubeconfig secrets for cluster access

### crd-compatibility-checker Controllers
- **CRD Compatibility Controller** (`pkg/controllers/crdcompatibility/`) - Reconciles CompatibilityRequirement resources and validates CRD create/update/delete operations via webhooks. Includes object validation and pruning sub-controllers.
- **Static Resource Installer Controller** (`pkg/controllers/staticresourceinstaller/`) - Installs static Kubernetes resources from embedded asset files on startup.

### machine-api-migration Controllers
- **Machine Migration Controller** (`pkg/controllers/machinemigration/`) - Handles handover of AuthoritativeAPI and object pausing for machine migration
- **MachineSet Migration Controller** (`pkg/controllers/machinesetmigration/`) - Handles handover of AuthoritativeAPI and object pausing for machineset migration
- **Machine Sync Controller** (`pkg/controllers/machinesync/`) - Synchronizes individual machine related resources between APIs
- **MachineSet Sync Controller** (`pkg/controllers/machinesetsync/`) - Synchronizes machineset related objects between APIs

### Shared Packages
- **Sync Common** (`pkg/controllers/synccommon/`) - Shared apply-configuration helpers and migration status logic used by the machine and machineset sync/migration controllers

## Conversion Framework
- **MAPI to CAPI Conversion** (`pkg/conversion/mapi2capi/`) - Converts MAPI resources to CAPI (supports AWS, OpenStack)
- **CAPI to MAPI Conversion** (`pkg/conversion/capi2mapi/`) - Converts CAPI resources to MAPI (supports AWS, OpenStack)
- **Conversion Utilities** (`pkg/conversion/util/`, `pkg/conversion/consts/`) - Shared constants and helper functions
- **Conversion Test Utilities** (`pkg/conversion/test/`) - Fuzz testing and test matchers for conversion logic

## File Structure
- `manifests/` - OpenShift manifests for operator deployment
- `capi-operator-manifests/` - Upstream CAPI provider manifests consumed by manifests-gen
- `admission-policies/` - Kustomize overlays for admission policy profiles (default, AWS)
- `manifests-gen/` - Tool for generating admission policy profiles
- `hack/` - Development and testing scripts
- `docs/controllers/` - Detailed controller documentation
- `e2e/` - End-to-end tests for each supported platform
- `vendor/` - Vendored dependencies (use `make vendor` to update)

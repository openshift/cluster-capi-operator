#!/bin/bash

echo "Updating dependencies for Cluster CAPI Operator workspace"

# Tidy all modules in the workspace
echo "Running go mod tidy for all modules..."
go work use -r .
for module in . e2e manifests-gen hack/tools; do
  if [ -f "$module/go.mod" ]; then
    echo "Tidying $module"
    (cd "$module" && go mod tidy)
  fi
done

# Verify all modules
echo "Verifying all modules..."
for module in . e2e manifests-gen hack/tools; do
  if [ -f "$module/go.mod" ]; then
    echo "Verifying $module"
    (cd "$module" && go mod verify)
  fi
done

# Sync workspace
echo "Syncing Go workspace..."
if ! go work sync; then
  echo "Warning: go work sync failed due to dependency conflicts. This is expected with the current vsphere provider dependency."
  echo "The workspace structure is in place for future use."
fi

# Create unified vendor directory
echo "Creating unified vendor directory..."
go work vendor -v

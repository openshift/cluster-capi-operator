#!/bin/bash

vendor() {
  go mod tidy
  go mod vendor
  go mod verify
}

vendor_in_subdir() {
  local dir=$1
  echo "Updating dependencies for $dir"
  cd "$dir" && GOWORK=off vendor && cd -
}

echo "Updating dependencies for Cluster CAPI Operator"
GOWORK=off vendor

echo "Syncing Go workspace"
if ! go work sync; then
  echo "Warning: go work sync failed due to dependency conflicts. This is expected with the current vsphere provider dependency."
  echo "The workspace structure is in place for future use, but individual module vendoring will be used for builds."
fi

vendor_in_subdir "hack/tools"
vendor_in_subdir "manifests-gen"

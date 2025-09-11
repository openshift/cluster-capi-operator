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
go work sync

vendor_in_subdir "hack/tools"
vendor_in_subdir "manifests-gen"

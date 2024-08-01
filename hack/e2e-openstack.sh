#!/bin/bash

set -euo pipefail

echo "Running e2e-openstack.sh"

unset GOFLAGS
tmp="$(mktemp -d)"

echo "cloning github.com/openshift/cluster-api-provider-openstack at branch '$PULL_BASE_REF'"
git clone --single-branch --branch="$PULL_BASE_REF" --depth=1 "https://github.com/openshift/cluster-api-provider-openstack.git" "$tmp"

echo "running cluster-api-provider-openstack's: make e2e"
exec make -C "$tmp/openshift" e2e

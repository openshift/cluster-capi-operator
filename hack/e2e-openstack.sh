#!/bin/bash

set -euo pipefail

echo "Running e2e-openstack.sh"

unset GOFLAGS
tmp="$(mktemp -d)"

git clone --depth=1 "https://github.com/openshift/cluster-api-provider-openstack.git" "$tmp"

exec make -C "$tmp/openshift" e2e

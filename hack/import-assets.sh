#!/bin/bash

set -o errexit
set -o pipefail

REPO_ROOT=$(dirname "${BASH_SOURCE}")/..

mkdir -p $REPO_ROOT/assets/capi-operator
mkdir -p $REPO_ROOT/assets/providers
cd $REPO_ROOT/hack/import-assets; go run .; cd -

#!/bin/bash

set -o errexit
set -o pipefail

REPO_ROOT=$(dirname "${BASH_SOURCE}")/..

mkdir -p $REPO_ROOT/assets/core-capi
mkdir -p $REPO_ROOT/assets/infrastructure-providers
cd $REPO_ROOT/hack/assets; go run . $1; cd -

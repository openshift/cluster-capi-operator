#!/bin/bash

set -o errexit
set -o pipefail

export LOCAL_BINARIES_PATH=$(realpath "$(dirname "${BASH_SOURCE[0]}")/..")/bin

# go-get-tool check if binary $1 presented, if not 'go get' package $2 and install it.
function go-get-tool() {
    if [ -f "$1" ] ; then
        echo "$1 already exists"
        return
    fi

    local TMP_DIR=$(mktemp -d)
    pushd "$TMP_DIR"
        go mod init tmp
        echo "Downloading $2"
        GOBIN=$LOCAL_BINARIES_PATH go get "$2"
    popd
    rm -rf "$TMP_DIR"
}

"$@"

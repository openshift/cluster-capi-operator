#!/bin/bash

set -o errexit
set -o pipefail

REPO_ROOT=$(realpath "$(dirname "${BASH_SOURCE[0]}")/..")

ENVTEST_VERSION=v0.7.0
ENVTEST_ASSETS_DIR=/tmp/testbin
ENVTEST_SETUP_SCRIPT=https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/${ENVTEST_VERSION}/hack/setup-envtest.sh

function setupEnvtest() {
    echo "Envtest version: ${ENVTEST_VERSION}."
    mkdir -p ${ENVTEST_ASSETS_DIR}
    test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh ${ENVTEST_SETUP_SCRIPT}
    source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh
    fetch_envtest_tools ${ENVTEST_ASSETS_DIR}
    setup_envtest_env ${ENVTEST_ASSETS_DIR}

    # Ensure that some home var is set and that it's not the root
    export HOME=${HOME:=/tmp/kubebuilder/testing}
    if [ $HOME == "/" ]; then
      export HOME=/tmp/kubebuilder/testing
    fi
}


OPENSHIFT_CI=${OPENSHIFT_CI:-""}
ARTIFACT_DIR=${ARTIFACT_DIR:-""}

function go_test() {
    go test "$@" ./... -coverprofile cover.out
}

runTestCI() {
    local GO_JUNIT_REPORT_PATH=$LOCAL_BINARIES_PATH/go-junit-report
    echo "CI env detected, run tests with jUnit report extraction"
    if [ -n "$ARTIFACT_DIR" ] && [ -d "$ARTIFACT_DIR" ]; then
        local JUNIT_LOCATION="$ARTIFACT_DIR"/junit_cluster_cloud_controller_manager_operator.xml
        echo "jUnit location: $JUNIT_LOCATION"
        ./hack/go-get-tool.sh go-get-tool "$GO_JUNIT_REPORT_PATH" github.com/jstemmer/go-junit-report
        go_test -v | tee >($GO_JUNIT_REPORT_PATH > "$JUNIT_LOCATION")
    else
        echo "\$ARTIFACT_DIR not set or does not exists, no jUnit will be published"
        go_test
    fi
}

function runTests() {
    if [ "$OPENSHIFT_CI" == "true" ]; then
        runTestCI
    else
        go_test -test.short
    fi
}


cd "${REPO_ROOT}" && \
setupEnvtest && \
runTests

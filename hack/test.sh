#!/usr/bin/env bash

set -o nounset
set -o pipefail

REPO_ROOT=$(dirname "${BASH_SOURCE}")/..

localtestenv=${REPO_ROOT}/.localtestenv
if [ -e "$localtestenv" ]; then
    export $(xargs < "$localtestenv")
fi

# Use existing value of TEST_DIRS, or $1 if not set. Makes it easier to target suites.
TEST_DIRS=${TEST_DIRS:-$1}
# Use 2nd arg, or 5m.
TIMEOUT=${2:-"5m"}

OPENSHIFT_CI=${OPENSHIFT_CI:-""}
ARTIFACT_DIR=${ARTIFACT_DIR:-""}
GINKGO=${GINKGO:-"go run -mod=vendor ${REPO_ROOT}/vendor/github.com/onsi/ginkgo/v2/ginkgo"}
GINKGO_ARGS=${GINKGO_ARGS:-"-r -v --randomize-all --randomize-suites --keep-going --race --trace --timeout=${TIMEOUT}"}
GINKGO_EXTRA_ARGS=${GINKGO_EXTRA_ARGS:-""}

# Ensure that some home var is set and that it's not the root.
# This is required for the kubebuilder cache.
export HOME=${HOME:=/tmp/kubebuilder-testing}
if [ $HOME == "/" ]; then
  export HOME=/tmp/kubebuilder-testing
fi

if [ "$OPENSHIFT_CI" == "true" ] && [ -n "$ARTIFACT_DIR" ] && [ -d "$ARTIFACT_DIR" ]; then # detect ci environment there
  GINKGO_ARGS="${GINKGO_ARGS} --junit-report=junit_cluster_capi_operator.xml --cover --coverprofile=test-unit-coverage.out --output-dir=${ARTIFACT_DIR}"
fi

# Print the command we are going to run as Make would.
echo ${GINKGO} ${GINKGO_ARGS} ${GINKGO_EXTRA_ARGS} ${TEST_DIRS}
eval "${GINKGO} ${GINKGO_ARGS} ${GINKGO_EXTRA_ARGS} ${TEST_DIRS}"
# Capture the test result to exit on error after coverage.
TEST_RESULT=$?

if [ -f "${ARTIFACT_DIR}/test-unit-coverage.out" ]; then
  # Convert the coverage to html for spyglass.
  go tool cover -html=${ARTIFACT_DIR}/test-unit-coverage.out -o ${ARTIFACT_DIR}/test-unit-coverage.html

  # Report the coverage at the end of the test output.
  echo -n "Coverage "
  go tool cover -func=${ARTIFACT_DIR}/test-unit-coverage.out | tail -n 1
  # Blank new line after the coverage output to make it easier to read when there is an error.
  echo
fi

# Ensure we exit based on the test result, coverage results are supplementary.
exit ${TEST_RESULT}

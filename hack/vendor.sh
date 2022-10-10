#!/bin/bash

vendor() {
  go mod tidy
  go mod vendor
  go mod verify
}

echo "Updating dependencies for Cluster CAPI Operator"
vendor

echo "Updating dependencies for E2E tests"
cd e2e/ && vendor && cd -

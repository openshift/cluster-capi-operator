IMG ?= controller:latest
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
TOOLS_DIR := hack/tools
TOOLS_BIN_DIR := $(TOOLS_DIR)/bin
BIN_DIR := bin
GOLANGCI_LINT = $(PROJECT_DIR)/$(TOOLS_BIN_DIR)/golangci-lint
KUSTOMIZE = $(PROJECT_DIR)/$(TOOLS_BIN_DIR)/kustomize

all: build

verify-%:
	make $*
	./hack/verify-diff.sh

verify: fmt lint

# Run tests
test: verify unit

# Build operator binaries
build: operator user-data-secret-controller cluster-controller

operator:
	go build -o bin/cluster-capi-operator cmd/cluster-capi-operator/main.go

user-data-secret-controller:
	go build -o bin/user-data-secret-controller cmd/user-data-secret-controller/main.go

cluster-controller:
	go build -o bin/cluster-controller cmd/cluster-controller/main.go

unit:
	hack/unit-tests.sh

# Run against the configured Kubernetes cluster in ~/.kube/config
run: verify
	go run cmd/cluster-capi-operator/main.go --leader-elect=false --images-json=./hack/sample-images.json

# Run go fmt against code
.PHONY: fmt
fmt: $(GOLANGCI_LINT)
	( GOLANGCI_LINT_CACHE=$(PROJECT_DIR)/.cache $(GOLANGCI_LINT) run --fix )

# Run go vet against code
.PHONY: vet
vet: lint

.PHONY: lint
lint: $(GOLANGCI_LINT)
	( GOLANGCI_LINT_CACHE=$(PROJECT_DIR)/.cache $(GOLANGCI_LINT) run )

# Download golangci-lint locally if necessary
$(GOLANGCI_LINT): $(TOOLS_DIR)/go.mod # Build golangci-lint from tools folder.
	cd $(TOOLS_DIR); go build -tags=tools -mod=readonly -o $(BIN_DIR)/golangci-lint github.com/golangci/golangci-lint/cmd/golangci-lint

import-assets:
	hack/import-assets.sh

# Run go mod
.PHONY: vendor
vendor:
	go mod tidy
	go mod vendor
	go mod verify

# Build the docker image
.PHONY: image
image:
	docker build -t ${IMG} .

# Push the docker image
.PHONY: push
push:
	docker push ${IMG}

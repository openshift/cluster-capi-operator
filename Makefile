IMG ?= controller:latest
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
TOOLS_DIR := hack/tools
TOOLS_BIN_DIR := $(TOOLS_DIR)/bin
BIN_DIR := bin
GOLANGCI_LINT = $(PROJECT_DIR)/$(TOOLS_BIN_DIR)/golangci-lint
KUSTOMIZE = $(PROJECT_DIR)/$(TOOLS_BIN_DIR)/kustomize
GOLANGCI_LINT_VERSION  = v1.44.1

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.23

HOME ?= /tmp/kubebuilder-testing
ifeq ($(HOME), /)
HOME = /tmp/kubebuilder-testing
endif

all: build

verify-%:
	make $*
	./hack/verify-diff.sh

verify: fmt lint

# Run tests
test: verify unit

# Build operator binaries
build: operator

operator:
	go build -o bin/cluster-capi-operator cmd/cluster-capi-operator/main.go

unit: envtest
	KUBEBUILDER_ASSETS=$(shell $(ENVTEST) --bin-dir=$(shell pwd)/bin use $(ENVTEST_K8S_VERSION) -p path) go test ./... -coverprofile cover.out

# Run against the configured Kubernetes cluster in ~/.kube/config
run: verify
	go run cmd/cluster-capi-operator/main.go --leader-elect=false --images-json=./hack/sample-images.json

# Run go fmt against code
.PHONY: fmt
fmt: golangci-lint
	( GOLANGCI_LINT_CACHE=$(PROJECT_DIR)/.cache $(GOLANGCI_LINT) run --fix )

# Run go vet against code
.PHONY: vet
vet: lint

.PHONY: golangci-lint lint
lint: golangci-lint
	( GOLANGCI_LINT_CACHE=$(PROJECT_DIR)/.cache $(GOLANGCI_LINT) run )

# Download golangci-lint locally if necessary
.PHONY: golangci-lint
GOLANGCI_LINT = $(shell pwd)/bin/golangci-lint
golangci-lint: # Download golangci-lint locally if necessary
	GOBIN=$(PROJECT_DIR)/bin go install -mod=readonly github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: envtest
ENVTEST = $(shell pwd)/bin/setup-envtest
envtest: # Download envtest-setup locally if necessary.
	GOBIN=$(PROJECT_DIR)/bin go install -mod=readonly sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

.PHONY: assets
assets:
	./hack/assets.sh

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

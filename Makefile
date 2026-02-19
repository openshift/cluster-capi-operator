IMG ?= controller:latest
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.33.2

ENVTEST = go run -mod=vendor ${PROJECT_DIR}/vendor/sigs.k8s.io/controller-runtime/tools/setup-envtest
GOLANGCI_LINT = go run -mod=vendor ${PROJECT_DIR}/vendor/github.com/golangci/golangci-lint/cmd/golangci-lint

HOME ?= /tmp/kubebuilder-testing
ifeq ($(HOME), /)
HOME = /tmp/kubebuilder-testing
endif

.PHONY: help all verify test build operator migration manifests-gen unit e2e run fmt vet lint vendor image push aws-cluster azure-cluster gcp-cluster powervs-cluster vsphere-cluster
.DEFAULT_GOAL := build

help: ## Display this help message
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: build ## Build all binaries

verify-%:
	make $*
	./hack/verify-diff.sh

verify: fmt lint ## Run formatting and linting checks

test: verify unit ## Run verification and unit tests

build: bin/capi-operator bin/capi-controllers bin/machine-api-migration bin/crd-compatibility-checker manifests-gen ## Build all binaries

# Ensure bin directory exists for build outputs
bin/:
	mkdir -p bin

.PHONY: manifests-gen
manifests-gen: | bin/ ## Build manifests-gen binary
	cd manifests-gen && go build -o ../bin/manifests-gen && cd ..

bin/%: | bin/ FORCE
	go build -o "$@" "./cmd/$*"

.PHONY: localtestenv
localtestenv: .localtestenv

.localtestenv: Makefile ## Set up local test environment
	KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path --bin-dir $(PROJECT_DIR)/bin --index https://raw.githubusercontent.com/openshift/api/master/envtest-releases.yaml)"; \
	echo "KUBEBUILDER_ASSETS=$${KUBEBUILDER_ASSETS}" > $@

TEST_DIRS ?= ./pkg/... ./manifests-gen/...
unit: .localtestenv ## Run unit tests
	./hack/test.sh "$(TEST_DIRS)" 10m

.PHONY: e2e
e2e: ## Run e2e tests against active kubeconfig
	./hack/test.sh "./e2e/..." 180m

run: ## Run the operator against the configured Kubernetes cluster
	oc -n openshift-cluster-api patch lease cluster-capi-operator-leader -p '{"spec":{"acquireTime": null, "holderIdentity": null, "renewTime": null}}' --type=merge
	go run cmd/cluster-capi-operator/main.go --images-json=./dev-images.json --leader-elect=true --leader-elect-lease-duration=120s --namespace="openshift-cluster-api" --leader-elect-resource-namespace="openshift-cluster-api"

fmt: ## Format Go code
	$(call ensure-home, ${GOLANGCI_LINT} run ./... --fix)

lint: ## Run linter checks
	$(call ensure-home, ${GOLANGCI_LINT} run ./...)

vendor: ## Vendor dependencies
	./hack/vendor.sh

image: ## Build the Docker image
	docker build -t ${IMG} .

push: ## Push the Docker image
	docker push ${IMG}

aws-cluster: ## Create an AWS cluster for testing
	./hack/clusters/create-aws.sh

azure-cluster: ## Create an Azure cluster for testing
	./hack/clusters/create-azure.sh

gcp-cluster: ## Create a GCP cluster for testing
	./hack/clusters/create-gcp.sh

powervs-cluster: ## Create a PowerVS cluster for testing
	./hack/clusters/create-powervs.sh

vsphere-cluster: ## Create a vSphere cluster for testing
	./hack/clusters/create-vsphere.sh

define ensure-home
	@ export HOME=$${HOME:=/tmp/kubebuilder-testing}; \
	if [ $${HOME} == "/" ]; then \
	  export HOME=/tmp/kubebuilder-testing; \
	fi; \
	$(1)
endef

.PHONY: FORCE
FORCE:

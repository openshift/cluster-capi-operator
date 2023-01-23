IMG ?= controller:latest
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.26

ENVTEST = go run ${PROJECT_DIR}/vendor/sigs.k8s.io/controller-runtime/tools/setup-envtest
GOLANGCI_LINT = go run ${PROJECT_DIR}/vendor/github.com/golangci/golangci-lint/cmd/golangci-lint

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

unit:
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path --bin-dir $(PROJECT_DIR)/bin)" ./hack/ci-test.sh

.PHONY: e2e
e2e:
	./hack/test.sh "./e2e/..." 30m

# Run against the configured Kubernetes cluster in ~/.kube/config
run: verify
	go run cmd/cluster-capi-operator/main.go --leader-elect=false --images-json=./hack/sample-images.json

# Run go fmt against code
.PHONY: fmt
fmt:
	$(call ensure-home, ${GOLANGCI_LINT} run ./... --fix)

# Run go vet against code
.PHONY: vet
vet: lint

.PHONY: lint
lint:
	$(call ensure-home, ${GOLANGCI_LINT} run ./...)

.PHONY: assets
assets:
	./hack/assets.sh $(PROVIDER)

# Run go mod
.PHONY: vendor
vendor:
	./hack/vendor.sh

# Build the docker image
.PHONY: image
image:
	docker build -t ${IMG} .

# Push the docker image
.PHONY: push
push:
	docker push ${IMG}

aws-cluster:
	./hack/clusters/create-aws.sh

azure-cluster:
	./hack/clusters/create-azure.sh

gcp-cluster:
	./hack/clusters/create-gcp.sh

powervs-cluster:
	./hack/clusters/create-powervs.sh

define ensure-home
	@ export HOME=$${HOME:=/tmp/kubebuilder-testing}; \
	if [ $${HOME} == "/" ]; then \
	  export HOME=/tmp/kubebuilder-testing; \
	fi; \
	$(1)
endef

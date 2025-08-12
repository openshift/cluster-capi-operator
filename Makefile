IMG ?= controller:latest
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.33.2

define go-package-dir
	$(shell go list -f '{{ .Dir }}' $(1))
endef

ENVTEST = go run $(call go-package-dir,sigs.k8s.io/controller-runtime/tools/setup-envtest)
GOLANGCI_LINT = go run $(call go-package-dir,github.com/golangci/golangci-lint/cmd/golangci-lint)

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

# Build binaries
build: operator migration manifests-gen

.PHONY: manifests-gen
manifests-gen:
	# building manifests-gen
	cd manifests-gen && go build -o ../bin/manifests-gen && cd ..

operator:
	# building cluster-capi-operator
	go build -o bin/cluster-capi-operator cmd/cluster-capi-operator/main.go

migration:
	# building migration
	go build -o bin/machine-api-migration cmd/machine-api-migration/main.go

.PHONY: localtestenv
localtestenv: .localtestenv

KUBEBUILDER_ASSETS ?= $(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path --bin-dir $(PROJECT_DIR)/bin --index https://raw.githubusercontent.com/openshift/api/master/envtest-releases.yaml)
.localtestenv: Makefile
	echo "KUBEBUILDER_ASSETS=$(KUBEBUILDER_ASSETS)" > $@

TEST_DIRS ?= ./pkg/... ./manifests-gen/...
unit: .localtestenv
	./hack/test.sh "$(TEST_DIRS)" 10m

.PHONY: e2e
e2e:
	./hack/test.sh "./e2e/..." 30m

# Run against the configured Kubernetes cluster in ~/.kube/config
run:
	oc -n openshift-cluster-api patch lease cluster-capi-operator-leader -p '{"spec":{"acquireTime": null, "holderIdentity": null, "renewTime": null}}' --type=merge
	go run cmd/cluster-capi-operator/main.go --images-json=./dev-images.json --leader-elect=true --leader-elect-lease-duration=120s --namespace="openshift-cluster-api" --leader-elect-resource-namespace="openshift-cluster-api"

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

.PHONY: crds-sync
api_module_dir ?= $(shell go list -f '{{.Dir}}' github.com/openshift/api)
crds := operator/v1alpha1/zz_generated.crd-manifests/0000_20_crd-compatibility-checker_01_crdcompatibilityrequirements.crd.yaml
crds-sync: $(crds:%=$(api_module_dir)/%)
	cp $? ./manifests/

aws-cluster:
	./hack/clusters/create-aws.sh

azure-cluster:
	./hack/clusters/create-azure.sh

gcp-cluster:
	./hack/clusters/create-gcp.sh

powervs-cluster:
	./hack/clusters/create-powervs.sh

vsphere-cluster:
	./hack/clusters/create-vsphere.sh

define ensure-home
	@ export HOME=$${HOME:=/tmp/kubebuilder-testing}; \
	if [ $${HOME} == "/" ]; then \
	  export HOME=/tmp/kubebuilder-testing; \
	fi; \
	$(1)
endef

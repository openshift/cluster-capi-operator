IMG ?= controller:latest

all: build

verify-%:
	make $*
	./hack/verify-diff.sh

# Run tests
test: verify unit

# Build operator binaries
build: operator

operator:
	go build -o bin/meta-cluster-api-operator cmd/meta-cluster-api-operator/main.go

unit:
	hack/unit-tests.sh

# Run against the configured Kubernetes cluster in ~/.kube/config
run: verify
	go run cmd/meta-cluster-api-operator/main.go

# Run go fmt against code
.PHONY: fmt
fmt:
	go fmt ./...

# Run go vet against code
.PHONY: vet
vet:
	go vet ./...

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

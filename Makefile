DIR := ${CURDIR}

# Image URL to use all building/pushing image targets
IMG ?= hypershift:latest

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true"

# Runtime CLI to use for building and pushing images
RUNTIME ?= docker

CONTROLLER_GEN=GO111MODULE=on GOFLAGS=-mod=vendor go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen

GO_GCFLAGS ?= -gcflags=all='-N -l'
GO=GO111MODULE=on GOFLAGS=-mod=vendor go
GO_BUILD_RECIPE=CGO_ENABLED=0 $(GO) build $(GO_GCFLAGS)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

all: build

build: hypershift-operator control-plane-operator hosted-cluster-config-operator hypershift

verify: build fmt vet

# Generate Kube manifests (e.g. CRDs)
.PHONY: hypershift-operator-manifests
hypershift-operator-manifests:
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./..." output:crd:artifacts:config=cmd/install/assets/hypershift-operator

# Build hypershift-operator binary
.PHONY: hypershift-operator
hypershift-operator:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."
	$(GO_BUILD_RECIPE) -o bin/hypershift-operator ./hypershift-operator

.PHONY: control-plane-operator
control-plane-operator:
	$(GO_BUILD_RECIPE) -o bin/control-plane-operator ./control-plane-operator

# Build hosted-cluster-config-operator binary
.PHONY: hosted-cluster-config-operator
hosted-cluster-config-operator:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."
	$(GO_BUILD_RECIPE) -o bin/hosted-cluster-config-operator ./hosted-cluster-config-operator

.PHONY: hypershift
hypershift:
	$(GO_BUILD_RECIPE) -o bin/hypershift .

# Run tests
.PHONY: test
test: build
	$(GO) test ./... -coverprofile cover.out

.PHONY: e2e
e2e:
	$(GO) test -tags e2e -c -o bin/test-e2e ./test/e2e

# Run go fmt against code
.PHONY: fmt
fmt:
	$(GO) fmt ./...

# Run go vet against code
.PHONY: vet
vet:
	$(GO) vet ./...

# Build the docker image
.PHONY: docker-build
docker-build:
	${RUNTIME} build . -t ${IMG}

# Push the docker image
.PHONY: docker-push
docker-push:
	${RUNTIME} push ${IMG}

.PHONY: run-local
run-local:
	bin/hypershift-operator run --operator-image=$(IMAGE)

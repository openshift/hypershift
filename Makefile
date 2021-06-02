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

all: build e2e

build: ignition-server hypershift-operator control-plane-operator hosted-cluster-config-operator hypershift

.PHONY: verify
verify: deps api fmt vet
	git diff-index --cached --quiet --ignore-submodules HEAD --
	git diff-files --quiet --ignore-submodules
	$(eval STATUS = $(shell git status -s))
	$(if $(strip $(STATUS)),$(error untracked files detected))

# Build ignition-server binary
.PHONY: ignition-server
ignition-server:
	$(GO_BUILD_RECIPE) -o bin/ignition-server ./ignition-server

# Build hypershift-operator binary
.PHONY: hypershift-operator
hypershift-operator:
	$(GO_BUILD_RECIPE) -o bin/hypershift-operator ./hypershift-operator

.PHONY: control-plane-operator
control-plane-operator:
	$(GO_BUILD_RECIPE) -o bin/control-plane-operator ./control-plane-operator

# Build hosted-cluster-config-operator binary
.PHONY: hosted-cluster-config-operator
hosted-cluster-config-operator:
	$(GO_BUILD_RECIPE) -o bin/hosted-cluster-config-operator ./hosted-cluster-config-operator

.PHONY: hypershift
hypershift:
	$(GO_BUILD_RECIPE) -o bin/hypershift .

# Run this when updating any of the types in the api package to regenerate the
# deepcopy code and CRD manifest files.
.PHONY: api
api:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./api/..." output:crd:artifacts:config=cmd/install/assets/hypershift-operator

# Target to generate deepcopy code and CRDs for etcd types in thirdparty package
.PHONY: etcd-api
etcd-api:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./thirdparty/etcd/..."
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./thirdparty/etcd/..." output:crd:artifacts:config=cmd/install/assets/etcd

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

# Updates Go modules
.PHONY: deps
deps:
	$(GO) mod tidy
	$(GO) mod vendor
	$(GO) mod verify

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

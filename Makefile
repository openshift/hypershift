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

# Change HOME to writeable location in CI for staticcheck
ifeq ("/","${HOME}")
HOME=/tmp
endif

all: build e2e

build: ignition-server hypershift-operator control-plane-operator hosted-cluster-config-operator hypershift

.PHONY: verify
verify: staticcheck deps api fmt vet
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
api: hypershift-api cluster-api cluster-api-provider-aws cluster-api-provider-ibmcloud etcd-api

.PHONY: hypershift-api
hypershift-api:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
	rm -rf cmd/install/assets/hypershift-operator/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./api/..." output:crd:artifacts:config=cmd/install/assets/hypershift-operator

.PHONY: cluster-api
cluster-api:
	rm -rf cmd/install/assets/cluster-api/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api/api/v1alpha4" output:crd:artifacts:config=cmd/install/assets/cluster-api
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api/exp/api/v1alpha4" output:crd:artifacts:config=cmd/install/assets/cluster-api
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api/exp/addons/api/v1alpha4" output:crd:artifacts:config=cmd/install/assets/cluster-api

.PHONY: cluster-api-provider-aws
cluster-api-provider-aws:
	rm -rf cmd/install/assets/cluster-api-provider-aws/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-aws/api/v1alpha4" output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-aws
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-aws/exp/api/v1alpha4" output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-aws

.PHONY: cluster-api-provider-ibmcloud
cluster-api-provider-ibmcloud:
	rm -rf cmd/install/assets/cluster-api-provider-ibmcloud/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/api/v1alpha4" output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-ibmcloud

.PHONY: etcd-api
etcd-api:
	rm -rf cmd/install/assets/etcd/*.yaml
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

# Run staticcheck
# How to ignore failures https://staticcheck.io/docs/configuration#line-based-linter-directives
.PHONY: staticcheck
staticcheck:
	HOME=$(HOME) $(GO) run honnef.co/go/tools/cmd/staticcheck ./control-plane-operator/controllers/... ./hypershift-operator/controllers/... ./ignition-server/... ./hosted-cluster-config-operator/... ./cmd/... ./support/certs/... ./support/releaseinfo/...

# Build the docker image with official golang image
.PHONY: docker-build
docker-build:
	${RUNTIME} build . -t ${IMG}

# Build the docker image copying binaries from workspace
.PHONY: docker-build-fast
docker-build-fast: build
	${RUNTIME} build . -t ${IMG} -f Dockerfile.fast

# Push the docker image
.PHONY: docker-push
docker-push:
	${RUNTIME} push ${IMG}

.PHONY: run-local
run-local:
	bin/hypershift-operator run

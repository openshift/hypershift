DIR := ${CURDIR}

# Image URL to use all building/pushing image targets
IMG ?= hypershift:latest

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true"

# Runtime CLI to use for building and pushing images
RUNTIME ?= docker

TOOLS_DIR=./hack/tools
BIN_DIR=bin
TOOLS_BIN_DIR := $(TOOLS_DIR)/$(BIN_DIR)
CONTROLLER_GEN := $(abspath $(TOOLS_BIN_DIR)/controller-gen)
STATICCHECK := $(abspath $(TOOLS_BIN_DIR)/staticcheck)
GENAPIDOCS := $(abspath $(TOOLS_BIN_DIR)/gen-crd-api-reference-docs)

PROMTOOL=GO111MODULE=on GOFLAGS=-mod=vendor go run github.com/prometheus/prometheus/cmd/promtool

GO_GCFLAGS ?= -gcflags=all='-N -l'
GO=GO111MODULE=on GOFLAGS=-mod=vendor go
GO_BUILD_RECIPE=CGO_ENABLED=0 $(GO) build $(GO_GCFLAGS)

OUT_DIR ?= bin

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

build: ignition-server hypershift-operator control-plane-operator hosted-cluster-config-operator konnectivity-socks5-proxy hypershift availability-prober token-minter

.PHONY: verify
verify: staticcheck deps api fmt vet promtool
	git diff-index --cached --quiet --ignore-submodules HEAD --
	git diff-files --quiet --ignore-submodules
	$(eval STATUS = $(shell git status -s))
	$(if $(strip $(STATUS)),$(error untracked files detected: ${STATUS}))

$(CONTROLLER_GEN): $(TOOLS_DIR)/go.mod # Build controller-gen from tools folder.
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=vendor go build -tags=tools -o $(BIN_DIR)/controller-gen sigs.k8s.io/controller-tools/cmd/controller-gen

$(STATICCHECK): $(TOOLS_DIR)/go.mod # Build staticcheck from tools folder.
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=vendor go build -tags=tools -o $(BIN_DIR)/staticcheck honnef.co/go/tools/cmd/staticcheck

$(GENAPIDOCS): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=vendor go build -tags=tools -o $(GENAPIDOCS) github.com/ahmetb/gen-crd-api-reference-docs

# Build ignition-server binary
.PHONY: ignition-server
ignition-server:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/ignition-server ./ignition-server

# Build hypershift-operator binary
.PHONY: hypershift-operator
hypershift-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/hypershift-operator ./hypershift-operator

.PHONY: control-plane-operator
control-plane-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/control-plane-operator ./control-plane-operator

# Build hosted-cluster-config-operator binary
.PHONY: hosted-cluster-config-operator
hosted-cluster-config-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/hosted-cluster-config-operator ./hosted-cluster-config-operator

# Build konnectivity-socks5-proxy binary
.PHONY: konnectivity-socks5-proxy
konnectivity-socks5-proxy:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/konnectivity-socks5-proxy ./konnectivity-socks5-proxy

.PHONY: hypershift
hypershift:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/hypershift .

.PHONY: availability-prober
availability-prober:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/availability-prober ./availability-prober

.PHONY: token-minter
token-minter:
	$(GO_BUILD_RECIPE) -o bin/token-minter ./token-minter

# Run this when updating any of the types in the api package to regenerate the
# deepcopy code and CRD manifest files.
.PHONY: api
api: hypershift-api cluster-api cluster-api-provider-aws cluster-api-provider-ibmcloud api-docs

.PHONY: hypershift-api
hypershift-api: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."
	rm -rf cmd/install/assets/hypershift-operator/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./api/..." output:crd:artifacts:config=cmd/install/assets/hypershift-operator

.PHONY: cluster-api
cluster-api: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api/exp/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api/exp/addons/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api

.PHONY: cluster-api-provider-aws
cluster-api-provider-aws: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-aws/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-aws/api/v1beta1" output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-aws
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-aws/exp/api/v1beta1" output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-aws

.PHONY: cluster-api-provider-ibmcloud
cluster-api-provider-ibmcloud: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-ibmcloud/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/github.com/kubernetes-sigs/cluster-api-provider-ibmcloud/api/v1alpha4" output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-ibmcloud

.PHONY: api-docs
api-docs: $(GENAPIDOCS)
	hack/gen-api-docs.sh $(GENAPIDOCS) $(DIR)

# Run tests
.PHONY: test
test: build
	$(GO) test ./... -coverprofile cover.out

.PHONY: e2e
e2e:
	$(GO) test -tags e2e -c -o bin/test-e2e ./test/e2e
	$(GO_BUILD_RECIPE) -o bin/test-setup ./test/setup

# Run go fmt against code
.PHONY: fmt
fmt:
	$(GO) fmt ./...

# Run go vet against code
.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: promtool
promtool:
	cd $(TOOLS_DIR); $(PROMTOOL) check rules ../../cmd/install/assets/recordingrules/*.yaml

# Updates Go modules
.PHONY: deps
deps:
	$(GO) mod tidy
	$(GO) mod vendor
	$(GO) mod verify

# Run staticcheck
# How to ignore failures https://staticcheck.io/docs/configuration#line-based-linter-directives
.PHONY: staticcheck
staticcheck: $(STATICCHECK)
	$(STATICCHECK) \
		./control-plane-operator/controllers/... \
		./hypershift-operator/controllers/... \
		./ignition-server/... \
		./hosted-cluster-config-operator/... \
		./cmd/... \
		./support/certs/... \
		./support/releaseinfo/... \
		./support/upsert/... \
		./konnectivity-socks5-proxy/... \
		./availability-prober/...

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

.PHONY: ci-install-hypershift
ci-install-hypershift:
	bin/hypershift install --hypershift-image $(HYPERSHIFT_RELEASE_LATEST) \
		--oidc-storage-provider-s3-credentials=/etc/hypershift-pool-aws-credentials/credentials \
		--oidc-storage-provider-s3-bucket-name=hypershift-ci-oidc \
		--oidc-storage-provider-s3-region=us-east-1

.PHONY: ci-install-hypershift-private
ci-install-hypershift-private:
	bin/hypershift install --hypershift-image $(HYPERSHIFT_RELEASE_LATEST) \
		--oidc-storage-provider-s3-credentials=/etc/hypershift-pool-aws-credentials/credentials \
		--oidc-storage-provider-s3-bucket-name=hypershift-ci-oidc \
		--oidc-storage-provider-s3-region=us-east-1 \
		--private-platform=AWS \
		--aws-private-creds=/etc/hypershift-pool-aws-credentials/credentials \
		--aws-private-region=us-east-1

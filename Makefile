DIR := ${CURDIR}

# Image URL to use all building/pushing image targets
IMG ?= hypershift:latest

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd"

# Runtime CLI to use for building and pushing images
RUNTIME ?= $(shell sh hack/utils.sh get_container_engine)

TOOLS_DIR=./hack/tools
BIN_DIR=bin
TOOLS_BIN_DIR := $(TOOLS_DIR)/$(BIN_DIR)
CONTROLLER_GEN := $(abspath $(TOOLS_BIN_DIR)/controller-gen)
STATICCHECK := $(abspath $(TOOLS_BIN_DIR)/staticcheck)
GENAPIDOCS := $(abspath $(TOOLS_BIN_DIR)/gen-crd-api-reference-docs)

PROMTOOL=$(abspath $(TOOLS_BIN_DIR)/promtool)

GO_GCFLAGS ?= -gcflags=all='-N -l'
GO=GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go
GO_BUILD_RECIPE=CGO_ENABLED=1 $(GO) build $(GO_GCFLAGS)
GO_CLI_RECIPE=CGO_ENABLED=0 $(GO) build $(GO_GCFLAGS) -ldflags '-extldflags "-static"'
GO_E2E_RECIPE=CGO_ENABLED=1 $(GO) test $(GO_GCFLAGS) -tags e2e -c

OUT_DIR ?= bin

# run the HO locally
HYPERSHIFT_INSTALL_AWS := ./hack/dev/aws/hypershft-install-aws.sh
RUN_OPERATOR_LOCALLY_AWS := ./hack/dev/aws/run-operator-locally-aws.sh

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

all: build e2e tests

pre-commit: all verify test

build: hypershift-operator control-plane-operator control-plane-pki-operator hypershift product-cli

.PHONY: update
update: api-deps api api-docs deps clients

.PHONY: verify
verify: update staticcheck fmt vet
	git diff-index --cached --quiet --ignore-submodules HEAD --
	git diff-files --quiet --ignore-submodules
	git diff --exit-code HEAD --
	$(eval STATUS = $(shell git status -s))
	$(if $(strip $(STATUS)),$(error untracked files detected: ${STATUS}))

$(CONTROLLER_GEN): $(TOOLS_DIR)/go.mod # Build controller-gen from tools folder.
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=vendor GOWORK=off go build -tags=tools -o $(BIN_DIR)/controller-gen sigs.k8s.io/controller-tools/cmd/controller-gen

$(STATICCHECK): $(TOOLS_DIR)/go.mod # Build staticcheck from tools folder.
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=vendor GOWORK=off go build -tags=tools -o $(BIN_DIR)/staticcheck honnef.co/go/tools/cmd/staticcheck

$(GENAPIDOCS): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=vendor GOWORK=off go build -tags=tools -o $(GENAPIDOCS) github.com/ahmetb/gen-crd-api-reference-docs

# Compile all tests
.PHONY: tests
tests:
	$(GO) test -o /dev/null -c ./...

# Build hypershift-operator binary
.PHONY: hypershift-operator
hypershift-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/hypershift-operator ./hypershift-operator

.PHONY: control-plane-operator
control-plane-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/control-plane-operator ./control-plane-operator

.PHONY: control-plane-pki-operator
control-plane-pki-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/control-plane-pki-operator ./control-plane-pki-operator

.PHONY: hypershift
hypershift:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/hypershift .

.PHONY: product-cli
product-cli:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/hcp ./product-cli

.PHONY: product-cli-release
product-cli-release:
	@for OS in linux; do for ARCH in amd64 arm64 ppc64 ppc64le s390x; do \
		echo "# Building $${OS}/$${ARCH}/hcp"; \
		GOOS=$${OS} GOARCH=$${ARCH} $(GO_CLI_RECIPE) -o $(OUT_DIR)/$${OS}/$${ARCH}/hcp ./product-cli \
			|| exit 1; \
		done; \
	done
	@for OS in darwin windows; do for ARCH in amd64 arm64; do \
		echo "# Building $${OS}/$${ARCH}/hcp"; \
		GOOS=$${OS} GOARCH=$${ARCH} $(GO_CLI_RECIPE) -o $(OUT_DIR)/$${OS}/$${ARCH}/hcp ./product-cli \
			|| exit 1; \
		done; \
	done

# Run this when updating any of the types in the api package to regenerate the
# deepcopy code and CRD manifest files.
.PHONY: api
api: hypershift-api cluster-api cluster-api-provider-aws cluster-api-provider-ibmcloud cluster-api-provider-kubevirt cluster-api-provider-agent cluster-api-provider-azure cluster-api-provider-openstack api-docs

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
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api/exp/ipam/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api

.PHONY: cluster-api-provider-aws
cluster-api-provider-aws: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-aws/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-aws/v2/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-aws
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-aws/v2/exp/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-aws
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api/exp/addons/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api

.PHONY: cluster-api-provider-ibmcloud
cluster-api-provider-ibmcloud: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-ibmcloud/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-ibmcloud/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-ibmcloud

.PHONY: cluster-api-provider-kubevirt
cluster-api-provider-kubevirt: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-kubevirt/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1" output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-kubevirt

.PHONY: cluster-api-provider-agent
cluster-api-provider-agent: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-agent/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/github.com/openshift/cluster-api-provider-agent/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-agent

.PHONY: cluster-api-provider-azure
cluster-api-provider-azure: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-azure/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-azure/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-azure

.PHONY: cluster-api-provider-openstack
cluster-api-provider-openstack: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-openstack/*.yaml
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-openstack/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-openstack

.PHONY: api-docs
api-docs: $(GENAPIDOCS)
	hack/gen-api-docs.sh $(GENAPIDOCS) $(DIR)

.PHONY: clients
clients: delegating_client
	GO=GO111MODULE=on GOFLAGS=-mod=readonly hack/update-codegen.sh

.PHONY: release
release:
	go run ./hack/tools/release/notes.go --from=${FROM} --to=${TO} --token=${TOKEN}

.PHONY: delegating_client
delegating_client:
	go run ./cmd/infra/aws/delegatingclientgenerator/main.go > ./cmd/infra/aws/delegating_client.txt
	mv ./cmd/infra/aws/delegating_client.{txt,go}

# Run tests
.PHONY: test
test:
	$(GO) test -race -count=25 -timeout=30m ./... -coverprofile cover.out

.PHONY: e2e
e2e:
	$(GO_E2E_RECIPE) -o bin/test-e2e ./test/e2e
	$(GO_BUILD_RECIPE) -o bin/test-setup ./test/setup
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=vendor GOWORK=off go build -tags=tools -o ../../bin/gotestsum gotest.tools/gotestsum

# Run go fmt against code
.PHONY: fmt
fmt:
	$(GO) fmt ./...

# Run go vet against code
.PHONY: vet
vet:
	$(GO) vet -tags integration,e2e ./...

# jparrill: The RHTAP tool is breaking the RHTAP builds from Feb 27th, so we're stop using it for now
# more info here https://redhat-internal.slack.com/archives/C031USXS2FJ/p1710177462151639
#.PHONY: build-promtool
#build-promtool:
#	cd $(TOOLS_DIR); $(GO) build -o $(PROMTOOL) github.com/prometheus/prometheus/cmd/promtool
#
#.PHONY: promtool
#promtool: build-promtool
#	cd $(TOOLS_DIR); $(PROMTOOL) check rules ../../cmd/install/assets/slos/*.yaml ../../cmd/install/assets/recordingrules/*.yaml ../../control-plane-operator/controllers/hostedcontrolplane/kas/assets/*.yaml

# Updates Go modules
.PHONY: deps
deps:
	$(GO) mod tidy
	$(GO) mod vendor
	$(GO) mod verify
	$(GO) list -m -mod=readonly -json all > /dev/null
	(cd hack/tools && $(GO) mod tidy && $(GO) mod vendor && $(GO) mod verify && $(GO) list -m -mod=readonly -json all > /dev/null)

.PHONY: api-deps
api-deps:
	cd api && \
	  $(GO) mod tidy && \
	  $(GO) mod vendor && \
	  $(GO) mod verify && \
	  $(GO) list -m -mod=readonly -json all > /dev/null


# Run staticcheck
# How to ignore failures https://staticcheck.io/docs/configuration#line-based-linter-directives
.PHONY: staticcheck
staticcheck: $(STATICCHECK)
	$(STATICCHECK) \
		./control-plane-operator/... \
		./control-plane-pki-operator/... \
		./hypershift-operator/controllers/... \
		./ignition-server/... \
		./cmd/... \
		./support/certs/... \
		./support/releaseinfo/... \
		./support/upsert/... \
		./konnectivity-socks5-proxy/... \
		./contrib/... \
		./availability-prober/... \
		./test/integration/... \

# Build the docker image with official golang image
.PHONY: docker-build
docker-build:
	${RUNTIME} build . -t ${IMG}

.PHONY: fast.Dockerfile.dockerignore
fast.Dockerfile.dockerignore:
	sed -e '/^bin\//d' .dockerignore > fast.Dockerfile.dockerignore

# Build the docker image copying binaries from workspace
.PHONY: docker-build-fast
docker-build-fast: build fast.Dockerfile.dockerignore
ifeq ($(RUNTIME),podman)
		${RUNTIME} build . -t ${IMG} -f fast.Dockerfile --ignorefile fast.Dockerfile.dockerignore
else
		DOCKER_BUILDKIT=1 ${RUNTIME} build . -t ${IMG} -f fast.Dockerfile
endif

# Push the docker image
.PHONY: docker-push
docker-push:
	${RUNTIME} push ${IMG}

.PHONY: run-local
run-local:
	bin/hypershift-operator run

.PHONY: ci-install-hypershift
ci-install-hypershift: ci-install-hypershift-private

.PHONY: ci-install-hypershift-private
ci-install-hypershift-private:
	bin/hypershift install --hypershift-image $(HYPERSHIFT_RELEASE_LATEST) \
		--oidc-storage-provider-s3-credentials=/etc/hypershift-pool-aws-credentials/credentials \
		--oidc-storage-provider-s3-bucket-name=hypershift-ci-oidc \
		--oidc-storage-provider-s3-region=us-east-1 \
		--private-platform=AWS \
		--aws-private-creds=/etc/hypershift-pool-aws-credentials/credentials \
		--aws-private-region=us-east-1 \
		--external-dns-provider=aws \
		--external-dns-credentials=/etc/hypershift-pool-aws-credentials/credentials \
		--external-dns-domain-filter=service.ci.hypershift.devcluster.openshift.com \
		--wait-until-available

.PHONY: regenerate-pki
regenerate-pki:
	REGENERATE_PKI=1 $(GO) test ./control-plane-pki-operator/...
	REGENERATE_PKI=1 $(GO) test ./test/e2e/... -run TestRegeneratePKI

.PHONY: hypershift-install-aws-dev
hypershift-install-aws-dev:
	@$(HYPERSHIFT_INSTALL_AWS)

.PHONY: run-operator-locally-aws-dev
run-operator-locally-aws-dev:
	@$(RUN_OPERATOR_LOCALLY_AWS)

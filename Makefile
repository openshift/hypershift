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
CG_ENV := GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor
CODE_GEN := $(abspath $(TOOLS_BIN_DIR)/codegen)
# Standard and plugin golangci-lint binaries
GOLANGCI_LINT := $(abspath $(TOOLS_BIN_DIR)/golangci-lint)
GOLANGCI_LINT_PLUGIN := $(abspath $(TOOLS_BIN_DIR)/golangci-kube-api-linter)
STATICCHECK := $(abspath $(TOOLS_BIN_DIR)/staticcheck)
GENAPIDOCS := $(abspath $(TOOLS_BIN_DIR)/gen-crd-api-reference-docs)
MOCKGEN := $(abspath $(TOOLS_BIN_DIR)/mockgen)

CODESPELL_VER := 2.4.1
CODESPELL_BIN := codespell
CODESPELL_DIST_DIR := codespell_dist
CODESPELL := $(TOOLS_BIN_DIR)/$(CODESPELL_DIST_DIR)/$(CODESPELL_BIN)

GITLINT_VER := 0.19.1
GITLINT_DIST_DIR := gitlint_dist
GITLINT_BIN := gitlint
GITLINT := $(TOOLS_BIN_DIR)/$(GITLINT_DIST_DIR)/$(GITLINT_BIN)-bin

PROMTOOL=$(abspath $(TOOLS_BIN_DIR)/promtool)

GO_GCFLAGS ?= -gcflags=all='-N -l'
GO=GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go
GOWS=GO111MODULE=on GOWORK=$(shell pwd)/hack/workspace/go.work GOFLAGS=-mod=vendor go
GO_BUILD_RECIPE=CGO_ENABLED=1 $(GO) build $(GO_GCFLAGS)
GO_CLI_RECIPE=CGO_ENABLED=0 $(GO) build $(GO_GCFLAGS) -ldflags '-extldflags "-static"'
GO_E2E_RECIPE=CGO_ENABLED=1 $(GO) test $(GO_GCFLAGS) -tags e2e -c

OUT_DIR ?= bin

# run the HO locally
HYPERSHIFT_INSTALL_AWS := ./hack/dev/aws/hypershft-install-aws.sh
RUN_OPERATOR_LOCALLY_AWS := ./hack/dev/aws/run-operator-locally-aws-dev.sh

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

build: hypershift-operator control-plane-operator control-plane-pki-operator karpenter-operator hypershift product-cli

.PHONY: update
update: api-deps workspace-sync deps api api-docs clients

$(GOLANGCI_LINT): $(TOOLS_DIR)/go.mod # Build standard golangci-lint
	cd $(TOOLS_DIR) && GO111MODULE=on GOFLAGS=-mod=mod GOWORK=off \
		go build -tags=tools -o bin/golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint

$(GOLANGCI_LINT_PLUGIN): $(TOOLS_DIR)/go.mod # Build custom golangci-lint with kube-api-linter plugin
	# Ensure standard golangci-lint exists
	@if [ ! -f "$(TOOLS_BIN_DIR)/golangci-lint" ]; then \
		echo "Building standard golangci-lint v2..."; \
		cd $(TOOLS_DIR) && GO111MODULE=on GOFLAGS=-mod=mod GOWORK=off \
			go build -tags=tools -o bin/golangci-lint github.com/golangci/golangci-lint/v2/cmd/golangci-lint; \
	fi
	# Build custom binary with kubeapilinter module plugin
	cp .custom-gcl.yml $(TOOLS_DIR)/.custom-gcl.yml
	cd $(TOOLS_DIR) && GO111MODULE=on GOFLAGS=-mod=mod GOWORK=off ./bin/golangci-lint custom -v

.PHONY: lint
lint: $(GOLANGCI_LINT_PLUGIN)
	$(GOLANGCI_LINT_PLUGIN) run --config ./.golangci.yml -v

.PHONY: lint-api
# Run only kubeapilinter against the API directory using plugin binary
lint-api: $(GOLANGCI_LINT_PLUGIN)
	cd api && $(GOLANGCI_LINT_PLUGIN) run --config ../.golangci-kubeapi.yml -v ./...

.PHONY: lint-fix
lint-fix: $(GOLANGCI_LINT_PLUGIN)
	$(GOLANGCI_LINT_PLUGIN) run --config ./.golangci.yml --fix -v

.PHONY: verify
verify: generate update staticcheck fmt vet verify-codespell lint lint-api cpo-container-sync run-gitlint
	git diff-index --cached --quiet --ignore-submodules HEAD --
	git diff-files --quiet --ignore-submodules
	git diff --exit-code HEAD --
	$(eval STATUS = $(shell git status -s))
	$(if $(strip $(STATUS)),$(error untracked files detected: ${STATUS}))

$(CONTROLLER_GEN): $(TOOLS_DIR)/go.mod # Build controller-gen from tools folder.
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=mod GOWORK=off go build -tags=tools -o $(BIN_DIR)/controller-gen sigs.k8s.io/controller-tools/cmd/controller-gen

$(CODE_GEN): $(TOOLS_DIR)/go.mod # Build code-gen from tools folder.
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=mod GOWORK=off go build -tags=tools -o $(BIN_DIR)/codegen github.com/openshift/api/tools/codegen/cmd


$(STATICCHECK): $(TOOLS_DIR)/go.mod # Build staticcheck from tools folder.
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=mod GOWORK=off go build -tags=tools -o $(BIN_DIR)/staticcheck honnef.co/go/tools/cmd/staticcheck

$(GENAPIDOCS): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=mod GOWORK=off go build -tags=tools -o $(GENAPIDOCS) github.com/ahmetb/gen-crd-api-reference-docs

$(MOCKGEN): ${TOOLS_DIR}/go.mod
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=mod GOWORK=off go build -tags=tools -o $(BIN_DIR)/mockgen go.uber.org/mock/mockgen


#.PHONY: generate
generate: $(MOCKGEN)
	GO111MODULE=on GOFLAGS=-mod=vendor GOWORK=off go generate ./...

# Compile all tests
.PHONY: tests
tests: generate
	$(GO) test -o /dev/null -c ./...

# Build hypershift-operator binary
.PHONY: hypershift-operator
hypershift-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/hypershift-operator ./hypershift-operator

.PHONY: karpenter-operator
karpenter-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/karpenter-operator ./karpenter-operator

.PHONY: karpenter-api
karpenter-api:
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./api/karpenter/..." output:crd:artifacts:config=karpenter-operator/controllers/karpenter/assets

.PHONY: control-plane-operator
control-plane-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/control-plane-operator ./control-plane-operator

.PHONY: control-plane-pki-operator
control-plane-pki-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/control-plane-pki-operator ./control-plane-pki-operator

.PHONY: hypershift
hypershift:
	$(GO_CLI_RECIPE) -o $(OUT_DIR)/hypershift .

.PHONY: product-cli
product-cli:
	$(GO_CLI_RECIPE) -o $(OUT_DIR)/hcp ./product-cli

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
api: hypershift-api cluster-api cluster-api-provider-aws cluster-api-provider-ibmcloud cluster-api-provider-kubevirt cluster-api-provider-agent cluster-api-provider-azure cluster-api-provider-openstack karpenter-api api-docs

.PHONY: hypershift-api
hypershift-api: $(CONTROLLER_GEN) $(CODE_GEN)
	# Clean up autogenerated files.
	rm -rf ./api/hypershift/v1beta1/zz_generated.featuregated-crd-manifests
	rm -rf ./api/hypershift/v1beta1/zz_generated.featuregated-crd-manifests.yaml
	rm -rf cmd/install/assets/hypershift-operator/*

	$(CG_ENV) $(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

	# These consolidate with the 3 steps used to generate CRDs by openshift/api.
	$(CG_ENV) $(CODE_GEN) empty-partial-schemas --base-dir ./api/hypershift/v1beta1
	$(CG_ENV) $(CODE_GEN) schemapatch --base-dir ./api/hypershift/v1beta1
	$(CG_ENV) $(CODE_GEN) crd-manifest-merge --manifest-merge:payload-manifest-path ./api/hypershift/v1beta1/featuregates --base-dir ./api/hypershift/v1beta1

	# Move final CRDs to the install folder.
	mv ./api/hypershift/v1beta1/zz_generated.crd-manifests cmd/install/assets/hypershift-operator/

	# Generate additional CRDs.
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./api/scheduling/..." output:crd:artifacts:config=cmd/install/assets/hypershift-operator
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./api/certificates/..." output:crd:artifacts:config=cmd/install/assets/hypershift-operator

.PHONY: cluster-api
cluster-api: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api/*.yaml
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api/exp/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api/exp/ipam/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api

.PHONY: cluster-api-provider-aws
cluster-api-provider-aws: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-aws/*.yaml
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-aws/v2/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-aws
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-aws/v2/exp/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-aws
# remove ROSA CRDs
	rm -rf cmd/install/assets/cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_rosa*.yaml
# remove EKS CRDs
	rm -rf cmd/install/assets/cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsmanaged*.yaml
	rm -rf cmd/install/assets/cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsfargateprofiles.yaml

.PHONY: cluster-api-provider-ibmcloud
cluster-api-provider-ibmcloud: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-ibmcloud/*.yaml
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-ibmcloud/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-ibmcloud

.PHONY: cluster-api-provider-kubevirt
cluster-api-provider-kubevirt: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-kubevirt/*.yaml
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1" output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-kubevirt

.PHONY: cluster-api-provider-agent
cluster-api-provider-agent: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-agent/*.yaml
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/github.com/openshift/cluster-api-provider-agent/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-agent

.PHONY: cluster-api-provider-azure
cluster-api-provider-azure: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-azure/*.yaml
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-azure/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-azure
# remove CAPZ managed CRDS
	rm -rf cmd/install/assets/cluster-api-provider-azure/infrastructure.cluster.x-k8s.io_azuremanaged*.yaml

.PHONY: cluster-api-provider-openstack
cluster-api-provider-openstack: $(CONTROLLER_GEN)
	rm -rf cmd/install/assets/cluster-api-provider-openstack/*.yaml
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/sigs.k8s.io/cluster-api-provider-openstack/api/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-openstack
	$(CG_ENV) $(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./vendor/github.com/k-orc/openstack-resource-controller/..." output:crd:artifacts:config=cmd/install/assets/cluster-api-provider-openstack

.PHONY: api-docs
api-docs: $(GENAPIDOCS)
	hack/gen-api-docs.sh $(GENAPIDOCS) $(DIR)

.PHONY: clients
clients: delegating_client
	GOWORK=off GO=GO111MODULE=on GOFLAGS=-mod=readonly hack/update-codegen.sh

.PHONY: release
release:
	go run ./hack/tools/release/notes.go --from=${FROM} --to=${TO} --token=${TOKEN}

.PHONY: delegating_client
delegating_client:
	$(GO) run ./cmd/infra/aws/delegatingclientgenerator/main.go > ./cmd/infra/aws/delegating_client.txt
	mv ./cmd/infra/aws/delegating_client.txt ./cmd/infra/aws/delegating_client.go
	$(GO) fmt ./cmd/infra/aws/delegating_client.go

# Run tests
.PHONY: test

# Determine the number of CPU cores
NUM_CORES := $(shell uname | grep -q 'Darwin' && sysctl -n hw.ncpu || nproc)

test: generate
	echo "Running tests with $(NUM_CORES) parallel jobs..."
	$(GO) test -race -parallel=$(NUM_CORES) -count=1 -timeout=30m ./... -coverprofile cover.out

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

.PHONY: api-deps
api-deps:
	cd api && \
	  $(GO) mod tidy && \
	  $(GO) mod vendor && \
	  $(GO) mod verify && \
	  $(GO) list -m -mod=readonly -json all > /dev/null

.PHONY: workspace-sync
workspace-sync:
	cd hack/workspace && \
	  $(GOWS) work sync

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

# Push the docker image
.PHONY: docker-push
docker-push:
	${RUNTIME} push ${IMG}

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

.PHONY: verify-codespell
verify-codespell: codespell ## Verify codespell.
	@$(CODESPELL) --count --ignore-words=./.codespellignore --skip="./hack/tools/bin/codespell_dist,./docs/site/*,./vendor/*,./api/vendor/*,./hack/tools/vendor/*,./api/hypershift/v1alpha1/*,./support/thirdparty/*,./docs/content/reference/*,./hack/tools/bin/*,./cmd/install/assets/*,./go.sum,./hack/workspace/go.work.sum,./api/hypershift/v1beta1/zz_generated.featuregated-crd-manifests,./hack/tools/go.mod,./hack/tools/go.sum"

.PHONY: run-gitlint
run-gitlint: $(GITLINT)
ifdef PULL_BASE_SHA
	@echo "Linting commits from $(PULL_BASE_SHA) to $(PULL_PULL_SHA) (CI: PR targeting $(PULL_BASE_SHA))"
	@$(GITLINT) --commits $(PULL_BASE_SHA)..$(PULL_PULL_SHA)
else
	$(eval MERGE_BASE := $(shell \
		git merge-base HEAD origin/HEAD 2>/dev/null || \
		git merge-base HEAD origin/main 2>/dev/null || \
		git merge-base HEAD origin/master 2>/dev/null || \
		echo "HEAD~1" \
	))
	@echo "Linting commits from $(MERGE_BASE) to HEAD (local development)"
	@$(GITLINT) --commits $(MERGE_BASE)..HEAD
endif

.PHONY: cpo-container-sync
cpo-container-sync:
	@echo "Syncing CPO container images"
	./hack/tools/git-hooks/cpo-containerfiles-in-sync.sh ./Containerfile.control-plane ./Dockerfile.control-plane

## Run Karpenter upstream e2e tests. Requires:
# - KARPENTER_CORE_DIR to be set to the path of the karpenter-core repository
# - current context to be set to a hosted cluster with AutoNode enabled.
# - an annotation to be applied to the HCP to stop reconcillation (hypershift.openshift.io/karpenter-core-e2e-override=true)
.PHONY: karpenter-upstream-e2e
karpenter-upstream-e2e:
	./karpenter-operator/e2e/upstream-e2e.sh

## --------------------------------------
## Tooling Binaries
## --------------------------------------

##@ codespell
codespell : $(CODESPELL) ## Build a local copy of codespell.
$(CODESPELL): ## Build codespell from tools folder.
		mkdir -p $(TOOLS_BIN_DIR); \
		mkdir -p $(TOOLS_BIN_DIR)/$(CODESPELL_DIST_DIR); \
		mkdir -p $(TOOLS_BIN_DIR)/$(CODESPELL_DIST_DIR)/bin; \
		mkdir -p $(TOOLS_BIN_DIR)/$(CODESPELL_DIST_DIR); \
	 	pip install --target=$(TOOLS_BIN_DIR)/$(CODESPELL_DIST_DIR) $(CODESPELL_BIN)==$(CODESPELL_VER) --upgrade; \
		mv $(TOOLS_BIN_DIR)/$(CODESPELL_DIST_DIR)/bin/$(CODESPELL_BIN) $(TOOLS_BIN_DIR)/$(CODESPELL_DIST_DIR); \
		rm -r $(TOOLS_BIN_DIR)/$(CODESPELL_DIST_DIR)/bin;

##@ gitlint
gitlint : $(GITLINT) ## Install local copy of gitlint
$(GITLINT): $(TOOLS_DIR)/go.mod
	mkdir -p $(TOOLS_BIN_DIR); \
	mkdir -p $(TOOLS_BIN_DIR)/$(GITLINT_DIST_DIR); \
	pip install --target=$(TOOLS_BIN_DIR)/$(GITLINT_DIST_DIR) gitlint==$(GITLINT_VER) --upgrade; \
	cp $(TOOLS_BIN_DIR)/$(GITLINT_DIST_DIR)/bin/$(GITLINT_BIN) $(TOOLS_BIN_DIR)/$(GITLINT_DIST_DIR)/$(GITLINT_BIN)-bin; \
	chmod +x $(TOOLS_BIN_DIR)/$(GITLINT_DIST_DIR)/$(GITLINT_BIN)-bin;
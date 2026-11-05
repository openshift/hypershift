DIR := ${CURDIR}

# Image URL to use all building/pushing image targets
IMG ?= hypershift:latest

# Runtime CLI to use for building and pushing images
RUNTIME ?= $(shell sh hack/utils.sh get_container_engine)

ARTIFACT_DIR ?= /tmp/artifacts
TOOLS_DIR=./hack/tools
BIN_DIR=bin
TOOLS_BIN_DIR := $(TOOLS_DIR)/$(BIN_DIR)
DEADCODE := $(abspath $(TOOLS_BIN_DIR)/deadcode)
DEADCODE_REPORT := $(abspath $(TOOLS_BIN_DIR)/deadcode-report)
DEADCODE_REPORT_SRC := $(wildcard $(TOOLS_DIR)/deadcode-report/*.go)
MOCKGEN := $(abspath $(TOOLS_BIN_DIR)/mockgen)
YQ := $(abspath $(TOOLS_BIN_DIR)/yq)

PYTEST_VER := 8.3.5

PYTHON_VENV := $(TOOLS_BIN_DIR)/python-venv
PYTHON_VENV_STAMP := $(PYTHON_VENV)/.installed

PROMTOOL=$(abspath $(TOOLS_BIN_DIR)/promtool)

# Setup envtest for running tests that require a Kubernetes API server
# SETUP_ENVTEST_VER is the version of setup-envtest to use, matching the version in hack/tools/go.mod
KUBEBUILDER_ENVTEST_KUBERNETES_VERSION ?= 1.34.0
SETUP_ENVTEST_VER := release-0.22
SETUP_ENVTEST := $(abspath $(TOOLS_BIN_DIR)/setup-envtest)
ENVTEST_ASSETS_DIR ?= $(abspath $(TOOLS_BIN_DIR)/envtest)
ENVTEST_OCP_ASSETS_DIR ?= $(abspath $(TOOLS_BIN_DIR)/envtest-ocp)
ENVTEST_KUBE_ASSETS_DIR ?= $(abspath $(TOOLS_BIN_DIR)/envtest-kube)

GO_GCFLAGS ?= -gcflags=all='-N -l'
GO=GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go
COMMIT_HASH ?= $(shell git rev-parse HEAD 2>/dev/null)
VERSION_PKG=github.com/openshift/hypershift/support/supportedversion
GO_LDFLAGS=-ldflags '-X $(VERSION_PKG).commitHash=$(COMMIT_HASH)'
GO_BUILD_RECIPE=CGO_ENABLED=1 $(GO) build $(GO_GCFLAGS) $(GO_LDFLAGS)
GO_CLI_RECIPE=CGO_ENABLED=0 $(GO) build $(GO_GCFLAGS) -ldflags '-X $(VERSION_PKG).commitHash=$(COMMIT_HASH) -extldflags "-static"'
GO_E2E_RECIPE=CGO_ENABLED=1 $(GO) test $(GO_GCFLAGS) -tags e2e -c
GO_E2EV2_RECIPE=CGO_ENABLED=1 $(GO) test $(GO_GCFLAGS) -tags e2ev2 -c
GO_BACKUPRESTORE_E2E_RECIPE=CGO_ENABLED=1 $(GO) test $(GO_GCFLAGS) -tags e2ev2,backuprestore -c
GO_REQSERVING_E2E_RECIPE=CGO_ENABLED=1 $(GO) test $(GO_GCFLAGS) -tags reqserving -c

OUT_DIR ?= bin

# run the HO locally
HYPERSHIFT_INSTALL_AWS := ./hack/dev/aws/hypershft-install-aws.sh
RUN_OPERATOR_LOCALLY_AWS := ./hack/dev/aws/run-operator-locally-aws-dev.sh

OPENSHIFT_CI ?= false
FAIL_FAST ?= true

# Do not fail fast in OpenShift CI, it's expensive to start the cluster, run all tests and report the results.
ifeq ($(OPENSHIFT_CI),true)
	FAIL_FAST = false
endif

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

CAPI_PROVIDERS := agent aws azure gcp ibmcloud kubevirt openstack

.PHONY: update
update:
	$(VERIFY) update

GOLANGCI_LINT := $(abspath $(TOOLS_BIN_DIR)/golangci-lint)
# Recursive Make wildcard keeps even the launcher independent of find.
rwildcard = $(foreach path,$(wildcard $(1)/*),$(call rwildcard,$(path),$(2)) $(filter $(subst *,%,$(2)),$(path)))
GOLANGCI_LINT_SRC = $(call rwildcard,$(TOOLS_DIR)/cmd/golangci-lint,*.go) $(call rwildcard,$(TOOLS_DIR)/hypershiftlinter,*.go)
$(GOLANGCI_LINT): $(TOOLS_DIR)/go.mod $(TOOLS_DIR)/go.sum $(TOOLS_DIR)/vendor/modules.txt $(GOLANGCI_LINT_SRC)
	cd $(TOOLS_DIR); CGO_ENABLED=0 $(GO) build -tags=tools -o $(GOLANGCI_LINT) ./cmd/golangci-lint

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Build the native linter with both custom linters compiled in.

api-lint api-lint-fix lint main-lint-fix lint-fix hypershift-lint-all: export CGO_ENABLED=0

# When not otherwise set, diff/lint against the upstream main branch.
# This is always set in OpenShift CI.
UPSTREAM_REMOTE ?= $(shell git remote -v 2>/dev/null | grep 'openshift/hypershift.*fetch' | head -1 | cut -f1)
PULL_BASE_SHA ?= $(if $(UPSTREAM_REMOTE),$(shell git rev-parse $(UPSTREAM_REMOTE)/main),$(shell git rev-parse main))

.PHONY: api-lint
api-lint: $(GOLANGCI_LINT)
	cd api && $(GOLANGCI_LINT) run --config ./.golangci.yml --modules-download-mode=readonly -v --new-from-rev=${PULL_BASE_SHA}

.PHONY: api-lint-fix
api-lint-fix: $(GOLANGCI_LINT)
	cd api && $(GOLANGCI_LINT) run --config ./.golangci.yml --fix -v --new-from-rev=${PULL_BASE_SHA}

.PHONY: precommit-api-lint-fix
precommit-api-lint-fix: $(GOLANGCI_LINT)
	cd api && $(GOLANGCI_LINT) fmt --config ./.golangci.yml --enable gci $(patsubst api/%,%,$(FILES))

.PHONY: lint
lint:
	$(VERIFY) lint

.PHONY: main-lint-fix
main-lint-fix: generate $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run --config ./.golangci.yml --fix -v $(if $(PULL_BASE_SHA),--new-from-rev=$(PULL_BASE_SHA) --whole-files)

.PHONY: precommit-main-lint-fix
precommit-main-lint-fix: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) fmt --config ./.golangci.yml --enable gci $(FILES)

.PHONY: lint-fix
lint-fix: generate $(GOLANGCI_LINT)
	$(MAKE) api-lint-fix; api_rc=$$?; \
	$(GOLANGCI_LINT) run --config ./.golangci.yml --fix -v; main_rc=$$?; \
	exit $$(( api_rc > main_rc ? api_rc : main_rc ))

.PHONY: hypershift-lint-all
hypershift-lint-all: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run --config ./.golangci.yml --modules-download-mode=readonly -v --enable-only hypershiftlinter

.PHONY: test-linter
test-linter:
	cd $(TOOLS_DIR) && CGO_ENABLED=0 $(GO) test ./hypershiftlinter/... ./cmd/golangci-lint ./verify/... ./crd-schema-check -count=1

.PHONY: verify-git-clean
verify-git-clean:
	$(VERIFY) verify-git-clean

.PHONY: verify-codecov
verify-codecov:
	$(VERIFY) verify-codecov

.PHONY: verify-crd-schema
verify-crd-schema:
	$(VERIFY) verify-crd-schema

# All verification runs use the native Go driver and the tool versions in go.mod.
# verify-ci and verify-quick remain compatibility aliases for existing callers.
VERIFY = GOOS= GOARCH= CGO_ENABLED=0 GOWORK=off GOFLAGS=-mod=vendor go -C $(TOOLS_DIR) run ./cmd/verify

.PHONY: verify verify-ci verify-quick
verify verify-ci verify-quick:
	$(VERIFY) verify

.PHONY: verify-repository
verify-repository: ## Run generation and repository checks; CI runs lint separately.
	$(VERIFY) verify-repository

$(YQ): $(TOOLS_DIR)/go.mod # Build yq v4 from tools folder.
	cd $(TOOLS_DIR); $(GO) build -tags=tools -o $(BIN_DIR)/yq github.com/mikefarah/yq/v4

$(DEADCODE): $(TOOLS_DIR)/go.mod $(TOOLS_DIR)/go.sum $(TOOLS_DIR)/tools.go $(TOOLS_DIR)/vendor/modules.txt
	cd $(TOOLS_DIR); GOOS=$(shell go env GOHOSTOS) GOARCH=$(shell go env GOHOSTARCH) CGO_ENABLED=0 $(GO) build -o $(DEADCODE) golang.org/x/tools/cmd/deadcode

$(DEADCODE_REPORT): $(TOOLS_DIR)/go.mod $(TOOLS_DIR)/go.sum $(TOOLS_DIR)/vendor/modules.txt $(DEADCODE_REPORT_SRC)
	cd $(TOOLS_DIR); GOOS=$(shell go env GOHOSTOS) GOARCH=$(shell go env GOHOSTARCH) CGO_ENABLED=0 $(GO) build -o $(DEADCODE_REPORT) ./deadcode-report

.PHONY: deadcode
deadcode: $(DEADCODE) $(DEADCODE_REPORT)
	# Always replace a cached cross-compiled mockgen before executing it.
	cd $(TOOLS_DIR); GOOS=$(shell go env GOHOSTOS) GOARCH=$(shell go env GOHOSTARCH) CGO_ENABLED=0 $(GO) build -tags=tools -o $(MOCKGEN) go.uber.org/mock/mockgen
	$(MAKE) generate GOOS=$(shell go env GOHOSTOS) GOARCH=$(shell go env GOHOSTARCH)
	$(DEADCODE_REPORT) -artifact-dir='$(subst ','\'',$(value ARTIFACT_DIR))'

.PHONY: deadcode-gate
deadcode-gate: ## Fail if the last deadcode scan reported any candidate.
	@report='$(subst ','\'',$(value ARTIFACT_DIR))/deadcode.txt'; \
	if [ -s "$$report" ]; then \
		echo "dead-code candidates found:"; \
		cat "$$report"; \
		exit 1; \
	fi

.PHONY: verify-deadcode
verify-deadcode: deadcode ## Run a whole-program dead-code scan and fail on any candidate.
	@$(MAKE) --no-print-directory deadcode-gate

.PHONY: generate
generate:
	$(VERIFY) generate

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
# Sync upstream CRDs from vendor, apply OpenShift CEL/schema adjustments, then generate OpenshiftEC2NodeClass from api/karpenter.
karpenter-api:
	$(VERIFY) karpenter-api

.PHONY: control-plane-operator
control-plane-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/control-plane-operator ./control-plane-operator

.PHONY: control-plane-pki-operator
control-plane-pki-operator:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/control-plane-pki-operator ./control-plane-pki-operator

.PHONY: hypershift
hypershift:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/hypershift .

.PHONY: hypershift-no-cgo
hypershift-no-cgo:
	$(GO_CLI_RECIPE) -o $(OUT_DIR)/hypershift-no-cgo .

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
api:
	$(VERIFY) api

.PHONY: hypershift-api
hypershift-api:
	$(VERIFY) hypershift-api

# Provider CRDs are staged by the Go driver before replacing shipped manifests.
.PHONY: cluster-api $(foreach p,$(CAPI_PROVIDERS),cluster-api-provider-$(p))
cluster-api $(foreach p,$(CAPI_PROVIDERS),cluster-api-provider-$(p)):
	$(VERIFY) $@

.PHONY: api-docs
api-docs:
	$(VERIFY) api-docs

.PHONY: clients
clients:
	$(VERIFY) clients

.PHONY: release
release:
	go run ./hack/tools/release/notes.go --from=${FROM} --to=${TO} --token=${TOKEN}

.PHONY: docs-aggregate
docs-aggregate:
	$(VERIFY) docs-aggregate

.PHONY: delegating_client
delegating_client:
	go run ./cmd/infra/aws/delegatingclientgenerator/main.go > ./cmd/infra/aws/delegating_client.txt
	mv ./cmd/infra/aws/delegating_client.txt ./cmd/infra/aws/delegating_client.go
	$(GO) fmt ./cmd/infra/aws/delegating_client.go

# Build setup-envtest from tools folder
$(SETUP_ENVTEST): $(TOOLS_DIR)/go.mod
	cd $(TOOLS_DIR); $(GO) build -tags=tools -o $(SETUP_ENVTEST) sigs.k8s.io/controller-runtime/tools/setup-envtest

.PHONY: setup-envtest
setup-envtest: $(SETUP_ENVTEST) ## Setup envtest binaries (etcd, kube-apiserver)
	$(eval KUBEBUILDER_ASSETS := $(shell $(SETUP_ENVTEST) use --use-env --bin-dir $(ENVTEST_ASSETS_DIR) -p path $(KUBEBUILDER_ENVTEST_KUBERNETES_VERSION)))
	@if [ -z "$(KUBEBUILDER_ASSETS)" ]; then echo "Failed to find kubebuilder assets, see errors above"; exit 1; fi
	@echo "KUBEBUILDER_ASSETS=$(KUBEBUILDER_ASSETS)"

# Run tests
.PHONY: test

# Determine the number of CPU cores
NUM_CORES = $(shell getconf _NPROCESSORS_ONLN || echo 1)
GO_TEST_FLAGS ?= -race

test: generate test-e2ev2-unit
	@echo "Running tests with $(NUM_CORES) parallel jobs..."
	$(GO) test $(GO_TEST_FLAGS) -parallel=$(NUM_CORES) -count=1 -timeout=30m ./... -coverprofile cover.out

.PHONY: test-e2ev2-unit
test-e2ev2-unit:
	$(GO) test $(GO_TEST_FLAGS) -tags=e2ev2 -count=1 -timeout=10m ./test/e2e/v2/internal ./test/e2e/v2/cmd/run-tests
	$(GO) test $(GO_TEST_FLAGS) -tags=e2ev2 -count=1 -timeout=10m -run '^(TestComponentSupportsTLSConfig|TestVerifyIntermediateTLSConnectivityAfterDowngrade|TestImageMirroring.*)$$' ./test/e2e/v2/tests

# Run tests only for Go packages with changes relative to PULL_BASE_SHA.
# Skips entirely if no .go files changed. No generate dependency (verify-quick handles it).
#
# pkg/capi/ and hack/capi-vendor/ are separate Go modules; go test from the root
# module fails on them with "does not contain package". Correctness of synced
# types is verified by go build ./... on the root module.
.PHONY: test-changed
test-changed:
	@CHANGED_DIRS=$$(git diff --name-only $(PULL_BASE_SHA)...HEAD -- '*.go' | \
		while IFS= read -r file; do dirname "$$file"; done | \
		sort -u | sed 's|^|./|' | grep -v '^\./vendor/' | grep -vE '^\./(api|pkg/capi|hack/capi-vendor)(/|$$)' | grep -vE '^\./hack/tools(/|$$)' | grep -vE '^\./test/e2e(/|$$)'); \
	if [ -z "$$CHANGED_DIRS" ]; then \
		echo "No Go files changed relative to $(PULL_BASE_SHA), skipping tests."; \
	else \
		CHANGED_PKGS=""; \
		for pkg in $$CHANGED_DIRS; do \
			if [ ! -d "$$pkg" ]; then continue; fi; \
			list_out=$$($(GO) list "$$pkg" 2>&1) && { CHANGED_PKGS="$$CHANGED_PKGS $$pkg"; continue; }; \
			case "$$list_out" in \
				*"build constraints exclude"*|*"no Go files"*) ;; \
				*) echo "ERROR: go list $$pkg failed: $$list_out" >&2; exit 1 ;; \
			esac; \
		done; \
		if [ -z "$$CHANGED_PKGS" ]; then \
			echo "Changed packages are removed or excluded by build constraints, skipping tests."; \
		else \
			echo "Running tests for changed packages: $$CHANGED_PKGS"; \
			$(GO) test -parallel=$(NUM_CORES) -count=1 -timeout=30m $$CHANGED_PKGS; \
		fi; \
	fi

# Run a subset of unit tests (used by CI sharding).
# Usage: make test-shard TEST_PACKAGES="./cmd/... ./support/..." COVER_PROFILE="cover-shard.out"
TEST_PACKAGES ?= ./...
COVER_PROFILE ?= cover.out
.PHONY: test-shard
test-shard: generate
	@echo "Running shard tests for packages: $(TEST_PACKAGES)"
	$(GO) test $(GO_TEST_FLAGS) -parallel=$(NUM_CORES) -count=1 -timeout=30m $(TEST_PACKAGES) -coverprofile $(COVER_PROFILE)

# OCP envtest index for downstream kubebuilder assets
ENVTEST_OCP_INDEX := https://raw.githubusercontent.com/openshift/api/master/envtest-releases.yaml
# OCP version to Kubernetes version mapping (OCP 4.x -> K8s 1.(x+13); OCP 5.0 == 4.23)
# OCP 4.17=1.30, 4.18=1.31, 4.19=1.32, 4.20=1.33, 4.21=1.34, 4.22=1.35, 4.23/5.0=1.36
# Also cover OpenShift-built Kubernetes 1.37 from the downstream envtest index.
ENVTEST_OCP_K8S_VERSIONS ?= 1.30.3 1.31.2 1.32.1 1.33.2 1.34.1 1.35.1 1.36.2 1.37.1

# Vanilla Kubernetes versions for envtest (upstream kubebuilder assets)
ENVTEST_KUBE_VERSIONS ?= 1.31.0 1.32.0 1.33.0 1.34.0 1.35.0 1.36.0 1.37.0

# Parallel envtest execution: 0 = sequential (default), N = N parallel jobs, MAX = all versions in parallel.
ENVTEST_JOBS ?= 0

# Internal pattern target for parallel sub-make. Do not call directly.
# Expects ENVTEST_BIN_DIR and optionally ENVTEST_INDEX_FLAG via sub-make variables.
_run-single-envtest-%:
	@log=$$(mktemp); \
	echo "=== Running envtest for K8s $* ===" > "$$log"; \
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use --use-env --bin-dir $(ENVTEST_BIN_DIR) -p path $(ENVTEST_INDEX_FLAG) $*)" \
	$(GO) test -tags envtest -race -count=1 -timeout=30m ./test/envtest/... >> "$$log" 2>&1; \
	rc=$$?; \
	cat "$$log"; \
	rm -f "$$log"; \
	exit $$rc

.PHONY: test-envtest-ocp
test-envtest-ocp: generate $(SETUP_ENVTEST) ## Run envtest tests for all supported OCP versions (ENVTEST_JOBS=0|N|MAX)
ifeq ($(ENVTEST_JOBS),0)
	@for k8s_ver in $(ENVTEST_OCP_K8S_VERSIONS); do \
		echo "=== Running envtest for OCP (K8s $$k8s_ver) ==="; \
		KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use --use-env --bin-dir $(ENVTEST_OCP_ASSETS_DIR) -p path --index $(ENVTEST_OCP_INDEX) $$k8s_ver)" \
		$(GO) test -tags envtest -race -count=1 -timeout=30m ./test/envtest/... || exit 1; \
	done
	@echo "=== All OCP envtest versions passed ==="
else
	@echo "=== Pre-fetching OCP envtest assets ==="
	@for k8s_ver in $(ENVTEST_OCP_K8S_VERSIONS); do \
		echo "  Fetching K8s $$k8s_ver..."; \
		$(SETUP_ENVTEST) use --use-env --bin-dir $(ENVTEST_OCP_ASSETS_DIR) -p path --index $(ENVTEST_OCP_INDEX) $$k8s_ver > /dev/null || { echo "Failed to fetch envtest assets for $$k8s_ver"; exit 1; }; \
	done
  ifeq ($(ENVTEST_JOBS),MAX)
	@echo "=== Running OCP envtest in parallel (jobs=$(words $(ENVTEST_OCP_K8S_VERSIONS))) ==="
	@$(MAKE) -j$(words $(ENVTEST_OCP_K8S_VERSIONS)) --no-print-directory \
		ENVTEST_BIN_DIR="$(ENVTEST_OCP_ASSETS_DIR)" \
		ENVTEST_INDEX_FLAG="--index $(ENVTEST_OCP_INDEX)" \
		$(addprefix _run-single-envtest-,$(ENVTEST_OCP_K8S_VERSIONS))
  else
	@echo "=== Running OCP envtest in parallel (jobs=$(ENVTEST_JOBS)) ==="
	@$(MAKE) -j$(ENVTEST_JOBS) --no-print-directory \
		ENVTEST_BIN_DIR="$(ENVTEST_OCP_ASSETS_DIR)" \
		ENVTEST_INDEX_FLAG="--index $(ENVTEST_OCP_INDEX)" \
		$(addprefix _run-single-envtest-,$(ENVTEST_OCP_K8S_VERSIONS))
  endif
	@echo "=== All OCP envtest versions passed ==="
endif

.PHONY: test-envtest-kube
test-envtest-kube: generate $(SETUP_ENVTEST) ## Run envtest tests for all supported Kubernetes versions (ENVTEST_JOBS=0|N|MAX)
ifeq ($(ENVTEST_JOBS),0)
	@for k8s_ver in $(ENVTEST_KUBE_VERSIONS); do \
		echo "=== Running envtest for Kubernetes $$k8s_ver ==="; \
		KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use --use-env --bin-dir $(ENVTEST_KUBE_ASSETS_DIR) -p path $$k8s_ver)" \
		$(GO) test -tags envtest -race -count=1 -timeout=30m ./test/envtest/... || exit 1; \
	done
	@echo "=== All Kubernetes envtest versions passed ==="
else
	@echo "=== Pre-fetching Kubernetes envtest assets ==="
	@for k8s_ver in $(ENVTEST_KUBE_VERSIONS); do \
		echo "  Fetching K8s $$k8s_ver..."; \
		$(SETUP_ENVTEST) use --use-env --bin-dir $(ENVTEST_KUBE_ASSETS_DIR) -p path $$k8s_ver > /dev/null || { echo "Failed to fetch envtest assets for $$k8s_ver"; exit 1; }; \
	done
  ifeq ($(ENVTEST_JOBS),MAX)
	@echo "=== Running Kubernetes envtest in parallel (jobs=$(words $(ENVTEST_KUBE_VERSIONS))) ==="
	@$(MAKE) -j$(words $(ENVTEST_KUBE_VERSIONS)) --no-print-directory \
		ENVTEST_BIN_DIR="$(ENVTEST_KUBE_ASSETS_DIR)" \
		ENVTEST_INDEX_FLAG="" \
		$(addprefix _run-single-envtest-,$(ENVTEST_KUBE_VERSIONS))
  else
	@echo "=== Running Kubernetes envtest in parallel (jobs=$(ENVTEST_JOBS)) ==="
	@$(MAKE) -j$(ENVTEST_JOBS) --no-print-directory \
		ENVTEST_BIN_DIR="$(ENVTEST_KUBE_ASSETS_DIR)" \
		ENVTEST_INDEX_FLAG="" \
		$(addprefix _run-single-envtest-,$(ENVTEST_KUBE_VERSIONS))
  endif
	@echo "=== All Kubernetes envtest versions passed ==="
endif

.PHONY: test-envtest-api-all
test-envtest-api-all: test-envtest-ocp test-envtest-kube ## Run all envtest API tests (ENVTEST_JOBS=0|N|MAX)

.PHONY: e2e
e2e: reqserving-e2e e2ev2 e2ev2-create-guests e2ev2-run-tests e2ev2-destroy-guests e2ev2-dump-guests backuprestore-e2e
	$(GO_E2E_RECIPE) -o bin/test-e2e ./test/e2e
	$(GO_BUILD_RECIPE) -o bin/test-setup ./test/setup
	cd $(TOOLS_DIR); GO111MODULE=on GOFLAGS=-mod=vendor GOWORK=off go build -tags=tools -o ../../bin/gotestsum gotest.tools/gotestsum

# Build request serving e2e tests
.PHONY: reqserving-e2e
reqserving-e2e:
	CGO_ENABLED=1 $(GO) test $(GO_GCFLAGS) -tags reqserving -c -o bin/test-reqserving ./test/reqserving-e2e

# Build e2e v2 tests
.PHONY: e2ev2
e2ev2:
	$(GO_E2EV2_RECIPE) -o bin/test-e2e-v2 ./test/e2e/v2/tests

.PHONY: e2ev2-create-guests
e2ev2-create-guests:
	$(GO_BUILD_RECIPE) -tags e2ev2 -o bin/create-guests ./test/e2e/v2/cmd/create-guests

.PHONY: e2ev2-run-tests
e2ev2-run-tests:
	$(GO_BUILD_RECIPE) -tags e2ev2 -o bin/run-tests ./test/e2e/v2/cmd/run-tests

.PHONY: e2ev2-destroy-guests
e2ev2-destroy-guests:
	$(GO_BUILD_RECIPE) -tags e2ev2 -o bin/destroy-guests ./test/e2e/v2/cmd/destroy-guests

.PHONY: e2ev2-dump-guests
e2ev2-dump-guests:
	$(GO_BUILD_RECIPE) -tags e2ev2 -o bin/dump-guests ./test/e2e/v2/cmd/dump-guests

.PHONY: backuprestore-e2e
backuprestore-e2e:
	$(GO_BACKUPRESTORE_E2E_RECIPE) -o bin/test-backuprestore ./test/e2e/v2/tests

.PHONY: test-backup-restore
test-backup-restore: backuprestore-e2e
	mkdir -p $(ARTIFACT_DIR)
	ARTIFACT_DIR=$(ARTIFACT_DIR) bin/test-backuprestore \
	  --ginkgo.v \
	  --ginkgo.no-color=$(OPENSHIFT_CI) \
	  --e2e.junit-report="$(ARTIFACT_DIR)/junit.xml" \
	  --ginkgo.label-filter="backup-restore" \
	  --ginkgo.fail-fast=$(FAIL_FAST) \
	  --ginkgo.timeout=2h

.PHONY: test-cross-cluster-restore
test-cross-cluster-restore: backuprestore-e2e
	mkdir -p $(ARTIFACT_DIR)
	ARTIFACT_DIR=$(ARTIFACT_DIR) bin/test-backuprestore \
	  --ginkgo.v \
	  --ginkgo.no-color=$(OPENSHIFT_CI) \
	  --e2e.junit-report="$(ARTIFACT_DIR)/junit.xml" \
	  --ginkgo.label-filter="cross-cluster-restore" \
	  --ginkgo.fail-fast=$(FAIL_FAST) \
	  --ginkgo.timeout=2h

# Run go fmt against code
.PHONY: fmt
fmt:
	$(VERIFY) fmt

# Run go vet against code
.PHONY: vet
vet:
	$(VERIFY) vet

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
	$(VERIFY) deps

.PHONY: api-deps
api-deps:
	$(VERIFY) api-deps

.PHONY: capi-sync capi-sync-force verify-capi-sync $(foreach p,$(CAPI_PROVIDERS),capi-sync-$(p))
capi-sync capi-sync-force verify-capi-sync $(foreach p,$(CAPI_PROVIDERS),capi-sync-$(p)):
	$(VERIFY) $@

# Guards the invariants of the localized provider modules: that they stay free
# of upstream CAPI, that all of their subpackages build (the root module only
# compiles the ones it imports), and that packages consuming the localized
# types still compile. Faster than staticcheck and points at the cause.
.PHONY: verify-capi-decoupling
verify-capi-decoupling:
	$(VERIFY) verify-capi-decoupling

.PHONY: workspace-sync
workspace-sync:
	$(VERIFY) workspace-sync

# Run staticcheck
# How to ignore failures https://staticcheck.io/docs/configuration#line-based-linter-directives
.PHONY: staticcheck
staticcheck:
	$(VERIFY) staticcheck

# Build the docker image with official golang image
.PHONY: docker-build
docker-build:
	${RUNTIME} build --build-arg COMMIT_HASH=$(COMMIT_HASH) . -t ${IMG}

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

.PHONY: verify-docs-nav
verify-docs-nav:
	$(VERIFY) verify-docs-nav

.PHONY: verify-tekton-pipeline-pairs
verify-tekton-pipeline-pairs:
	$(VERIFY) verify-tekton-pipeline-pairs

COMMIT_LAYOUT := skills/restructure-commits/commit_layout.py

.PHONY: verify-commit-layout
verify-commit-layout: ## Verify component commit layout of BASE..HEAD_REF (restructure-commits gate). Set BASE; optional HEAD_REF, EXPECT_TREE.
	@test -n "$(BASE)" || { echo "BASE must be set to the merge base with the PR target branch" >&2; exit 2; }
	@python3 $(COMMIT_LAYOUT) verify --base "$(BASE)" --head "$(or $(HEAD_REF),HEAD)" $(if $(EXPECT_TREE),--expect-tree "$(EXPECT_TREE)")

.PHONY: ownership-rules
ownership-rules:
	$(VERIFY) ownership-rules

.PHONY: test-commit-layout
test-commit-layout: $(PYTHON_VENV_STAMP) ## Test commit layout and Go ownership generation.
	CGO_ENABLED=0 $(GO) -C $(TOOLS_DIR) test ./verify -count=1
	@if [ -x $(PYTHON_VENV)/bin/python3 ]; then \
		$(PYTHON_VENV)/bin/python3 -m pytest -q -p no:cacheprovider skills/restructure-commits; \
	else \
		PYTHONPATH=$(PYTHON_VENV) python3 -m pytest -q -p no:cacheprovider skills/restructure-commits; \
	fi

.PHONY: verify-codespell
verify-codespell:
	$(VERIFY) verify-codespell

.PHONY: verify-api-deps
verify-api-deps:
	$(VERIFY) verify-api-deps

.PHONY: run-gitlint
run-gitlint:
	$(VERIFY) run-gitlint

.PHONY: run-gitlint-commit-msg
run-gitlint-commit-msg:
	COMMIT_MSG_FILE="$(COMMIT_MSG_FILE)" $(VERIFY) run-gitlint-commit-msg

.PHONY: cpo-container-sync
cpo-container-sync:
	$(VERIFY) cpo-container-sync

## Run Karpenter upstream e2e tests. Requires:
# - KARPENTER_CORE_DIR to be set to the path of the karpenter-core repository
# - current context to be set to a hosted cluster with AutoNode enabled.
# - an annotation to be applied to the HCP to stop reconcillation (hypershift.openshift.io/karpenter-core-e2e-override=true)
.PHONY: karpenter-upstream-e2e
karpenter-upstream-e2e: $(YQ)
	./karpenter-operator/e2e/upstream-e2e.sh

## --------------------------------------
## Tooling Binaries
## --------------------------------------

# Python is used only by the optional commit-restructuring test suite.
$(PYTHON_VENV_STAMP):
	python3 -m venv $(PYTHON_VENV)
	$(PYTHON_VENV)/bin/python -m pip install pytest==$(PYTEST_VER)
	touch $@

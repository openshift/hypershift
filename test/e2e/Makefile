.DEFAULT_GOAL:=help

REPO_ROOT := $(shell git rev-parse --show-toplevel)
DATA_DIR := $(REPO_ROOT)/test/e2e/data

help:  ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-25s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

## --------------------------------------
## Binaries
## --------------------------------------

TOOLS_DIR := $(REPO_ROOT)/hack/tools
BIN_DIR := bin
TOOLS_BIN_DIR := $(TOOLS_DIR)/$(BIN_DIR)
GINKGO := $(TOOLS_BIN_DIR)/ginkgo
KUSTOMIZE := $(TOOLS_BIN_DIR)/kustomize

$(GINKGO): # Build ginkgo from tools folder.
	cd $(TOOLS_DIR) && go build -tags=tools -o $(BIN_DIR)/ginkgo github.com/onsi/ginkgo/ginkgo

$(KUSTOMIZE): # Build kustomize from tools folder.
	$(REPO_ROOT)/hack/ensure-kustomize.sh

## --------------------------------------
## Data
## --------------------------------------

.PHONY: example-cluster
example-cluster:
	mkdir -p $(DATA_DIR)
	KUSTOMIZE_PLUGIN_HOME=$(REPO_ROOT)/config/example-cluster/plugin kustomize build --enable_alpha_plugins $(REPO_ROOT)/config/example-cluster > $(DATA_DIR)/example-cluster.yaml

## --------------------------------------
## Testing
## --------------------------------------

GINKGO_FOCUS  ?=
GINKGO_NODES  ?= 1
SKIP_RESOURCE_CLEANUP ?= false
GINKGO_NOCOLOR ?= false

.PHONY: run
run: $(GINKGO) example-cluster ## Run the end-to-end tests
	$(GINKGO) -v -trace -tags=e2e -focus="$(GINKGO_FOCUS)" -nodes=$(GINKGO_NODES) --noColor=$(GINKGO_NOCOLOR) $(GINKGO_ARGS) . -- \
	  -e2e.data-dir="$(DATA_DIR)"

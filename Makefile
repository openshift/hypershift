# Image URL to use all building/pushing image targets
IMG ?= hypershift:latest
# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true"

CONTROLLER_GEN=GO111MODULE=on GOFLAGS=-mod=vendor go run sigs.k8s.io/controller-tools/cmd/controller-gen

GO_GCFLAGS ?= -gcflags=all='-N -l'
GO=GO111MODULE=on GOFLAGS=-mod=vendor go
GO_BUILD_RECIPE=CGO_ENABLED=0 $(GO) build $(GO_GCFLAGS)

# Kustomize overlay to use
PROFILE ?= production

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

all: build manifests

build: hypershift-operator control-plane-operator

verify: build fmt vet

# Generate code
generate:
	hack/update-generated-bindata.sh
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

# Build hypershift-operator binary
hypershift-operator: generate
	$(GO_BUILD_RECIPE) -o bin/hypershift-operator ./hypershift-operator

# Build control-plane-operator binary
control-plane-operator: generate
	$(GO_BUILD_RECIPE) -o bin/control-plane-operator ./control-plane-operator

# Run tests
test: build
	$(GO) test ./... -coverprofile cover.out

# Generate Kube manifests (e.g. CRDs)
manifests:
	$(CONTROLLER_GEN) $(CRD_OPTIONS) webhook paths="./..." output:crd:artifacts:config=config/hypershift-operator

# Installs hypershift into a cluster
install: manifests release-info-data
	kustomize build config/install/$(PROFILE) | oc apply -f -

# Uninstalls hypershit from a cluster
uninstall: manifests
	kustomize build config/install/$(PROFILE) | oc delete -f -

# Builds the config with Kustomize for manual usage
.PHONY: config
config: release-info-data
	kustomize build config/install/$(PROFILE)

# Run go fmt against code
fmt:
	$(GO) fmt ./...

# Run go vet against code
vet:
	$(GO) vet ./...

# Build the docker image
docker-build:
	docker build . -t ${IMG}

# Push the docker image
docker-push:
	docker push ${IMG}

release-info-data:
	oc adm release info --output json > config/hypershift-operator/release-info.json

DIR := ${CURDIR}

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

build: control-plane-operator

.PHONY: verify
verify: deps fmt vet
	git diff-index --cached --quiet --ignore-submodules HEAD --
	git diff-files --quiet --ignore-submodules
	$(eval STATUS = $(shell git status -s))
	$(if $(strip $(STATUS)),$(error untracked files detected))

.PHONY: control-plane-operator
control-plane-operator:
	$(GO_BUILD_RECIPE) -o bin/control-plane-operator .

# Run tests
.PHONY: test
test: build
	$(GO) test ./... -coverprofile cover.out

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

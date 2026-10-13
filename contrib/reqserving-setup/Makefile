GO_GCFLAGS ?= -gcflags=all='-N -l'
GO=GO111MODULE=on GOWORK=off GOFLAGS=-mod=vendor go
GO_BUILD_RECIPE=CGO_ENABLED=1 $(GO) build $(GO_GCFLAGS)

.PHONY: all
all: build

.PHONY: build
build:
	$(GO_BUILD_RECIPE) -o reqserving-setup .

.PHONY: clean
clean:
	rm -f reqserving-setup

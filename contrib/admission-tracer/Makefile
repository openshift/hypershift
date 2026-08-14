
IMG=quay.io/sjenning/admission-tracer:latest
RUNTIME ?= podman
GO_GCFLAGS ?= -gcflags=all='-N -l'
GO=GO111MODULE=on GOFLAGS=-mod=vendor go
GO_BUILD_RECIPE=CGO_ENABLED=0 $(GO) build $(GO_GCFLAGS)

all: build image push

.PHONY: build
build:
	$(GO_BUILD_RECIPE) .

.PHONY: image
image: build
	${RUNTIME} build . -t ${IMG}

.PHONY: push
push: image
	${RUNTIME} push ${IMG}

.PHONY: deploy
deploy:
	oc apply -f manifests/

.PHONY: redeploy
redeploy:
	oc patch deployment -n hypershift admission-tracer -p "{\"spec\":{\"template\":{\"metadata\":{\"annotations\":{\"date\":\"$(shell date +%s)\"}}}}}"
IMG ?= quay.io/hypershift/mkdocs-material:latest
RUNTIME ?= docker

.PHONY: build
build:
	mkdocs build

.PHONY: build-containerized
build-containerized:
	$(RUNTIME run --rm -it -v ${PWD}:/docs $(IMG) build

.PHONY: serve-containerized
serve-containerized:
	$(RUNTIME) run --rm -it -p 8000:8000 -v ${PWD}:/docs $(IMG) serve --dev-addr 0.0.0.0:8000

.PHONY: image
image:
	$(RUNTIME) build -t $(IMG) .

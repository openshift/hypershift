IMG ?= quay.io/hypershift/mkdocs-material:latest

.PHONY: build
build:
	mkdocs build

.PHONY: build-containerized
build-containerized:
	docker run --rm -it -v ${PWD}:/docs $(IMG) build

.PHONY: serve-containerized
serve-containerized:
	docker run --rm -it -p 8000:8000 -v ${PWD}:/docs $(IMG) serve --dev-addr 0.0.0.0:8000

.PHONY: image
image:
	docker build -t $(IMG) .

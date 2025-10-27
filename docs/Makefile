# Prefer Docker for better multi-arch and registry support, fallback to podman
RUNTIME ?= $(shell command -v docker >/dev/null 2>&1 && echo docker || echo podman)
version := $(shell awk -f image-version.awk requirements.txt)
VERSION ?= $(if $(MKDOCS_TAG),$(MKDOCS_TAG),latest)
IMG ?= quay.io/hypershift/mkdocs-material:$(VERSION)

# Multi-architecture support for Linux platforms
PLATFORMS ?= linux/amd64,linux/arm64
BUILDER_NAME ?= hypershift-docs-builder

.PHONY: build
build:
	mkdocs build

.PHONY: build-containerized
build-containerized:
	$(RUNTIME) volume create --ignore hypershift-docs-site
	$(RUNTIME) run --rm -it \
		-v $(shell pwd):/home/docs/hypershift:Z \
		-v $(shell pwd)/../.git:/home/docs/hypershift/.git:Z \
		-v hypershift-docs-site:/home/docs/hypershift/site \
		$(IMG) build --strict

.PHONY: serve-containerized
serve-containerized:
	$(RUNTIME) run --rm -it -p 8000:8000 \
		-v $(shell pwd):/home/docs/hypershift:Z \
		-v $(shell pwd)/../.git:/home/docs/hypershift/.git:Z \
		-v hypershift-docs-site:/home/docs/hypershift/site \
		$(IMG) serve --dev-addr 0.0.0.0:8000

.PHONY: setup-buildx
setup-buildx:
	@if [ "$(RUNTIME)" = "docker" ]; then \
		if ! docker buildx ls | grep -q $(BUILDER_NAME); then \
			docker buildx create --name $(BUILDER_NAME) --use; \
		else \
			docker buildx use $(BUILDER_NAME); \
		fi; \
	fi

.PHONY: image
image: setup-buildx
	@if [ "$(RUNTIME)" = "docker" ]; then \
		docker buildx build --platform $(PLATFORMS) -t $(IMG) --load .; \
	else \
		$(RUNTIME) build --platform $(PLATFORMS) -t $(IMG) .; \
	fi

.PHONY: image-multiarch
image-multiarch: setup-buildx
	@if [ "$(RUNTIME)" = "docker" ]; then \
		docker buildx build --platform $(PLATFORMS) -t $(IMG) .; \
	else \
		$(RUNTIME) build --manifest $(IMG) --platform $(PLATFORMS) .; \
	fi

# Build and push multi-architecture images
.PHONY: push
push: setup-buildx
	@echo "🚀 Building and pushing multi-architecture image..."
	@echo "   Image: $(IMG)"
	@echo "   Version: $(VERSION)"
	@echo "   Platforms: $(PLATFORMS)"
	@echo "   Runtime: $(RUNTIME)"
	@echo ""
	@if [ "$(RUNTIME)" = "docker" ]; then \
		docker buildx build --platform $(PLATFORMS) -t $(IMG) --push .; \
	else \
		$(RUNTIME) build --platform $(PLATFORMS) -t $(IMG) .; \
		$(RUNTIME) manifest create $(IMG) --amend $(IMG); \
		$(RUNTIME) manifest push $(IMG); \
	fi
	@echo ""
	@echo "✅ Successfully pushed $(IMG) for platforms: $(PLATFORMS)"

.PHONY: clean-builder
clean-builder:
	@if [ "$(RUNTIME)" = "docker" ]; then \
		if docker buildx ls | grep -q $(BUILDER_NAME); then \
			docker buildx rm $(BUILDER_NAME); \
		fi; \
	fi

.PHONY: inspect
inspect:
	@echo "Runtime: $(RUNTIME)"
	@echo "Version: $(VERSION)"
	@echo "Image: $(IMG)"
	@echo "Platforms: $(PLATFORMS)"
	@echo "Builder: $(BUILDER_NAME)"

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build               - Build documentation locally"
	@echo "  build-containerized - Build documentation using container"
	@echo "  serve-containerized - Serve documentation using container on port 8000"
	@echo ""
	@echo "Deployment:"
	@echo "  push                - 🚀 Build and push multi-arch image (current: $(VERSION))"
	@echo ""
	@echo "Usage examples:"
	@echo "  make push                      # Push as :latest"
	@echo "  make push VERSION=v1.2.3      # Push as :v1.2.3"
	@echo "  make push VERSION=9.6.8       # Push as :9.6.8"
	@echo ""
	@echo "Local development:"
	@echo "  image               - Build container image for current platform"
	@echo "  image-multiarch     - Build multi-architecture container image"
	@echo ""
	@echo "Utilities:"
	@echo "  setup-buildx        - Setup Docker buildx for multi-platform builds"
	@echo "  clean-builder       - Remove buildx builder"
	@echo "  inspect             - Show current configuration"
	@echo "  help                - Show this help message"

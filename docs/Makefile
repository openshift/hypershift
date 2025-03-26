RUNTIME ?= $(shell sh ../hack/utils.sh get_container_engine)
VERSION := $(shell awk -f image-version.awk requirements.txt)
IMG ?= quay.io/hypershift/mkdocs-material:$(VERSION)

.PHONY: build
build:
	mkdocs build

.PHONY: build-containerized
build-containerized:
	$(RUNTIME) volume create --ignore hypershift-docs-site
	$(RUNTIME) run --rm -it \
		-v ${PWD}:/home/docs/hypershift:Z \
		-v hypershift-docs-site:/home/docs/hypershift/site \
		$(IMG) build --strict

.PHONY: serve-containerized
serve-containerized:
	$(RUNTIME) run --rm -it -p 8000:8000 \
		-v ${PWD}:/home/docs/hypershift:Z \
		-v hypershift-docs-site:/home/docs/hypershift/site \
		$(IMG) serve --dev-addr 0.0.0.0:8000

.PHONY: image
image:
	$(RUNTIME) build -t $(IMG) .

.PHONY: push
push:
        $(RUNTIME) push $(IMG)

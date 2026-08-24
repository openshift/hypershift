RUNTIME ?= $(shell sh ../hack/utils.sh get_container_engine)
IMG ?= docker.io/zensical/zensical:0.0.51

.PHONY: build
build:
	uv run --frozen zensical build --strict

.PHONY: build-containerized
build-containerized:
	$(RUNTIME) volume create --ignore hypershift-docs-site
	$(RUNTIME) run --rm -it \
		-v ${PWD}:/docs:Z \
		-v hypershift-docs-site:/docs/site \
		$(IMG) build --strict

.PHONY: serve-containerized
serve-containerized:
	$(RUNTIME) run --rm -it -p 8000:8000 \
		-v ${PWD}:/docs:Z \
		-v hypershift-docs-site:/docs/site \
		$(IMG)

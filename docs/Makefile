RUNTIME ?= $(shell sh ../hack/utils.sh get_container_engine)
IMG ?= docker.io/zensical/zensical:0.0.51
BASE ?= main

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

# Visual comparison between the current working tree and a base ref (default: main).
#
#   make compare              # compare against main
#   make compare BASE=v4.18   # compare against a tag
#
# The report is written to compare/output/ and served on http://127.0.0.1:8883.
.PHONY: compare
compare: compare-build compare-run

.PHONY: compare-build
compare-build: compare-build-base compare-build-head

.PHONY: compare-build-base
compare-build-base:
	@BASE_SHA=$$(git -C .. rev-parse "$(BASE)"); \
	if [ -f site-base/.commit ] && [ "$$(cat site-base/.commit)" = "$$BASE_SHA" ]; then \
		echo "site-base is up to date ($$BASE_SHA), skipping"; \
	else \
		rm -rf site-base; \
		echo "Building base site from $(BASE) ($$BASE_SHA)..."; \
		WORKTREE=$$(mktemp -d); \
		trap 'git -C .. worktree remove --force "$$WORKTREE" 2>/dev/null' EXIT; \
		git -C .. worktree add --detach "$$WORKTREE" "$(BASE)"; \
		cd "$$WORKTREE/docs" && uv run --frozen zensical build -d "$(CURDIR)/site-base"; \
		echo "$$BASE_SHA" > site-base/.commit; \
	fi

.PHONY: compare-build-head
compare-build-head:
	@rm -rf site-head site; \
	echo "Building head site from working tree..."; \
	uv run --frozen zensical build -f mkdocs.yml; \
	mv site site-head

.PHONY: compare-run
compare-run:
	cd compare && pnpm install --frozen-lockfile
	cd compare && pnpm exec playwright install chromium
	cd compare && node compare.mjs ../site-base ../site-head --output output
	@echo ""
	@echo "Report: compare/output/index.html"
	@echo "Run 'make compare-serve' to view in a browser."

.PHONY: compare-serve
compare-serve:
	@echo "Serving report at http://127.0.0.1:8883"
	@cd compare/output && python3 -m http.server 8883 --bind 127.0.0.1

.PHONY: compare-clean
compare-clean:
	rm -rf site-base site-head compare/output

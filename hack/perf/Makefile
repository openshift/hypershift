# Standalone Makefile for the performance timing tooling.
#
# These targets are deliberately kept out of the repository root Makefile: the tooling reads CI
# artifacts and verifies nothing about the hypershift code itself, so it does not belong in the
# `verify` gates. Run it from this directory:
#
#   make -C hack/perf test

PYTHON ?= python3

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help.
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-12s\033[0m %s\n", $$1, $$2}'

# Plain unittest rather than pytest: these tests are stdlib-only, so this target needs nothing
# beyond the PyYAML the extractor itself requires.
.PHONY: test
test: ## Run the timing extractor tests against the fixtures in testdata/.
	$(PYTHON) -m unittest discover -s $(CURDIR) -t $(CURDIR) -p 'test_*.py' -v

.PHONY: extract
extract: ## Extract timings from an artifact tree: make extract ARTIFACTS=/tmp/artifacts
	@test -n "$(ARTIFACTS)" || { echo "set ARTIFACTS=<artifact dir>" >&2; exit 2; }
	$(PYTHON) $(CURDIR)/hcp-timings.py extract $(ARTIFACTS)

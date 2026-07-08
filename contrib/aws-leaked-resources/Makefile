BINARY := cleanleaked
REPO_ROOT := $(shell git rev-parse --show-toplevel)
PKG := ./contrib/aws-leaked-resources

.PHONY: build test vet clean scan dry-run

build:
	@mkdir -p bin
	cd $(REPO_ROOT) && go build -o $(PKG)/bin/$(BINARY) $(PKG)
	@echo "Built: bin/$(BINARY)"

test:
	cd $(REPO_ROOT) && go test $(PKG)/... -count=1 -timeout 120s -v

vet:
	cd $(REPO_ROOT) && go vet $(PKG)/...

clean:
	rm -rf bin

scan:
	cd $(REPO_ROOT) && go run $(PKG) scan

dry-run:
	cd $(REPO_ROOT) && go run $(PKG) delete --dry-run

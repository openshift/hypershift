.PHONY: all
all: build test check

.PHONY: build
build:
	goreleaser build --clean --snapshot --single-target

.PHONY: test
test:
	go test -count=1 -cover -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

.PHONY: regenerate
regenerate:
	REGENERATE_TEST_OUTPUTS=true go test ./...

.PHONY: graph
graph:
	go run ./shorten/internal/generate/

.PHONY: check
check:
	golangci-lint run

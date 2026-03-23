.PHONY: build test lint clean dashboard scraper image deploy

GO := go
BINARY_DIR := bin

build: dashboard scraper

dashboard:
	$(GO) build -o $(BINARY_DIR)/dashboard ./cmd/dashboard

scraper:
	$(GO) build -o $(BINARY_DIR)/scraper ./cmd/scraper

test:
	$(GO) test ./... -v -count=1

lint:
	$(GO) vet ./...

clean:
	rm -rf $(BINARY_DIR)

image:
	podman build -t jira-agent-dashboard:latest -f Containerfile .

deploy:
	kubectl apply -k deploy/

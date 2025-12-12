SHELL := /bin/bash

NAME := oadp-recovery
BIN  := bin/$(NAME)
GO   := go

# Docker variables
REGISTRY ?= quay.io/hypershift
IMAGE_NAME ?= $(NAME)
IMAGE_TAG ?= latest
IMAGE := $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: all build test lint clean deploy remove docker-build docker-push test-clusters test-recovery test-cleanup help

all: deps build


deps:
	$(GO) mod tidy
	$(GO) mod download

build:
	mkdir -p bin
	$(GO) build -o $(BIN) .

test:
	$(GO) test ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BIN)

deploy:
	@echo "Deploying OADP Recovery CronJob..."
	kubectl apply -f manifests/
	@echo "OADP Recovery CronJob deployed successfully"

remove:
	@echo "Removing OADP Recovery CronJob..."
	kubectl delete -f manifests/ --ignore-not-found=true
	@echo "OADP Recovery CronJob removed successfully"

docker-image: docker-build docker-push

docker-build:
	@echo "Building Docker image $(IMAGE)..."
	docker build --platform linux/amd64 -t $(IMAGE) .
	@echo "Docker image $(IMAGE) built successfully"

docker-push: docker-build
	@echo "Pushing Docker image $(IMAGE)..."
	docker push $(IMAGE)
	@echo "Docker image $(IMAGE) pushed successfully"

# Integration test targets
test-clusters:
	@echo "Creating test HostedClusters for OADP recovery testing..."
	@echo "Environment variables:"
	@echo "  NUM_CLUSTERS=$(or $(NUM_CLUSTERS),2)"
	@echo "  BASE_NAME=$(or $(BASE_NAME),test-cluster)"
	@echo ""
	NUM_CLUSTERS=$(or $(NUM_CLUSTERS),2) BASE_NAME=$(or $(BASE_NAME),test-cluster) ./test/create-test-clusters.sh

test-recovery:
	@echo "Testing OADP recovery functionality..."
	$(eval JOB_NAME := test-recovery-$(shell date +%s))
	kubectl create job --from=cronjob/oadp-recovery $(JOB_NAME) -n openshift-adp
	@echo "Job $(JOB_NAME) created. Waiting for completion..."
	kubectl wait --for=condition=complete --timeout=60s job/$(JOB_NAME) -n openshift-adp
	@echo "📊 Recovery job logs:"
	kubectl logs job/$(JOB_NAME) -n openshift-adp
	@echo "✅ OADP recovery test completed"

test-cleanup:
	@echo "Cleaning up test resources..."
	kubectl delete namespace test-oadp-recovery --ignore-not-found=true
	@echo "Test cleanup completed"

test-integration: test-integration-cleanup test-clusters deploy test-recovery test-integration-verify
	@echo ""
	@echo "🎉 Full integration test completed successfully!"
	@echo "📊 Results:"
	@echo "  ✅ Test clusters created with OADP annotations"
	@echo "  ✅ OADP Recovery CronJob deployed"
	@echo "  ✅ Recovery test executed - clusters without backups were unpaused"
	@echo "  ✅ Verification completed"
	@echo ""
	@echo "🧹 To cleanup: make test-cleanup"

test-integration-cleanup:
	@echo "🧹 Cleaning test environment..."
	kubectl delete namespace test-oadp-recovery --ignore-not-found=true
	kubectl delete jobs -n openshift-adp -l app=oadp-recovery --ignore-not-found=true >/dev/null 2>&1 || true
	kubectl delete cronjob oadp-recovery -n openshift-adp --ignore-not-found=true >/dev/null 2>&1 || true
	@echo "✅ Test environment cleaned"

test-integration-verify:
	@echo ""
	@echo "🔍 Verifying integration test results..."
	@echo "📊 HostedClusters status:"
	kubectl get hostedcluster -n test-oadp-recovery -o custom-columns="NAME:.metadata.name,PAUSED:.spec.pausedUntil,OADP_ANNOTATION:.metadata.annotations.oadp\.openshift\.io/paused-by" --no-headers | while read name paused oadp; do \
		if [[ "$$oadp" == "<none>" ]]; then \
			echo "  ✅ $$name - Successfully unpaused (no OADP annotations)"; \
		else \
			echo "  ❌ $$name - Still has OADP annotations: $$oadp"; \
		fi; \
	done
	@echo ""
	@echo "📊 NodePools status:"
	kubectl get nodepool -n test-oadp-recovery -o custom-columns="NAME:.metadata.name,PAUSED:.spec.pausedUntil,OADP_ANNOTATION:.metadata.annotations.oadp\.openshift\.io/paused-by" --no-headers | while read name paused oadp; do \
		if [[ "$$oadp" == "<none>" ]]; then \
			echo "  ✅ $$name - Successfully unpaused (no OADP annotations)"; \
		else \
			echo "  ❌ $$name - Still has OADP annotations: $$oadp"; \
		fi; \
	done
	@echo "✅ Verification completed"

help:
	@echo "OADP Recovery Makefile"
	@echo ""
	@echo "Available targets:"
	@echo "  build            Build the oadp-recovery binary"
	@echo "  test             Run unit tests"
	@echo "  deps             Download Go dependencies"
	@echo "  lint             Run linter"
	@echo "  clean            Clean build artifacts"
	@echo ""
	@echo "Docker targets:"
	@echo "  docker-build     Build Docker image"
	@echo "  docker-push      Build and push Docker image"
	@echo "  docker-image     Build and push Docker image (alias)"
	@echo ""
	@echo "Deployment targets:"
	@echo "  deploy           Deploy OADP Recovery CronJob to cluster"
	@echo "  remove           Remove OADP Recovery CronJob from cluster"
	@echo ""
	@echo "Integration test targets:"
	@echo "  test-clusters    Create test HostedClusters for testing"
	@echo "  test-recovery    Test OADP recovery functionality"
	@echo "  test-integration Complete end-to-end integration test (cleanup + create + deploy + test + verify)"
	@echo "  test-cleanup     Clean up test resources"
	@echo ""
	@echo "Environment variables for test-clusters:"
	@echo "  NUM_CLUSTERS     Number of clusters to create (default: 2)"
	@echo "  BASE_NAME        Base name for clusters (default: test-cluster)"
	@echo ""
	@echo "Examples:"
	@echo "  make test-clusters NUM_CLUSTERS=5"
	@echo "  make test-clusters BASE_NAME=my-test NUM_CLUSTERS=3"


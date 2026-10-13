SHELL := /bin/bash

.PHONY: test test-clusters test-recovery test-cleanup deploy clean help

help: ## Display this help message
	@echo "OADP Recovery Script"
	@echo "===================="
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

deploy: ## Deploy the CronJob to the cluster
	@echo "Deploying OADP recovery CronJob..."
	@echo "Generating ConfigMap from standalone script..."
	./scripts/generate-configmap.sh | kubectl apply -f -
	kubectl apply -f manifests/rbac-cronjob.yaml

clean: ## Remove the CronJob from the cluster
	@echo "Removing OADP recovery CronJob..."
	kubectl delete -f manifests/rbac-cronjob.yaml --ignore-not-found=true
	kubectl delete configmap oadp-recovery-script -n hypershift --ignore-not-found=true

## Testing
test: ## Test: Run complete integration test (scales operator, creates clusters, tests recovery, cleans up)
	@echo "Running OADP recovery integration test..."
	./test/test-integration.sh

test-clusters: ## Test: Create test clusters for integration testing
	@echo "Creating test HostedClusters for OADP recovery testing..."
	@echo "Environment variables:"
	@echo "  NUM_CLUSTERS=$(or $(NUM_CLUSTERS),5)"
	@echo "  BASE_NAME=$(or $(BASE_NAME),test-cluster)"
	@echo ""
	NUM_CLUSTERS=$(or $(NUM_CLUSTERS),5) BASE_NAME=$(or $(BASE_NAME),test-cluster) ./test/create-test-clusters.sh

test-recovery: ## Test: OADP recovery functionality with existing clusters
	@echo "Testing OADP recovery functionality..."
	@echo "🔍 Before recovery - checking cluster states..."
	@kubectl get hostedcluster -n test-oadp-recovery -o custom-columns="NAME:.metadata.name,PAUSED:.spec.pausedUntil,OADP_BY:.metadata.annotations.oadp\.openshift\.io/paused-by" --no-headers || true
	@echo ""
	@echo "🚀 Running OADP recovery (REAL execution - no dry-run)..."
	./oadp-recovery.sh --log-level verbose
	@echo "✅ OADP recovery test completed"

test-cleanup: ## Test: Clean up test resources
	@echo "Cleaning up test resources..."
	@kubectl delete np -n test-oadp-recovery --all --ignore-not-found=true >/dev/null 2>&1 || true
	@kubectl delete hc -n test-oadp-recovery --all --ignore-not-found=true >/dev/null 2>&1 || true
	@kubectl delete namespace test-oadp-recovery --ignore-not-found=true >/dev/null 2>&1 || true
	@echo "Test cleanup completed"


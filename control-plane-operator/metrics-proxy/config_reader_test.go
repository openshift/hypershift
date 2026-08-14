package metricsproxy

import (
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestConfigFileReaderLoad(t *testing.T) {
	t.Parallel()

	configYAML := `
endpointResolver:
  url: https://endpoint-resolver.test-ns.svc
  caFile: /tmp/test-ca/tls.crt
components:
  - name: etcd
    selector:
      app: etcd
    metricsPort: 2381
    metricsPath: /metrics
    metricsScheme: https
    tlsServerName: etcd-client
    caFile: ""
    certFile: ""
    keyFile: ""
    metricsJob: etcd
    metricsNamespace: openshift-etcd
    metricsService: etcd
    metricsEndpoint: etcd-metrics
  - name: kube-apiserver
    selector:
      app: kube-apiserver
    metricsPort: 6443
    metricsPath: /metrics
    metricsScheme: https
    tlsServerName: kube-apiserver
    caFile: ""
    certFile: ""
    keyFile: ""
    metricsJob: apiserver
    metricsNamespace: default
    metricsService: kubernetes
    metricsEndpoint: https
`

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	log := zap.New(zap.UseDevMode(true))
	reader := NewConfigFileReader(configPath, log)

	t.Run("When loading a valid config file, it should parse all components", func(t *testing.T) {
		t.Parallel()
		r := NewConfigFileReader(configPath, log)
		if err := r.Load(); err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		names := r.GetComponentNames()
		if len(names) != 2 {
			t.Errorf("expected 2 components, got %d", len(names))
		}

		kas, ok := r.GetComponent("kube-apiserver")
		if !ok {
			t.Fatal("kube-apiserver not found")
		}
		if kas.MetricsPort != 6443 {
			t.Errorf("expected port 6443, got %d", kas.MetricsPort)
		}
		if kas.MetricsJob != "apiserver" {
			t.Errorf("expected metricsJob apiserver, got %s", kas.MetricsJob)
		}
		if kas.MetricsNamespace != "default" {
			t.Errorf("expected metricsNamespace default, got %s", kas.MetricsNamespace)
		}
		if kas.MetricsService != "kubernetes" {
			t.Errorf("expected metricsService kubernetes, got %s", kas.MetricsService)
		}
		if kas.MetricsEndpoint != "https" {
			t.Errorf("expected metricsEndpoint https, got %s", kas.MetricsEndpoint)
		}
		if kas.Selector == nil || kas.Selector["app"] != "kube-apiserver" {
			t.Errorf("expected selector {app: kube-apiserver}, got %v", kas.Selector)
		}

		etcd, ok := r.GetComponent("etcd")
		if !ok {
			t.Fatal("etcd not found")
		}
		if etcd.MetricsPort != 2381 {
			t.Errorf("expected port 2381, got %d", etcd.MetricsPort)
		}
		if etcd.MetricsJob != "etcd" {
			t.Errorf("expected metricsJob etcd, got %s", etcd.MetricsJob)
		}
		if etcd.MetricsNamespace != "openshift-etcd" {
			t.Errorf("expected metricsNamespace openshift-etcd, got %s", etcd.MetricsNamespace)
		}
		if etcd.MetricsService != "etcd" {
			t.Errorf("expected metricsService etcd, got %s", etcd.MetricsService)
		}
		if etcd.MetricsEndpoint != "etcd-metrics" {
			t.Errorf("expected metricsEndpoint etcd-metrics, got %s", etcd.MetricsEndpoint)
		}
	})

	t.Run("When loading config, it should return correct endpoint resolver URL", func(t *testing.T) {
		t.Parallel()
		if err := reader.Load(); err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}
		if reader.EndpointResolverURL() != "https://endpoint-resolver.test-ns.svc" {
			t.Errorf("expected endpoint resolver URL, got %s", reader.EndpointResolverURL())
		}
	})

	t.Run("When config file does not exist, it should return an error", func(t *testing.T) {
		t.Parallel()
		r := NewConfigFileReader("/nonexistent/path/config.yaml", log)
		if err := r.Load(); err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})

	t.Run("When component is not in config, it should return false", func(t *testing.T) {
		t.Parallel()
		r := NewConfigFileReader(configPath, log)
		if err := r.Load(); err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}
		_, ok := r.GetComponent("nonexistent")
		if ok {
			t.Error("expected false for nonexistent component")
		}
	})
}

func TestBuildTLSConfigFromFiles(t *testing.T) {
	t.Parallel()

	t.Run("When CA file does not exist, it should return config without CA", func(t *testing.T) {
		t.Parallel()
		cfg, err := buildTLSConfigFromFiles("/nonexistent/ca.crt", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RootCAs != nil {
			t.Error("expected nil RootCAs for nonexistent CA file")
		}
	})

	t.Run("When no files are specified, it should return a basic TLS config", func(t *testing.T) {
		t.Parallel()
		cfg, err := buildTLSConfigFromFiles("", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg == nil {
			t.Fatal("expected non-nil TLS config")
		}
	})
}

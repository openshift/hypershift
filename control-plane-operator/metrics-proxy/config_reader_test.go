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

	t.Run("When components declare metrics filters, it should compile them per component", func(t *testing.T) {
		t.Parallel()

		filterYAML := `
components:
  - name: pod-monitor
    metricsPort: 8080
    metricsFilter: None
  - name: legacy
    metricsPort: 8080
  - name: sre-without-rules
    metricsPort: 8080
    metricsFilter: Relabel
  - name: openshift-apiserver
    metricsPort: 8443
    metricsFilter: Relabel
    metricRelabelConfigs:
      - action: keep
        sourceLabels: ["__name__", "le"]
        regex: "apiserver_request_duration_seconds_bucket;(0.1|\\+Inf)"
      - action: drop
        sourceLabels: ["code"]
        separator: "/"
        regex: "5.."
  - name: unsupported-only
    metricsPort: 8080
    metricsFilter: Relabel
    metricRelabelConfigs:
      - action: labeldrop
        regex: pod
  - name: invalid-regex
    metricsPort: 8080
    metricsFilter: Relabel
    metricRelabelConfigs:
      - action: keep
        sourceLabels: ["__name__"]
        regex: "(unclosed"
  - name: unknown-mode
    metricsPort: 8080
    metricsFilter: Everything
`
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(filterYAML), 0644); err != nil {
			t.Fatalf("failed to write test config: %v", err)
		}
		r := NewConfigFileReader(path, log)
		if err := r.Load(); err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}
		checkCompiledSampleFilters(t, r)
	})
}

// checkCompiledSampleFilters verifies the sample filters compiled for the
// components of the metrics filter test configuration.
func checkCompiledSampleFilters(t *testing.T, r *ConfigFileReader) {
	t.Helper()
	get := func(name string) ComponentConfig {
		t.Helper()
		c, ok := r.GetComponent(name)
		if !ok {
			t.Fatalf("component %s not found", name)
		}
		return c
	}

	for _, name := range []string{"pod-monitor", "legacy"} {
		if f := get(name).sampleFilter; f != nil {
			t.Errorf("When %s has no Relabel filter, it should forward everything, got filter %+v", name, f)
		}
	}
	for _, name := range []string{"sre-without-rules", "invalid-regex", "unknown-mode"} {
		if f := get(name).sampleFilter; f == nil || !f.forwardNothing {
			t.Errorf("When %s cannot forward any sample, it should fail closed, got filter %+v", name, f)
		}
	}

	oas := get("openshift-apiserver").sampleFilter
	if oas == nil || oas.forwardNothing || len(oas.rules) != 2 {
		t.Fatalf("When openshift-apiserver has keep and drop rules, it should compile both, got %+v", oas)
	}
	if !oas.rules[0].keep || oas.rules[0].separator != ";" || oas.rules[0].regex.String() != `^(?s:apiserver_request_duration_seconds_bucket;(0.1|\+Inf))$` {
		t.Errorf("unexpected keep rule: %+v (regex %s)", oas.rules[0], oas.rules[0].regex)
	}
	if oas.rules[1].keep || oas.rules[1].separator != "/" || len(oas.rules[1].sourceLabels) != 1 || oas.rules[1].sourceLabels[0] != "code" {
		t.Errorf("unexpected drop rule: %+v", oas.rules[1])
	}

	if f := get("unsupported-only").sampleFilter; f == nil || f.forwardNothing || len(f.rules) != 0 {
		t.Errorf("When only unsupported actions are configured, it should ignore them and forward samples, got %+v", f)
	}
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

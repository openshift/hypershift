package metricsproxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/metrics"

	"k8s.io/utils/ptr"

	"sigs.k8s.io/yaml"

	"github.com/go-logr/logr"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

type fakeComponentProvider struct {
	components map[string]ComponentConfig
}

func (f *fakeComponentProvider) GetComponent(name string) (ComponentConfig, bool) {
	c, ok := f.components[name]
	return c, ok
}

func (f *fakeComponentProvider) GetComponentNames() []string {
	names := make([]string, 0, len(f.components))
	for k := range f.components {
		names = append(names, k)
	}
	return names
}

type fakeTargetDiscoverer struct {
	targets []ScrapeTarget
	err     error
}

func (f *fakeTargetDiscoverer) Discover(_ context.Context, _ map[string]string, _ int32) ([]ScrapeTarget, error) {
	return f.targets, f.err
}

func parseHostPort(t *testing.T, serverURL string) (string, int32) {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("failed to parse server URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("failed to parse port: %v", err)
	}
	return u.Hostname(), int32(port)
}

func newTestHandler(components map[string]ComponentConfig, discoverer TargetDiscoverer) *ProxyHandler {
	scraper := NewScraper()
	filter := NewFilter(logr.Discard())
	labeler := NewLabeler("test-namespace")

	handler := NewProxyHandler(logr.Discard(), &fakeComponentProvider{components: components}, discoverer, scraper, filter, labeler)
	return handler
}

func TestProxyHandler_ServeHTTP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		path             string
		components       map[string]ComponentConfig
		discoverer       *fakeTargetDiscoverer
		wantStatusCode   int
		wantBodyContains string
	}{
		{
			name: "When path is empty, it should return 400",
			path: "/metrics/",
			components: map[string]ComponentConfig{
				"etcd": {MetricsPort: 2381},
			},
			discoverer:       &fakeTargetDiscoverer{},
			wantStatusCode:   http.StatusBadRequest,
			wantBodyContains: "invalid path",
		},
		{
			name: "When path has nested slashes, it should return 400",
			path: "/metrics/foo/bar",
			components: map[string]ComponentConfig{
				"etcd": {MetricsPort: 2381},
			},
			discoverer:       &fakeTargetDiscoverer{},
			wantStatusCode:   http.StatusBadRequest,
			wantBodyContains: "invalid path",
		},
		{
			name: "When component is unknown, it should return 404",
			path: "/metrics/unknown-component",
			components: map[string]ComponentConfig{
				"etcd": {MetricsPort: 2381},
			},
			discoverer:       &fakeTargetDiscoverer{},
			wantStatusCode:   http.StatusNotFound,
			wantBodyContains: "unknown component",
		},
		{
			name: "When discovery fails, it should return 500",
			path: "/metrics/etcd",
			components: map[string]ComponentConfig{
				"etcd": {MetricsPort: 2381, Selector: map[string]string{"app": "etcd"}},
			},
			discoverer: &fakeTargetDiscoverer{
				err: fmt.Errorf("endpoint-resolver unreachable"),
			},
			wantStatusCode:   http.StatusInternalServerError,
			wantBodyContains: "failed to discover targets",
		},
		{
			name: "When no targets are discovered, it should return 200 with empty body",
			path: "/metrics/etcd",
			components: map[string]ComponentConfig{
				"etcd": {MetricsPort: 2381, Selector: map[string]string{"app": "etcd"}},
			},
			discoverer: &fakeTargetDiscoverer{
				targets: []ScrapeTarget{},
			},
			wantStatusCode:   http.StatusOK,
			wantBodyContains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			handler := newTestHandler(tt.components, tt.discoverer)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil)
			g.Expect(err).NotTo(HaveOccurred())
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			g.Expect(rec.Code).To(Equal(tt.wantStatusCode))
			if tt.wantBodyContains != "" {
				g.Expect(rec.Body.String()).To(ContainSubstring(tt.wantBodyContains))
			}
		})
	}

	t.Run("When scraping succeeds, it should return metrics with injected labels", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Start a fake metrics server that returns a simple gauge metric.
		fakeMetrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, `# HELP test_metric A test metric`)
			fmt.Fprintln(w, `# TYPE test_metric gauge`)
			fmt.Fprintln(w, `test_metric 42`)
		}))
		defer fakeMetrics.Close()

		fakeHost, fakePort := parseHostPort(t, fakeMetrics.URL)

		handler := newTestHandler(
			map[string]ComponentConfig{
				"etcd": {
					MetricsPort:   fakePort,
					MetricsPath:   "/metrics",
					MetricsScheme: "http",
					Selector:      map[string]string{"app": "etcd"},
				},
			},
			&fakeTargetDiscoverer{
				targets: []ScrapeTarget{
					{PodName: "etcd-0", PodIP: fakeHost, Port: fakePort},
				},
			},
		)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/etcd", nil)
		g.Expect(err).NotTo(HaveOccurred())
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusOK))

		body := rec.Body.String()
		g.Expect(body).To(ContainSubstring("test_metric"))
		g.Expect(body).To(ContainSubstring(`pod="etcd-0"`))
		g.Expect(body).To(ContainSubstring(`namespace="test-namespace"`))
		g.Expect(body).To(ContainSubstring(`job="etcd"`))
	})

	t.Run("When all scrape targets fail, it should return 502", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		handler := newTestHandler(
			map[string]ComponentConfig{
				"etcd": {
					MetricsPort:   9999,
					MetricsPath:   "/metrics",
					MetricsScheme: "http",
					Selector:      map[string]string{"app": "etcd"},
				},
			},
			&fakeTargetDiscoverer{
				targets: []ScrapeTarget{
					{PodName: "etcd-0", PodIP: "192.0.2.1", Port: 9999},
				},
			},
		)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/etcd", nil)
		g.Expect(err).NotTo(HaveOccurred())
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusBadGateway))
		g.Expect(rec.Body.String()).To(ContainSubstring("all scrape targets failed"))
	})

	t.Run("When some targets fail, it should return metrics from successful targets with error counter", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		// Start a fake metrics server for the successful target.
		fakeMetrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, `# HELP good_metric A good metric`)
			fmt.Fprintln(w, `# TYPE good_metric gauge`)
			fmt.Fprintln(w, `good_metric 1`)
		}))
		defer fakeMetrics.Close()

		fakeHost, fakePort := parseHostPort(t, fakeMetrics.URL)

		handler := newTestHandler(
			map[string]ComponentConfig{
				"etcd": {
					MetricsPort:   fakePort,
					MetricsPath:   "/metrics",
					MetricsScheme: "http",
					Selector:      map[string]string{"app": "etcd"},
				},
			},
			&fakeTargetDiscoverer{
				targets: []ScrapeTarget{
					{PodName: "etcd-0", PodIP: fakeHost, Port: fakePort},
					{PodName: "etcd-1", PodIP: "192.0.2.1", Port: 9999}, // unreachable
				},
			},
		)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/etcd", nil)
		g.Expect(err).NotTo(HaveOccurred())
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusOK))

		body := rec.Body.String()
		g.Expect(body).To(ContainSubstring("good_metric"))
		g.Expect(body).To(ContainSubstring("metrics_aggregator_pod_scrape_errors_total"))
		g.Expect(body).To(ContainSubstring(`pod="etcd-1"`))
	})

	t.Run("When trailing slash is in path, it should resolve component correctly", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		handler := newTestHandler(
			map[string]ComponentConfig{
				"etcd": {MetricsPort: 2381, Selector: map[string]string{"app": "etcd"}},
			},
			&fakeTargetDiscoverer{targets: []ScrapeTarget{}},
		)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/etcd/", nil)
		g.Expect(err).NotTo(HaveOccurred())
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Trailing slash should be trimmed and resolve to "etcd", returning 200 (empty targets).
		g.Expect(rec.Code).To(Equal(http.StatusOK))
	})

	// The following subtests cover gzip response compression on the success write
	// path, and confirm the error/empty-targets paths are never gzipped.

	// A single shared fake metrics server and handler, so the injected `instance`
	// label (which embeds the server's ephemeral port) is identical across requests
	// and response bodies can be compared. Use t.Cleanup rather than defer: the
	// parallel subtests run after this function returns, so a deferred Close would
	// shut the server down before they execute.
	const metricsFixture = "# HELP test_metric A test metric\n" +
		"# TYPE test_metric gauge\n" +
		"test_metric 42\n"
	fakeMetrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, err := io.WriteString(w, metricsFixture); err != nil {
			t.Errorf("failed to write fake metrics fixture: %v", err)
		}
	}))
	t.Cleanup(fakeMetrics.Close)

	fakeHost, fakePort := parseHostPort(t, fakeMetrics.URL)
	handler := newTestHandler(
		map[string]ComponentConfig{
			"etcd": {
				MetricsPort:   fakePort,
				MetricsPath:   "/metrics",
				MetricsScheme: "http",
				Selector:      map[string]string{"app": "etcd"},
			},
		},
		&fakeTargetDiscoverer{
			targets: []ScrapeTarget{{PodName: "etcd-0", PodIP: fakeHost, Port: fakePort}},
		},
	)

	serve := func(t *testing.T, acceptEncoding string) *httptest.ResponseRecorder {
		t.Helper()
		g := NewWithT(t)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/etcd", nil)
		g.Expect(err).NotTo(HaveOccurred())
		if acceptEncoding != "" {
			req.Header.Set("Accept-Encoding", acceptEncoding)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// Baseline: uncompressed response (no Accept-Encoding).
	g := NewWithT(t)
	plainRec := serve(t, "")
	g.Expect(plainRec.Code).To(Equal(http.StatusOK))
	g.Expect(plainRec.Header().Get("Content-Encoding")).To(BeEmpty())
	plainBody := plainRec.Body.Bytes()
	plainContentType := plainRec.Header().Get("Content-Type")
	g.Expect(plainContentType).NotTo(BeEmpty())
	g.Expect(plainBody).To(ContainSubstring("test_metric"))

	t.Run("When Accept-Encoding advertises gzip, the body gunzips to the uncompressed response and Content-Type is unchanged", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		rec := serve(t, "gzip")

		g.Expect(rec.Code).To(Equal(http.StatusOK))
		g.Expect(rec.Header().Get("Content-Encoding")).To(Equal("gzip"))
		// Content-Type must be preserved.
		g.Expect(rec.Header().Get("Content-Type")).To(Equal(plainContentType))

		// The gzip body must decode to the same exposition content as the plain body.
		gr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
		g.Expect(err).NotTo(HaveOccurred())
		decoded, err := io.ReadAll(gr)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(gr.Close()).To(Succeed())

		g.Expect(canonicalizeExposition(t, decoded)).To(Equal(canonicalizeExposition(t, plainBody)))
		g.Expect(decoded).To(ContainSubstring("test_metric"))
	})

	t.Run("When Accept-Encoding is mixed-case among multiple encodings, it should still gzip", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		rec := serve(t, "deflate, GZip")

		g.Expect(rec.Code).To(Equal(http.StatusOK))
		g.Expect(rec.Header().Get("Content-Encoding")).To(Equal("gzip"))
	})

	t.Run("When Accept-Encoding is absent, it should return plain text with no Content-Encoding", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		rec := serve(t, "")

		g.Expect(rec.Code).To(Equal(http.StatusOK))
		g.Expect(rec.Header().Get("Content-Encoding")).To(BeEmpty())
		g.Expect(rec.Body.String()).To(ContainSubstring("test_metric"))
		g.Expect(canonicalizeExposition(t, rec.Body.Bytes())).To(Equal(canonicalizeExposition(t, plainBody)))
	})

	t.Run("When Accept-Encoding does not include gzip, it should return plain text", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		rec := serve(t, "deflate, br")

		g.Expect(rec.Code).To(Equal(http.StatusOK))
		g.Expect(rec.Header().Get("Content-Encoding")).To(BeEmpty())
		g.Expect(canonicalizeExposition(t, rec.Body.Bytes())).To(Equal(canonicalizeExposition(t, plainBody)))
	})

	t.Run("When Accept-Encoding sets gzip;q=0, it should not gzip (explicitly rejected)", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		rec := serve(t, "gzip;q=0")

		g.Expect(rec.Code).To(Equal(http.StatusOK))
		g.Expect(rec.Header().Get("Content-Encoding")).To(BeEmpty())
		g.Expect(canonicalizeExposition(t, rec.Body.Bytes())).To(Equal(canonicalizeExposition(t, plainBody)))
	})

	t.Run("When Accept-Encoding sets gzip with a non-zero q-value, it should gzip", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		rec := serve(t, "gzip;q=0.5")

		g.Expect(rec.Code).To(Equal(http.StatusOK))
		g.Expect(rec.Header().Get("Content-Encoding")).To(Equal("gzip"))
	})

	t.Run("When a target error path is hit with Accept-Encoding gzip, the error response is not gzipped", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		handler := newTestHandler(
			map[string]ComponentConfig{
				"etcd": {MetricsPort: 2381, Selector: map[string]string{"app": "etcd"}},
			},
			&fakeTargetDiscoverer{err: fmt.Errorf("endpoint-resolver unreachable")},
		)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/etcd", nil)
		g.Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		g.Expect(rec.Header().Get("Content-Encoding")).To(BeEmpty())
		g.Expect(rec.Body.String()).To(ContainSubstring("failed to discover targets"))
	})

	t.Run("When the empty-targets path is hit with Accept-Encoding gzip, the response is not gzipped", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		handler := newTestHandler(
			map[string]ComponentConfig{
				"etcd": {MetricsPort: 2381, Selector: map[string]string{"app": "etcd"}},
			},
			&fakeTargetDiscoverer{targets: []ScrapeTarget{}},
		)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/etcd", nil)
		g.Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusOK))
		g.Expect(rec.Header().Get("Content-Encoding")).To(BeEmpty())
		g.Expect(rec.Body.Len()).To(Equal(0))
	})

	t.Run("When components carry metric relabel rules in the config file, it should forward only the permitted samples", func(t *testing.T) {
		t.Parallel()

		fakeMetrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			if _, err := fmt.Fprint(w, testExposition); err != nil {
				t.Errorf("failed to write metrics fixture: %v", err)
			}
		}))
		// The subtests run in parallel after this function returns.
		t.Cleanup(fakeMetrics.Close)
		fakeHost, fakePort := parseHostPort(t, fakeMetrics.URL)

		allSamples := []string{
			"apiserver_request_duration_seconds_bucket le=+Inf",
			"apiserver_request_duration_seconds_bucket le=0.1",
			"apiserver_request_duration_seconds_bucket le=1",
			"apiserver_request_duration_seconds_count",
			"apiserver_request_duration_seconds_sum",
			"apiserver_request_total",
			"apiserver_request_total",
			"process_resident_memory_bytes",
			"rpc_latency_seconds quantile=0.5",
			"rpc_latency_seconds quantile=0.99",
			"rpc_latency_seconds_count",
			"rpc_latency_seconds_sum",
		}

		tests := []struct {
			name          string
			component     string
			filter        MetricsFilterMode
			rules         []prometheusoperatorv1.RelabelConfig
			failedTarget  bool
			wantSamples   []string
			wantTypeLines []string
		}{
			{
				name:      "When an SRE keep rule selects openshift-apiserver metrics by name, it should return only matching samples",
				component: "openshift-apiserver",
				filter:    MetricsFilterRelabel,
				rules: []prometheusoperatorv1.RelabelConfig{{
					Action:       "keep",
					Regex:        "(apiserver_request_total|apiserver_request_duration_seconds_bucket)",
					SourceLabels: []prometheusoperatorv1.LabelName{"__name__"},
				}},
				wantSamples: []string{
					"apiserver_request_duration_seconds_bucket le=+Inf",
					"apiserver_request_duration_seconds_bucket le=0.1",
					"apiserver_request_duration_seconds_bucket le=1",
					"apiserver_request_total",
					"apiserver_request_total",
				},
				wantTypeLines: []string{"# TYPE apiserver_request_duration_seconds_bucket untyped", "# TYPE apiserver_request_total counter"},
			},
			{
				name:      "When an SRE keep rule selects openshift-apiserver buckets by name and le, it should return only the matching buckets",
				component: "openshift-apiserver",
				filter:    MetricsFilterRelabel,
				rules: []prometheusoperatorv1.RelabelConfig{{
					Action:       "keep",
					Regex:        `apiserver_request_duration_seconds_bucket;(1|\+Inf)`,
					SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "le"},
				}},
				wantSamples: []string{
					"apiserver_request_duration_seconds_bucket le=+Inf",
					"apiserver_request_duration_seconds_bucket le=1",
				},
			},
			{
				name:         "When the SRE set has no rules for a known component, it should return only the synthetic scrape-error metric",
				component:    "openshift-controller-manager",
				filter:       MetricsFilterRelabel,
				failedTarget: true,
				wantSamples:  []string{"metrics_aggregator_pod_scrape_errors_total"},
			},
			{
				name:        "When the Telemetry kube-apiserver keep rule is used, it should return only the kept metrics",
				component:   "kube-apiserver",
				filter:      MetricsFilterRelabel,
				rules:       metrics.KASRelabelConfigs(metrics.MetricsSetTelemetry),
				wantSamples: []string{"apiserver_request_total", "apiserver_request_total"},
			},
			{
				name:        "When the Telemetry kube-scheduler drop .* rule is used, it should return no samples",
				component:   "kube-scheduler",
				filter:      MetricsFilterRelabel,
				rules:       metrics.SchedulerRelabelConfigs(metrics.MetricsSetTelemetry),
				wantSamples: nil,
			},
			{
				name:        "When the Telemetry openshift-route-controller-manager drop (.*) rule is used, it should return no samples",
				component:   "openshift-route-controller-manager",
				filter:      MetricsFilterRelabel,
				rules:       metrics.OpenShiftRouteControllerManagerRelabelConfigs(metrics.MetricsSetTelemetry),
				wantSamples: nil,
			},
			{
				name:          "When the metrics filter is None, as for the All set and PodMonitors, it should return every sample unchanged",
				component:     "cluster-autoscaler",
				filter:        MetricsFilterNone,
				wantSamples:   allSamples,
				wantTypeLines: []string{"# TYPE apiserver_request_duration_seconds histogram", "# TYPE rpc_latency_seconds summary"},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				g := NewWithT(t)

				fileConfig := FileConfig{Components: []ComponentFileConfig{{
					Name:                 tt.component,
					Selector:             map[string]string{"app": tt.component},
					MetricsPort:          fakePort,
					MetricsPath:          "/metrics",
					MetricsScheme:        "http",
					MetricsFilter:        tt.filter,
					MetricRelabelConfigs: tt.rules,
				}}}
				data, err := yaml.Marshal(fileConfig)
				g.Expect(err).NotTo(HaveOccurred())
				configPath := filepath.Join(t.TempDir(), "config.yaml")
				g.Expect(os.WriteFile(configPath, data, 0o600)).To(Succeed())
				reader := NewConfigFileReader(configPath, logr.Discard())
				g.Expect(reader.Load()).To(Succeed())

				targets := []ScrapeTarget{{PodName: tt.component + "-0", PodIP: fakeHost, Port: fakePort}}
				if tt.failedTarget {
					targets = append(targets, ScrapeTarget{PodName: tt.component + "-1", PodIP: "192.0.2.1", Port: 9999})
				}
				handler := NewProxyHandler(logr.Discard(), reader, &fakeTargetDiscoverer{targets: targets},
					NewScraper(), NewFilter(logr.Discard()), NewLabeler("test-namespace"))

				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/"+tt.component, nil)
				g.Expect(err).NotTo(HaveOccurred())
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)

				g.Expect(rec.Code).To(Equal(http.StatusOK))
				body := rec.Body.String()
				g.Expect(exposedSampleKeys(body)).To(Equal(tt.wantSamples))
				for _, typeLine := range tt.wantTypeLines {
					g.Expect(body).To(ContainSubstring(typeLine))
				}
				if len(tt.wantSamples) > 0 && !tt.failedTarget {
					g.Expect(body).To(ContainSubstring(`pod="` + tt.component + `-0"`))
				}
			})
		}
	})

	t.Run("When several targets expose the same metric families, it should encode each family once with one exposition type", func(t *testing.T) {
		t.Parallel()

		// Pod a exposes a complete summary with one quantile; pod b adds a
		// second quantile that the drop rule below removes.
		podA := serveExposition(t, "pod-a", `# TYPE rpc_latency_seconds summary
rpc_latency_seconds{quantile="0.5"} 0.2
rpc_latency_seconds_sum 1
rpc_latency_seconds_count 5
# TYPE requests_total counter
requests_total 1
# TYPE conflicting_metric gauge
conflicting_metric 3
`)
		podB := serveExposition(t, "pod-b", `# TYPE rpc_latency_seconds summary
rpc_latency_seconds{quantile="0.5"} 0.3
rpc_latency_seconds{quantile="0.99"} 0.9
rpc_latency_seconds_sum 2
rpc_latency_seconds_count 7
# TYPE requests_total counter
requests_total 2
# TYPE conflicting_metric counter
conflicting_metric 4
`)

		tests := []struct {
			name           string
			filter         MetricsFilterMode
			rules          []prometheusoperatorv1.RelabelConfig
			wantTypes      map[string]dto.MetricType
			wantSampleKeys []string
		}{
			{
				name:   "When one target's summary is partially filtered and another's is complete, it should expose the family once as untyped",
				filter: MetricsFilterRelabel,
				rules: []prometheusoperatorv1.RelabelConfig{
					{Action: "keep", Regex: "rpc_latency_seconds.*|requests_total", SourceLabels: []prometheusoperatorv1.LabelName{"__name__"}},
					{Action: "drop", Regex: `rpc_latency_seconds;0\.99`, SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "quantile"}},
				},
				wantTypes: map[string]dto.MetricType{
					"requests_total":            dto.MetricType_COUNTER,
					"rpc_latency_seconds":       dto.MetricType_UNTYPED,
					"rpc_latency_seconds_count": dto.MetricType_UNTYPED,
					"rpc_latency_seconds_sum":   dto.MetricType_UNTYPED,
				},
				wantSampleKeys: []string{
					"requests_total pod=pod-a", "requests_total pod=pod-b",
					"rpc_latency_seconds pod=pod-a quantile=0.5", "rpc_latency_seconds pod=pod-b quantile=0.5",
					"rpc_latency_seconds_count pod=pod-a", "rpc_latency_seconds_count pod=pod-b",
					"rpc_latency_seconds_sum pod=pod-a", "rpc_latency_seconds_sum pod=pod-b",
				},
			},
			{
				name:   "When the targets are not filtered, it should expose each typed family once with the series of every target",
				filter: MetricsFilterNone,
				rules:  nil,
				wantTypes: map[string]dto.MetricType{
					"requests_total":      dto.MetricType_COUNTER,
					"rpc_latency_seconds": dto.MetricType_SUMMARY,
				},
				wantSampleKeys: []string{
					"requests_total pod=pod-a", "requests_total pod=pod-b",
					"rpc_latency_seconds pod=pod-a quantile=0.5", "rpc_latency_seconds pod=pod-b quantile=0.5",
					"rpc_latency_seconds pod=pod-b quantile=0.99",
					"rpc_latency_seconds_count pod=pod-a", "rpc_latency_seconds_count pod=pod-b",
					"rpc_latency_seconds_sum pod=pod-a", "rpc_latency_seconds_sum pod=pod-b",
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				g := NewWithT(t)

				handler := newTestHandler(
					map[string]ComponentConfig{
						"openshift-apiserver": {
							MetricsPort:   podA.Port,
							MetricsPath:   "/metrics",
							MetricsScheme: "http",
							sampleFilter:  newSampleFilter(logr.Discard(), "openshift-apiserver", tt.filter, tt.rules),
						},
					},
					&fakeTargetDiscoverer{targets: []ScrapeTarget{
						podA,
						podB,
					}},
				)
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/openshift-apiserver", nil)
				g.Expect(err).NotTo(HaveOccurred())
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				g.Expect(rec.Code).To(Equal(http.StatusOK))

				// A strict text-format reader rejects a metric name declared twice.
				parser := expfmt.NewTextParser(model.LegacyValidation)
				families, err := parser.TextToMetricFamilies(strings.NewReader(rec.Body.String()))
				if tt.filter == MetricsFilterNone {
					// The conflicting_metric types differ between the targets,
					// so its samples stay in separate families, as before.
					g.Expect(rec.Body.String()).To(ContainSubstring("# TYPE conflicting_metric gauge"))
					g.Expect(rec.Body.String()).To(ContainSubstring("# TYPE conflicting_metric counter"))
					g.Expect(strings.Index(rec.Body.String(), "# TYPE conflicting_metric gauge")).
						To(BeNumerically("<", strings.Index(rec.Body.String(), "# TYPE conflicting_metric counter")))
					conflictFreeParser := expfmt.NewTextParser(model.LegacyValidation)
					families, err = conflictFreeParser.TextToMetricFamilies(
						strings.NewReader(withoutFamily(rec.Body.String(), "conflicting_metric")))
				}
				g.Expect(err).NotTo(HaveOccurred(), "response:\n%s", rec.Body.String())
				for name, wantType := range tt.wantTypes {
					g.Expect(families).To(HaveKey(name))
					g.Expect(families[name].GetType()).To(Equal(wantType), name)
				}
				g.Expect(sampleKeysWithPod(families)).To(Equal(tt.wantSampleKeys))
			})
		}
	})

	t.Run("When filtered families from several targets share names, it should keep permitted samples with one family per name in any target order", func(t *testing.T) {
		t.Parallel()

		withPod := func(target ScrapeTarget, pod string) ScrapeTarget {
			target.PodName = pod
			return target
		}
		summaryWithTwoQuantiles := serveExposition(t, "", `# TYPE latency summary
latency{quantile="0.5"} 0.2
latency{quantile="0.99"} 0.9
latency_sum 2
latency_count 7
`)
		standaloneCount := serveExposition(t, "", `# TYPE latency_count gauge
latency_count 9
`)
		gaugeRPC := serveExposition(t, "", `# TYPE rpc_latency_seconds gauge
rpc_latency_seconds 1
`)
		partialRPCSummary := serveExposition(t, "", `# TYPE rpc_latency_seconds summary
rpc_latency_seconds{quantile="0.5"} 0.3
rpc_latency_seconds{quantile="0.99"} 0.9
rpc_latency_seconds_sum 2
rpc_latency_seconds_count 7
`)
		completeRPCSummary := serveExposition(t, "", `# TYPE rpc_latency_seconds summary
rpc_latency_seconds{quantile="0.5"} 0.2
rpc_latency_seconds_sum 1
rpc_latency_seconds_count 5
`)
		dropQuantile := func(family string) prometheusoperatorv1.RelabelConfig {
			return prometheusoperatorv1.RelabelConfig{
				Action: "drop", Regex: family + `;0\.99`, SourceLabels: []prometheusoperatorv1.LabelName{"__name__", "quantile"},
			}
		}
		dropPod := func(pod string) prometheusoperatorv1.RelabelConfig {
			return prometheusoperatorv1.RelabelConfig{Action: "drop", Regex: pod, SourceLabels: []prometheusoperatorv1.LabelName{"pod"}}
		}

		tests := []struct {
			name           string
			targets        []ScrapeTarget
			rules          []prometheusoperatorv1.RelabelConfig
			wantTypes      map[string]dto.MetricType
			wantSampleKeys []string
		}{
			{
				name:    "When a rejected target exposes a family named like a flattened sample of another target, it should keep the permitted sample",
				targets: []ScrapeTarget{withPod(summaryWithTwoQuantiles, "pod-a"), withPod(standaloneCount, "pod-b")},
				rules:   []prometheusoperatorv1.RelabelConfig{dropPod("pod-b"), dropQuantile("latency")},
				wantTypes: map[string]dto.MetricType{
					"latency":       dto.MetricType_UNTYPED,
					"latency_count": dto.MetricType_UNTYPED,
					"latency_sum":   dto.MetricType_UNTYPED,
				},
				wantSampleKeys: []string{"latency pod=pod-a quantile=0.5", "latency_count pod=pod-a", "latency_sum pod=pod-a"},
			},
			{
				name:    "When a surviving family is named like a flattened sample of another target, it should keep only the surviving family",
				targets: []ScrapeTarget{withPod(summaryWithTwoQuantiles, "pod-a"), withPod(standaloneCount, "pod-b")},
				rules:   []prometheusoperatorv1.RelabelConfig{dropQuantile("latency")},
				wantTypes: map[string]dto.MetricType{
					"latency":       dto.MetricType_UNTYPED,
					"latency_count": dto.MetricType_GAUGE,
					"latency_sum":   dto.MetricType_UNTYPED,
				},
				wantSampleKeys: []string{"latency pod=pod-a quantile=0.5", "latency_count pod=pod-b", "latency_sum pod=pod-a"},
			},
			{
				name: "When a filtered-away family of another type comes first, it should still merge the same-type families of later targets",
				targets: []ScrapeTarget{
					withPod(gaugeRPC, "pod-gauge"), withPod(partialRPCSummary, "pod-partial"), withPod(completeRPCSummary, "pod-complete"),
				},
				rules: []prometheusoperatorv1.RelabelConfig{dropPod("pod-gauge"), dropQuantile("rpc_latency_seconds")},
				wantTypes: map[string]dto.MetricType{
					"rpc_latency_seconds":       dto.MetricType_UNTYPED,
					"rpc_latency_seconds_count": dto.MetricType_UNTYPED,
					"rpc_latency_seconds_sum":   dto.MetricType_UNTYPED,
				},
				wantSampleKeys: []string{
					"rpc_latency_seconds pod=pod-complete quantile=0.5", "rpc_latency_seconds pod=pod-partial quantile=0.5",
					"rpc_latency_seconds_count pod=pod-complete", "rpc_latency_seconds_count pod=pod-partial",
					"rpc_latency_seconds_sum pod=pod-complete", "rpc_latency_seconds_sum pod=pod-partial",
				},
			},
		}

		for _, tt := range tests {
			for _, order := range permutations(len(tt.targets)) {
				targets := make([]ScrapeTarget, 0, len(order))
				pods := make([]string, 0, len(order))
				for _, i := range order {
					targets = append(targets, tt.targets[i])
					pods = append(pods, tt.targets[i].PodName)
				}
				t.Run(fmt.Sprintf("%s (target order %s)", tt.name, strings.Join(pods, ",")), func(t *testing.T) {
					t.Parallel()
					g := NewWithT(t)

					handler := newTestHandler(
						map[string]ComponentConfig{
							"openshift-apiserver": {
								MetricsPort:   targets[0].Port,
								MetricsPath:   "/metrics",
								MetricsScheme: "http",
								sampleFilter:  newSampleFilter(logr.Discard(), "openshift-apiserver", MetricsFilterRelabel, tt.rules),
							},
						},
						&fakeTargetDiscoverer{targets: targets},
					)
					req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/openshift-apiserver", nil)
					g.Expect(err).NotTo(HaveOccurred())
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					g.Expect(rec.Code).To(Equal(http.StatusOK))

					parser := expfmt.NewTextParser(model.LegacyValidation)
					families, err := parser.TextToMetricFamilies(strings.NewReader(rec.Body.String()))
					g.Expect(err).NotTo(HaveOccurred(), "response:\n%s", rec.Body.String())
					g.Expect(families).To(HaveLen(len(tt.wantTypes)), "response:\n%s", rec.Body.String())
					for name, wantType := range tt.wantTypes {
						g.Expect(families).To(HaveKey(name))
						g.Expect(families[name].GetType()).To(Equal(wantType), name)
					}
					g.Expect(sampleKeysWithPod(families)).To(Equal(tt.wantSampleKeys))
				})
			}
		}
	})
}

var bucketOrQuantileLabel = regexp.MustCompile(`(?:^|,)(le|quantile)="([^"]*)"`)

// exposedSampleKeys returns the sorted sample names of a text exposition, with
// the le or quantile label value appended when present.
func exposedSampleKeys(body string) []string {
	var keys []string
	for line := range strings.SplitSeq(body, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest, _ := strings.Cut(line, "{")
		name, _, _ = strings.Cut(name, " ")
		if match := bucketOrQuantileLabel.FindStringSubmatch(rest); match != nil {
			name += " " + match[1] + "=" + match[2]
		}
		keys = append(keys, name)
	}
	slices.Sort(keys)
	return keys
}

// withoutFamily removes the comment and sample lines of one metric family from
// a text exposition.
func withoutFamily(body, family string) string {
	var lines []string
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, family+" ") || strings.HasPrefix(line, family+"{") ||
			strings.HasPrefix(line, "# TYPE "+family+" ") || strings.HasPrefix(line, "# HELP "+family+" ") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// sampleKeysWithPod returns the sorted samples of the parsed families as
// "<name> pod=<pod>" plus the quantile when present. Only counter, untyped
// and summary families are expanded, which is all the multi-target fixture
// exposes.
func sampleKeysWithPod(families map[string]*dto.MetricFamily) []string {
	labelValue := func(m *dto.Metric, name string) string {
		for _, lp := range m.Label {
			if lp.GetName() == name {
				return lp.GetValue()
			}
		}
		return ""
	}
	var keys []string
	for name, mf := range families {
		for _, m := range mf.Metric {
			pod := " pod=" + labelValue(m, "pod")
			switch mf.GetType() {
			case dto.MetricType_SUMMARY:
				for _, q := range m.GetSummary().GetQuantile() {
					keys = append(keys, fmt.Sprintf("%s%s quantile=%s", name, pod, strconv.FormatFloat(q.GetQuantile(), 'g', -1, 64)))
				}
				keys = append(keys, name+"_sum"+pod, name+"_count"+pod)
			default:
				if quantile := labelValue(m, "quantile"); quantile != "" {
					keys = append(keys, name+pod+" quantile="+quantile)
					continue
				}
				keys = append(keys, name+pod)
			}
		}
	}
	slices.Sort(keys)
	return keys
}

// serveExposition starts a metrics endpoint that serves the given text
// exposition until the test and its subtests complete, and returns it as a
// scrape target for pod.
func serveExposition(t *testing.T, pod, exposition string) ScrapeTarget {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, err := fmt.Fprint(w, exposition); err != nil {
			t.Errorf("failed to write metrics fixture: %v", err)
		}
	}))
	// Parallel subtests run after the parent test function returns.
	t.Cleanup(server.Close)
	host, port := parseHostPort(t, server.URL)
	return ScrapeTarget{PodName: pod, PodIP: host, Port: port}
}

// permutations returns every ordering of the indexes 0..n-1.
func permutations(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var result [][]int
	for _, rest := range permutations(n - 1) {
		for i := 0; i <= len(rest); i++ {
			order := append(append(append([]int{}, rest[:i]...), n-1), rest[i:]...)
			result = append(result, order)
		}
	}
	return result
}

// canonicalizeExposition parses Prometheus exposition text and re-encodes it with
// the labels of each series sorted by name. Labeler.Inject appends labels by
// ranging over a Go map, so the proxy emits them in a nondeterministic order;
// canonicalizing makes response comparisons insensitive to that ordering while
// still detecting any change to metric names, sample values, or labels.
func canonicalizeExposition(t *testing.T, body []byte) string {
	t.Helper()
	g := NewWithT(t)

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	g.Expect(err).NotTo(HaveOccurred())

	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	slices.Sort(names)

	var buf bytes.Buffer
	enc := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, name := range names {
		mf := families[name]
		for _, m := range mf.Metric {
			slices.SortFunc(m.Label, func(a, b *dto.LabelPair) int {
				return strings.Compare(a.GetName(), b.GetName())
			})
		}
		g.Expect(enc.Encode(mf)).To(Succeed())
	}
	return buf.String()
}

func TestBuildScrapeErrorMetric(t *testing.T) {
	t.Parallel()

	t.Run("When targets fail, it should produce one counter per failed target", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		targets := []ScrapeTarget{
			{PodName: "pod-a", PodIP: "10.0.0.1", Port: 8080},
			{PodName: "pod-b", PodIP: "10.0.0.2", Port: 8080},
		}

		mf := buildScrapeErrorMetric("kube-apiserver", targets)

		g.Expect(mf.GetName()).To(Equal("metrics_aggregator_pod_scrape_errors_total"))
		g.Expect(mf.GetType()).To(Equal(dto.MetricType_COUNTER))
		g.Expect(mf.Metric).To(HaveLen(2))

		for i, m := range mf.Metric {
			g.Expect(m.Counter.GetValue()).To(Equal(float64(1)))

			labels := make(map[string]string)
			for _, lp := range m.Label {
				labels[lp.GetName()] = lp.GetValue()
			}
			g.Expect(labels["component"]).To(Equal("kube-apiserver"))
			g.Expect(labels["pod"]).To(Equal(targets[i].PodName))
		}
	})
}

func TestRequireClientCert(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	handler := requireClientCert(inner)

	t.Run("When no TLS connection, it should return 401", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/etcd", nil)
		g.Expect(err).NotTo(HaveOccurred())
		req.TLS = nil
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		g.Expect(rec.Body.String()).To(ContainSubstring("client certificate required"))
	})

	t.Run("When TLS but no verified chains, it should return 401", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/etcd", nil)
		g.Expect(err).NotTo(HaveOccurred())
		req.TLS = &tls.ConnectionState{VerifiedChains: nil}
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		g.Expect(rec.Body.String()).To(ContainSubstring("client certificate required"))
	})

	t.Run("When TLS with verified chains, it should pass through to next handler", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/etcd", nil)
		g.Expect(err).NotTo(HaveOccurred())
		req.TLS = &tls.ConnectionState{
			VerifiedChains: [][]*x509.Certificate{{}},
		}
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusOK))
		body, _ := io.ReadAll(rec.Body)
		g.Expect(string(body)).To(ContainSubstring("ok"))
	})

}

func TestAddToBatches(t *testing.T) {
	t.Parallel()

	family := func(name string, metricType dto.MetricType, pod string) *dto.MetricFamily {
		return &dto.MetricFamily{
			Name:   ptr.To(name),
			Type:   metricType.Enum(),
			Metric: []*dto.Metric{{Label: []*dto.LabelPair{{Name: ptr.To("pod"), Value: ptr.To(pod)}}}},
		}
	}
	targets := []map[string]*dto.MetricFamily{
		{"rpc": family("rpc", dto.MetricType_GAUGE, "t1"), "other": family("other", dto.MetricType_COUNTER, "t1")},
		{"rpc": family("rpc", dto.MetricType_SUMMARY, "t2")},
		{"rpc": family("rpc", dto.MetricType_SUMMARY, "t3"), "other": family("other", dto.MetricType_COUNTER, "t3")},
		{"rpc": family("rpc", dto.MetricType_HISTOGRAM, "t4")},
		{"rpc": family("rpc", dto.MetricType_GAUGE, "t5")},
	}

	t.Run("When families of the same name have several types, it should group each type across all targets", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		batches := []map[string]*dto.MetricFamily{make(map[string]*dto.MetricFamily)}
		for _, families := range targets {
			batches = addToBatches(batches, families)
		}

		pods := func(mf *dto.MetricFamily) []string {
			var result []string
			for _, m := range mf.Metric {
				result = append(result, m.Label[0].GetValue())
			}
			return result
		}
		g.Expect(batches).To(HaveLen(3))
		g.Expect(batches[0]).To(HaveLen(2))
		g.Expect(batches[0]["rpc"].GetType()).To(Equal(dto.MetricType_GAUGE))
		g.Expect(pods(batches[0]["rpc"])).To(Equal([]string{"t1", "t5"}))
		g.Expect(batches[0]["other"].GetType()).To(Equal(dto.MetricType_COUNTER))
		g.Expect(pods(batches[0]["other"])).To(Equal([]string{"t1", "t3"}))
		g.Expect(batches[1]).To(HaveLen(1))
		g.Expect(batches[1]["rpc"].GetType()).To(Equal(dto.MetricType_SUMMARY))
		g.Expect(pods(batches[1]["rpc"])).To(Equal([]string{"t2", "t3"}))
		g.Expect(batches[2]).To(HaveLen(1))
		g.Expect(batches[2]["rpc"].GetType()).To(Equal(dto.MetricType_HISTOGRAM))
		g.Expect(pods(batches[2]["rpc"])).To(Equal([]string{"t4"}))
	})

	t.Run("When a target has no families, it should leave the batches unchanged", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		batches := []map[string]*dto.MetricFamily{make(map[string]*dto.MetricFamily)}
		batches = addToBatches(batches, map[string]*dto.MetricFamily{})
		g.Expect(batches).To(HaveLen(1))
		g.Expect(batches[0]).To(BeEmpty())
	})
}

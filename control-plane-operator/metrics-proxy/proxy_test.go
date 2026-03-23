package metricsproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	dto "github.com/prometheus/client_model/go"
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
	filter := NewFilter("All")
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

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
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

		req := httptest.NewRequest(http.MethodGet, "/metrics/etcd", nil)
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

		req := httptest.NewRequest(http.MethodGet, "/metrics/etcd", nil)
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

		req := httptest.NewRequest(http.MethodGet, "/metrics/etcd", nil)
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

		req := httptest.NewRequest(http.MethodGet, "/metrics/etcd/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// Trailing slash should be trimmed and resolve to "etcd", returning 200 (empty targets).
		g.Expect(rec.Code).To(Equal(http.StatusOK))
	})

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

		req := httptest.NewRequest(http.MethodGet, "/metrics/etcd", nil)
		req.TLS = nil
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		g.Expect(rec.Body.String()).To(ContainSubstring("client certificate required"))
	})

	t.Run("When TLS but no verified chains, it should return 401", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req := httptest.NewRequest(http.MethodGet, "/metrics/etcd", nil)
		req.TLS = &tls.ConnectionState{VerifiedChains: nil}
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		g.Expect(rec.Code).To(Equal(http.StatusUnauthorized))
		g.Expect(rec.Body.String()).To(ContainSubstring("client certificate required"))
	})

	t.Run("When TLS with verified chains, it should pass through to next handler", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		req := httptest.NewRequest(http.MethodGet, "/metrics/etcd", nil)
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

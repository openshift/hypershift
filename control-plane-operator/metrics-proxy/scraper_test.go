package metricsproxy

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	. "github.com/onsi/gomega"
)

type scrapeRoundTripper func(*http.Request) (*http.Response, error)

func (transport scrapeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type scrapeResponseBody struct {
	io.Reader
	closed bool
}

func (body *scrapeResponseBody) Close() error {
	body.closed = true
	return nil
}

func TestScrape(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, host, scheme, path, want string
		port                           int32
	}{
		{name: "When the pod address is raw IPv6, it should request a bracketed authority", host: "2001:db8::1", port: 9090, want: "https://[2001:db8::1]:9090/metrics"},
		{name: "When the pod address is IPv4, it should preserve the authority", host: "192.0.2.1", port: 9090, want: "https://192.0.2.1:9090/metrics"},
		{name: "When the target is DNS, it should preserve the authority", host: "metrics.example.test", port: 9090, want: "https://metrics.example.test:9090/metrics"},
		{name: "When HTTP and a relative path are selected, it should normalize the path", host: "2001:db8::1", port: 8080, scheme: "http", path: "custom/metrics", want: "http://[2001:db8::1]:8080/custom/metrics"},
		{name: "When HTTPS and an absolute path are selected, it should retain them", host: "2001:db8::1", port: 8443, scheme: "https", path: "/custom/metrics", want: "https://[2001:db8::1]:8443/custom/metrics"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			target := ScrapeTarget{PodIP: test.host, Port: test.port}
			g.Expect(scrapeURL(target, test.path, test.scheme)).To(Equal(test.want), "production scraper URL must retain the exact authority and path")
			body := &scrapeResponseBody{Reader: strings.NewReader("# TYPE endpoint_metric gauge\nendpoint_metric 1\n")}
			var observed *http.Request
			client := &http.Client{Transport: scrapeRoundTripper(func(request *http.Request) (*http.Response, error) {
				observed = request
				return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
			})}
			ctx := t.Context()
			result := NewScraper().scrape(ctx, client, target, test.path, test.scheme)
			g.Expect(result.Err).ToNot(HaveOccurred(), "scrape must construct and execute its production request")
			g.Expect(observed).ToNot(BeNil(), "client transport must receive the actual scraper request")
			g.Expect(observed.URL.String()).To(Equal(test.want), "actual request must use the production URL")
			g.Expect(observed.Method).To(Equal(http.MethodGet), "scrape must use GET")
			g.Expect(observed.Context()).To(BeIdenticalTo(ctx), "scrape must retain cancellation context")
			g.Expect(result.Families).To(HaveKey("endpoint_metric"), "response metrics must still be parsed")
			g.Expect(body.closed).To(BeTrue(), "scrape must close the response body")
		})
	}
}

func TestBuildScrapeClient(t *testing.T) {
	t.Parallel()

	t.Run("When called with a TLS config, it should set DisableKeepAlives on the transport", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		client := buildScrapeClient("", tlsCfg)

		transport, ok := client.Transport.(*http.Transport)
		g.Expect(ok).To(BeTrue(), "transport should be *http.Transport")
		g.Expect(transport.DisableKeepAlives).To(BeTrue())
	})

	t.Run("When called with a nil TLS config, it should set DisableKeepAlives on the transport", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		client := buildScrapeClient("", nil)

		transport, ok := client.Transport.(*http.Transport)
		g.Expect(ok).To(BeTrue(), "transport should be *http.Transport")
		g.Expect(transport.DisableKeepAlives).To(BeTrue())
		g.Expect(transport.TLSClientConfig.MinVersion).To(Equal(uint16(tls.VersionTLS12)))
	})

	t.Run("When called with a TLS server name, it should set it on the transport", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		client := buildScrapeClient("my-server.example.com", nil)

		transport, ok := client.Transport.(*http.Transport)
		g.Expect(ok).To(BeTrue())
		g.Expect(transport.TLSClientConfig.ServerName).To(Equal("my-server.example.com"))
	})

	t.Run("When called with a TLS config, it should clone it rather than mutate the original", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		original := &tls.Config{MinVersion: tls.VersionTLS12}
		buildScrapeClient("override.example.com", original)

		g.Expect(original.ServerName).To(BeEmpty(), "original TLS config should not be mutated")
	})
}

func TestScrapeAll(t *testing.T) {
	t.Parallel()

	t.Run("When scraping multiple targets, it should return a result for each target", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		var requestCount atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, `# HELP test_gauge A test gauge`)
			fmt.Fprintln(w, `# TYPE test_gauge gauge`)
			fmt.Fprintln(w, `test_gauge 1`)
		}))
		defer server.Close()

		host, port := parseHostPort(t, server.URL)

		scraper := NewScraper()
		targets := []ScrapeTarget{
			{PodName: "pod-0", PodIP: host, Port: port},
			{PodName: "pod-1", PodIP: host, Port: port},
			{PodName: "pod-2", PodIP: host, Port: port},
		}

		results := scraper.ScrapeAll(t.Context(), targets, "/metrics", "http", "", nil)

		g.Expect(results).To(HaveLen(3))
		for i, r := range results {
			g.Expect(r.Err).NotTo(HaveOccurred(), "target %d should succeed", i)
			g.Expect(r.Families).To(HaveKey("test_gauge"))
		}
		g.Expect(int(requestCount.Load())).To(Equal(3))
	})

	t.Run("When a target is unreachable, it should return an error for that target only", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, `# TYPE ok_metric gauge`)
			fmt.Fprintln(w, `ok_metric 1`)
		}))
		defer server.Close()

		host, port := parseHostPort(t, server.URL)

		scraper := NewScraper()
		targets := []ScrapeTarget{
			{PodName: "good-pod", PodIP: host, Port: port},
			{PodName: "bad-pod", PodIP: "192.0.2.1", Port: 1},
		}

		results := scraper.ScrapeAll(t.Context(), targets, "/metrics", "http", "", nil)

		g.Expect(results).To(HaveLen(2))
		g.Expect(results[0].Err).NotTo(HaveOccurred())
		g.Expect(results[0].Families).To(HaveKey("ok_metric"))
		g.Expect(results[1].Err).To(HaveOccurred())
	})

	t.Run("When scraping completes, it should not leak idle connections", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		var activeConns atomic.Int32
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintln(w, `# TYPE conn_metric gauge`)
			fmt.Fprintln(w, `conn_metric 1`)
		}))
		server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
			switch state {
			case http.StateNew:
				activeConns.Add(1)
			case http.StateClosed:
				activeConns.Add(-1)
			}
		}
		server.Start()
		defer server.Close()

		host, port := parseHostPort(t, server.URL)

		scraper := NewScraper()
		targets := []ScrapeTarget{
			{PodName: "pod-0", PodIP: host, Port: port},
		}

		results := scraper.ScrapeAll(t.Context(), targets, "/metrics", "http", "", nil)
		g.Expect(results[0].Err).NotTo(HaveOccurred())

		g.Eventually(func() int32 {
			return activeConns.Load()
		}).Should(Equal(int32(0)), "all connections should be closed after ScrapeAll returns")
	})

	t.Run("When the target returns non-200, it should return an error with the status code", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}))
		defer server.Close()

		host, port := parseHostPort(t, server.URL)

		scraper := NewScraper()
		targets := []ScrapeTarget{
			{PodName: "pod-0", PodIP: host, Port: port},
		}

		results := scraper.ScrapeAll(t.Context(), targets, "/metrics", "http", "", nil)

		g.Expect(results[0].Err).To(HaveOccurred())
		g.Expect(results[0].Err.Error()).To(ContainSubstring("403"))
	})
}

package metricsproxy

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

type ProxyHandler struct {
	log               logr.Logger
	componentProvider ComponentProvider
	targetDiscoverer  TargetDiscoverer
	scraper           *Scraper
	filter            *Filter
	labeler           *Labeler
}

func NewProxyHandler(log logr.Logger, componentProvider ComponentProvider, targetDiscoverer TargetDiscoverer, scraper *Scraper, filter *Filter, labeler *Labeler) *ProxyHandler {
	return &ProxyHandler{
		log:               log,
		componentProvider: componentProvider,
		targetDiscoverer:  targetDiscoverer,
		scraper:           scraper,
		filter:            filter,
		labeler:           labeler,
	}
}

func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract component name from path: /metrics/<component-name>
	path := strings.TrimPrefix(r.URL.Path, "/metrics/")
	path = strings.TrimSuffix(path, "/")
	if path == "" || strings.Contains(path, "/") {
		http.Error(w, "invalid path: expected /metrics/<component-name>", http.StatusBadRequest)
		return
	}
	componentName := path

	// Look up the component configuration.
	componentConfig, ok := h.componentProvider.GetComponent(componentName)
	if !ok {
		http.Error(w, fmt.Sprintf("unknown component: %s", componentName), http.StatusNotFound)
		return
	}

	// Authentication is enforced by the requireClientCert middleware which
	// verifies a client certificate was presented and validated against the
	// cluster-signer-ca CA bundle (via VerifyClientCertIfGiven at the TLS layer).

	// Discover pods
	targets, err := h.targetDiscoverer.Discover(ctx, componentConfig.Selector, componentConfig.MetricsPort)
	if err != nil {
		h.log.Error(err, "discovery error", "component", componentName)
		http.Error(w, "failed to discover targets", http.StatusInternalServerError)
		return
	}

	if len(targets) == 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Scrape all targets
	results := h.scraper.ScrapeAll(ctx, targets, componentConfig.MetricsPath, componentConfig.MetricsScheme, componentConfig.TLSServerName, componentConfig.TLSConfig)

	// Merge, filter, and label results
	format := expfmt.NewFormat(expfmt.TypeTextPlain)
	var buf bytes.Buffer
	encoder := expfmt.NewEncoder(&buf, format)

	successes := 0
	var failedTargets []ScrapeTarget
	for i, result := range results {
		if result.Err != nil {
			h.log.Error(result.Err, "scrape error", "target", targets[i].PodName)
			failedTargets = append(failedTargets, targets[i])
			continue
		}
		successes++

		families := h.filter.Apply(componentName, result.Families)
		families = h.labeler.Inject(families, targets[i], componentName, componentConfig)

		for _, mf := range families {
			if err := encoder.Encode(mf); err != nil {
				h.log.Error(err, "encode error")
			}
		}
	}

	// Emit synthetic scrape error counter for failed targets.
	if len(failedTargets) > 0 {
		errorFamily := buildScrapeErrorMetric(componentName, failedTargets)
		if err := encoder.Encode(errorFamily); err != nil {
			h.log.Error(err, "encode error for scrape error metric")
		}
	}

	if successes == 0 {
		http.Error(w, "all scrape targets failed", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", string(format))

	// Prometheus requests gzip by default on scrapes (scrape config
	// enable_compression, default true since v2.49). The guest-side
	// control-plane-metrics-forwarder is a TCP (TLS passthrough) proxy, so gzip is
	// negotiated end to end between Prometheus and this handler and shrinks the
	// on-the-wire bytes on both the cross-AZ and PrivateLink legs. The exposition
	// text compresses by roughly 30-45x. See OCPBUGS-132731.
	if acceptsGzip(r) {
		// Content-Encoding must be set before WriteHeader; the gzip writer streams
		// the body, so no (now-stale) Content-Length is set.
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		gz := gzip.NewWriter(w)
		if _, err := gz.Write(buf.Bytes()); err != nil {
			h.log.Error(err, "failed to write gzip response body")
		}
		// Close flushes any buffered data and writes the gzip trailer.
		if err := gz.Close(); err != nil {
			h.log.Error(err, "failed to close gzip writer")
		}
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// acceptsGzip reports whether the request's Accept-Encoding header accepts gzip.
// It matches the gzip content-coding case-insensitively and honors an explicit
// "q=0", which marks the coding as not acceptable (RFC 9110 §12.5.3). Full
// q-value ranking is intentionally not implemented: Prometheus sends a plain
// "gzip", and we only need a yes/no decision, not a preferred-coding selection.
func acceptsGzip(r *http.Request) bool {
	for part := range strings.SplitSeq(r.Header.Get("Accept-Encoding"), ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		return !hasZeroQValue(params)
	}
	return false
}

// hasZeroQValue reports whether an Accept-Encoding parameter string sets the
// quality value to zero (e.g. "q=0" or "q=0.000"), disabling the coding.
func hasZeroQValue(params string) bool {
	for p := range strings.SplitSeq(params, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "q") {
			continue
		}
		if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && q == 0 {
			return true
		}
	}
	return false
}

// buildScrapeErrorMetric creates a synthetic metrics_aggregator_pod_scrape_errors_total
// counter metric family with one metric per failed scrape target.
func buildScrapeErrorMetric(componentName string, failedTargets []ScrapeTarget) *dto.MetricFamily {
	name := "metrics_aggregator_pod_scrape_errors_total"
	help := "Total number of pod scrape errors per component and pod."
	metricType := dto.MetricType_COUNTER

	metrics := make([]*dto.Metric, 0, len(failedTargets))
	for _, t := range failedTargets {
		compLabel := "component"
		podLabel := "pod"
		compValue := componentName
		podValue := t.PodName
		counterValue := float64(1)

		metrics = append(metrics, &dto.Metric{
			Label: []*dto.LabelPair{
				{Name: &compLabel, Value: &compValue},
				{Name: &podLabel, Value: &podValue},
			},
			Counter: &dto.Counter{Value: &counterValue},
		})
	}

	return &dto.MetricFamily{
		Name:   &name,
		Help:   &help,
		Type:   &metricType,
		Metric: metrics,
	}
}

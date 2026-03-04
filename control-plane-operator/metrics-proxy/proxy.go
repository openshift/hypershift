package metricsproxy

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
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

	// Authentication is handled at the TLS layer via mTLS (RequireAndVerifyClientCert).
	// By the time a request reaches this handler, the client certificate has already
	// been verified against the cluster-signer-ca CA bundle.

	// Discover pods
	targets, err := h.targetDiscoverer.Discover(ctx, componentConfig.ServiceName, componentConfig.MetricsPort)
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
	for i, result := range results {
		if result.Err != nil {
			h.log.Error(result.Err, "scrape error", "target", targets[i].PodName)
			continue
		}
		successes++

		families := h.filter.Apply(componentName, result.Families)
		families = h.labeler.Inject(families, targets[i], componentName, componentConfig.ServiceName)

		for _, mf := range families {
			if err := encoder.Encode(mf); err != nil {
				h.log.Error(err, "encode error")
			}
		}
	}

	if successes == 0 {
		http.Error(w, "all scrape targets failed", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", string(format))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

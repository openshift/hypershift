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
	log                 logr.Logger
	authenticator       *TokenAuthenticator
	componentDiscoverer *ComponentDiscoverer
	endpointDiscoverer  *EndpointSliceDiscoverer
	scraper             *Scraper
	filter              *Filter
	labeler             *Labeler
}

func NewProxyHandler(log logr.Logger, authenticator *TokenAuthenticator, componentDiscoverer *ComponentDiscoverer, endpointDiscoverer *EndpointSliceDiscoverer, scraper *Scraper, filter *Filter, labeler *Labeler) *ProxyHandler {
	return &ProxyHandler{
		log:                 log,
		authenticator:       authenticator,
		componentDiscoverer: componentDiscoverer,
		endpointDiscoverer:  endpointDiscoverer,
		scraper:             scraper,
		filter:              filter,
		labeler:             labeler,
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

	// Look up the component from the runtime-discovered ServiceMonitors.
	componentConfig, ok := h.componentDiscoverer.GetComponent(componentName)
	if !ok {
		http.Error(w, fmt.Sprintf("unknown component: %s", componentName), http.StatusNotFound)
		return
	}

	// Authenticate
	token := extractBearerToken(r)
	if token == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}

	authenticated, err := h.authenticator.Authenticate(ctx, token)
	if err != nil {
		h.log.Error(err, "authentication error")
		w.Header().Set("Retry-After", "30")
		http.Error(w, "authentication service unavailable", http.StatusServiceUnavailable)
		return
	}
	if !authenticated {
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}

	// Discover pods
	targets, err := h.endpointDiscoverer.Discover(ctx, componentConfig.ServiceName, componentConfig.MetricsPort)
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

	for i, result := range results {
		if result.Err != nil {
			h.log.Error(result.Err, "scrape error", "target", targets[i].PodName)
			continue
		}

		families := h.filter.Apply(componentName, result.Families)
		families = h.labeler.Inject(families, targets[i], componentName, componentConfig.ServiceName)

		for _, mf := range families {
			if err := encoder.Encode(mf); err != nil {
				h.log.Error(err, "encode error")
			}
		}
	}

	w.Header().Set("Content-Type", string(format))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}

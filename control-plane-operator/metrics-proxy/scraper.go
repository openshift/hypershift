package metricsproxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const (
	scrapeTimeout = 10 * time.Second
)

type ScrapeResult struct {
	Families map[string]*dto.MetricFamily
	Err      error
}

type Scraper struct{}

func NewScraper() *Scraper {
	return &Scraper{}
}

// ScrapeAll scrapes all targets in parallel using the per-component TLS config.
// The tlsConfig is discovered from the component's ServiceMonitor and contains
// the correct CA, client cert, and client key for that specific component.
func (s *Scraper) ScrapeAll(ctx context.Context, targets []ScrapeTarget, metricsPath, scheme, tlsServerName string, tlsConfig *tls.Config) []ScrapeResult {
	client := buildScrapeClient(tlsServerName, tlsConfig)

	results := make([]ScrapeResult, len(targets))
	var wg sync.WaitGroup

	for i, target := range targets {
		wg.Add(1)
		go func(idx int, t ScrapeTarget) {
			defer wg.Done()
			results[idx] = s.scrape(ctx, client, t, metricsPath, scheme)
		}(i, target)
	}

	wg.Wait()
	return results
}

func buildScrapeClient(tlsServerName string, tlsConfig *tls.Config) *http.Client {
	var tlsCfg *tls.Config
	if tlsConfig != nil {
		tlsCfg = tlsConfig.Clone()
	} else {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if tlsServerName != "" {
		tlsCfg.ServerName = tlsServerName
	}
	return &http.Client{
		Timeout: scrapeTimeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}
}

func (s *Scraper) scrape(ctx context.Context, client *http.Client, target ScrapeTarget, metricsPath, scheme string) ScrapeResult {
	if scheme == "" {
		scheme = "https"
	}

	url := fmt.Sprintf("%s://%s:%d%s", scheme, target.PodIP, target.Port, metricsPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ScrapeResult{Err: fmt.Errorf("failed to create request for %s: %w", url, err)}
	}

	resp, err := client.Do(req)
	if err != nil {
		return ScrapeResult{Err: fmt.Errorf("failed to scrape %s: %w", url, err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return ScrapeResult{Err: fmt.Errorf("scrape %s returned status %d: %s", url, resp.StatusCode, string(body))}
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return ScrapeResult{Err: fmt.Errorf("failed to parse metrics from %s: %w", url, err)}
	}

	return ScrapeResult{Families: families}
}

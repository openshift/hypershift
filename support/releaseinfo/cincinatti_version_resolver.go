package releaseinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	defaultArch          = "multi"
	defaultCincinnatiURL = "https://api.openshift.com/api/upgrades_info/v1/graph"
	cacheTTL             = 10 * time.Minute
)

// VersionResolver resolves an OpenShift version string to a release image pullspec.
type VersionResolver interface {
	// Resolve resolves a version to a release image pullspec using the given Cincinnati channel.
	// The channel should be the full Cincinnati channel string (e.g., "stable-4.20").
	Resolve(ctx context.Context, version, channel string) (string, error)
}

type cacheEntry struct {
	releaseImage string
	expiry       time.Time
}

// CincinnatiVersionResolver resolves OpenShift versions to release image pullspecs
// by querying the Cincinnati graph API (api.openshift.com).
type CincinnatiVersionResolver struct {
	client  *http.Client
	baseURL string

	mu    sync.RWMutex
	cache map[string]cacheEntry
}

// NewCincinnatiVersionResolver creates a CincinnatiVersionResolver with default settings.
func NewCincinnatiVersionResolver() *CincinnatiVersionResolver {
	return &CincinnatiVersionResolver{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: defaultCincinnatiURL,
		cache:   make(map[string]cacheEntry),
	}
}

// cincinnatiGraph represents the Cincinnati graph API response.
type cincinnatiGraph struct {
	Nodes []cincinnatiNode `json:"nodes"`
}

type cincinnatiNode struct {
	Version  string            `json:"version"`
	Payload  string            `json:"payload"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Resolve resolves an OpenShift version (e.g., "4.20.1") to a fully qualified
// release image pullspec by querying the Cincinnati graph API with the given channel.
func (r *CincinnatiVersionResolver) Resolve(ctx context.Context, version, channel string) (string, error) {
	cacheKey := channel + "/" + version

	// Check cache
	r.mu.RLock()
	if entry, ok := r.cache[cacheKey]; ok && time.Now().Before(entry.expiry) {
		r.mu.RUnlock()
		return entry.releaseImage, nil
	}
	r.mu.RUnlock()

	releaseImage, err := r.fetchVersion(ctx, version, channel)
	if err != nil {
		return "", err
	}

	// Update cache
	r.mu.Lock()
	r.cache[cacheKey] = cacheEntry{
		releaseImage: releaseImage,
		expiry:       time.Now().Add(cacheTTL),
	}
	r.mu.Unlock()

	return releaseImage, nil
}

func (r *CincinnatiVersionResolver) fetchVersion(ctx context.Context, version, channel string) (string, error) {
	url := fmt.Sprintf("%s?channel=%s&arch=%s", r.baseURL, channel, defaultArch)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to query Cincinnati API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("cincinnati API returned status %d: %s", resp.StatusCode, string(body))
	}

	var graph cincinnatiGraph
	if err := json.NewDecoder(resp.Body).Decode(&graph); err != nil {
		return "", fmt.Errorf("failed to decode Cincinnati API response: %w", err)
	}

	for _, node := range graph.Nodes {
		if node.Version == version {
			if node.Payload == "" {
				return "", fmt.Errorf("empty payload for version %q in Cincinnati graph", version)
			}
			return node.Payload, nil
		}
	}

	return "", fmt.Errorf("version %q not found in Cincinnati graph for channel %q", version, channel)
}

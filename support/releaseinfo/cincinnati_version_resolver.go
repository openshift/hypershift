package releaseinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"github.com/blang/semver"
)

const (
	defaultArch          = "multi"
	defaultCincinnatiURL = "https://api.openshift.com/api/upgrades_info/graph"
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
	channels     sets.Set[string]
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
	// Derive the Cincinnati channel. Use the input channel if set, otherwise default to "fast-<major>.<minor>".
	if channel == "" {
		parsedVersion, err := semver.Parse(version)
		if err != nil {
			return "", fmt.Errorf("failed to parse version %q: %w", version, err)
		}
		channel = fmt.Sprintf("fast-%d.%d", parsedVersion.Major, parsedVersion.Minor)
	}

	arch := defaultArch

	cacheKey := arch + "/" + version

	// Check cache
	r.mu.RLock()
	if entry, ok := r.cache[cacheKey]; ok && time.Now().Before(entry.expiry) {
		r.mu.RUnlock()
		if !entry.channels.Has(channel) {
			return "", fmt.Errorf("%q not found in channel %q, although it is in %s", version, channel, strings.Join(sets.List(entry.channels), ", "))
		}
		return entry.releaseImage, nil
	}
	r.mu.RUnlock()

	entry, err := r.fetchVersion(ctx, version, channel, arch)
	if err != nil {
		return "", err
	}

	// Update cache
	r.mu.Lock()
	r.cache[cacheKey] = entry
	r.mu.Unlock()

	return entry.releaseImage, nil
}

func (r *CincinnatiVersionResolver) fetchVersion(ctx context.Context, version, channel, arch string) (cacheEntry, error) {
	url := fmt.Sprintf("%s?channel=%s&arch=%s", r.baseURL, channel, arch)
	entry := cacheEntry{}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return entry, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.redhat.cincinnati.v1+json")

	resp, err := r.client.Do(req)
	if err != nil {
		return entry, fmt.Errorf("failed to query Cincinnati API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return entry, fmt.Errorf("cincinnati API returned status %d: %s", resp.StatusCode, string(body))
	}

	var graph cincinnatiGraph
	if err := json.NewDecoder(resp.Body).Decode(&graph); err != nil {
		return entry, fmt.Errorf("failed to decode Cincinnati API response: %w", err)
	}

	for _, node := range graph.Nodes {
		if node.Version == version {
			if node.Payload == "" {
				return entry, fmt.Errorf("empty payload for version %q in Cincinnati graph", version)
			}
			entry.expiry = time.Now().Add(cacheTTL)
			entry.releaseImage = node.Payload
			entry.channels = sets.New[string]()
			channelsString := node.Metadata["io.openshift.upgrades.graph.release.channels"]
			channels := strings.Split(channelsString, ",")
			for _, ch := range channels {
				if len(ch) > 0 {
					entry.channels.Insert(ch)
				}
			}
			if entry.channels.Len() == 0 {
				klog.V(2).Infof("no non-empty, comma-delimited channels in io.openshift.upgrades.graph.release.channels for %q in %s: %q", version, req.URL, channelsString)
				entry.channels.Insert(channel)
			}
			return entry, nil
		}
	}

	return entry, fmt.Errorf("version %q not found in Cincinnati graph for channel %q", version, channel)
}

//go:build e2ev2

package lifecycle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const ManifestFileName = "cluster-manifests.json"

// ClusterManifest captures the identity of all clusters for a CI run.
// It is written to SHARED_DIR before any infrastructure is provisioned
// so that destroy-guests can always clean up, even if the HostedCluster
// CR was never applied to the management cluster.
type ClusterManifest struct {
	Clusters []ClusterEntry `json:"clusters"`
}

// ClusterEntry captures the deterministic identity of a single cluster.
type ClusterEntry struct {
	Variant   string `json:"variant"`
	Name      string `json:"name"`
	InfraID   string `json:"infraID"`
	Namespace string `json:"namespace"`
}

func WriteManifest(dir string, m *ClusterManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling cluster manifest: %w", err)
	}
	path := filepath.Join(dir, ManifestFileName)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing cluster manifest to %s: %w", path, err)
	}
	return nil
}

func ReadManifest(dir string) (*ClusterManifest, error) {
	path := filepath.Join(dir, ManifestFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading cluster manifest from %s: %w", path, err)
	}
	var m ClusterManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshaling cluster manifest: %w", err)
	}
	return &m, nil
}

// LookupCluster returns the ClusterEntry for the given variant.
func (m *ClusterManifest) LookupCluster(variant string) (ClusterEntry, error) {
	for _, c := range m.Clusters {
		if c.Variant == variant {
			return c, nil
		}
	}
	return ClusterEntry{}, fmt.Errorf("no cluster entry for variant %q", variant)
}

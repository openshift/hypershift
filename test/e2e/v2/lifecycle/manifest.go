//go:build e2ev2

package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/distribution/reference"
)

const ManifestFileName = "cluster-manifests.json"

// ClusterManifest captures the identity and destroy arguments of all clusters for a CI run.
// It is written to SHARED_DIR before any infrastructure is provisioned
// so that destroy-guests can always clean up, even if the HostedCluster
// CR was never applied to the management cluster.
type ClusterManifest struct {
	Clusters      []ClusterEntry    `json:"clusters"`
	ReleaseImages map[string]string `json:"releaseImages,omitempty"`
}

// ClusterEntry captures the deterministic identity and destroy arguments of a single cluster.
type ClusterEntry struct {
	Variant           string           `json:"variant"`
	Name              string           `json:"name"`
	InfraID           string           `json:"infraID"`
	Namespace         string           `json:"namespace"`
	DestroyArgs       []string         `json:"destroyArgs,omitempty"`
	ExpectedNodePools map[string]int32 `json:"expectedNodePools"`
}

func (entry *ClusterEntry) UnmarshalJSON(data []byte) error {
	type clusterEntry ClusterEntry
	decoded := struct {
		*clusterEntry
		ExpectedNodePools *json.RawMessage `json:"expectedNodePools"`
	}{clusterEntry: (*clusterEntry)(entry)}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	entry.ExpectedNodePools = nil
	if decoded.ExpectedNodePools == nil {
		return nil
	}
	pools, err := ParseExpectedNodePools(string(*decoded.ExpectedNodePools))
	if err != nil {
		return err
	}
	entry.ExpectedNodePools = pools
	return nil
}

func WriteManifest(dir string, m *ClusterManifest) error {
	for _, cluster := range m.Clusters {
		if err := ValidateExpectedNodePools(cluster.ExpectedNodePools); err != nil {
			return err
		}
	}
	if err := validateReleaseImages(m.ReleaseImages); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling cluster manifest: %w", err)
	}
	path := filepath.Join(dir, ManifestFileName)
	if err := writeManifestAtomically(path, data, newManifestTempFile); err != nil {
		return fmt.Errorf("writing cluster manifest to %s: %w", path, err)
	}
	return nil
}

type manifestTempFile interface {
	io.WriteCloser
	Name() string
	Sync() error
}

func newManifestTempFile(dir, pattern string) (manifestTempFile, error) {
	return os.CreateTemp(dir, pattern)
}

func writeManifestAtomically(path string, data []byte, createTemp func(string, string) (manifestTempFile, error)) (resultErr error) {
	temporary, err := createTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	closed, renamed := false, false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, temporary.Close())
		}
		if !renamed {
			resultErr = errors.Join(resultErr, os.Remove(temporary.Name()))
		}
	}()
	written, err := temporary.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	err = temporary.Close()
	closed = true
	if err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	renamed = true
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
	for _, cluster := range m.Clusters {
		if err := ValidateExpectedNodePools(cluster.ExpectedNodePools); err != nil {
			return nil, err
		}
	}
	if err := validateReleaseImages(m.ReleaseImages); err != nil {
		return nil, err
	}
	return &m, nil
}

func validateReleaseImages(images map[string]string) error {
	for name, image := range images {
		switch name {
		case "E2E_LATEST_RELEASE_IMAGE", "E2E_PREVIOUS_RELEASE_IMAGE", "E2E_N1_RELEASE_IMAGE", "E2E_N2_RELEASE_IMAGE", "E2E_N3_RELEASE_IMAGE", "E2E_N4_RELEASE_IMAGE":
		default:
			return fmt.Errorf("unsupported release image input %q in cluster manifest", name)
		}
		if _, err := reference.ParseNormalizedNamed(image); err != nil {
			return fmt.Errorf("invalid release image reference for %s in cluster manifest", name)
		}
	}
	return nil
}

// ReleaseImagesFromEnvironment captures nonsecret suite release-image inputs for
// the create/run handoff. Missing historical images remain absent; latestImage
// is used only as the fallback for E2E_LATEST_RELEASE_IMAGE.
func ReleaseImagesFromEnvironment(latestImage string) map[string]string {
	images := map[string]string{}
	for _, mapping := range [][2]string{
		{"E2E_LATEST_RELEASE_IMAGE", "RELEASE_IMAGE_LATEST"},
		{"E2E_PREVIOUS_RELEASE_IMAGE", "OCP_IMAGE_N1"},
		{"E2E_N1_RELEASE_IMAGE", "OCP_IMAGE_N1"},
		{"E2E_N2_RELEASE_IMAGE", "OCP_IMAGE_N2"},
		{"E2E_N3_RELEASE_IMAGE", "OCP_IMAGE_N3"},
		{"E2E_N4_RELEASE_IMAGE", "OCP_IMAGE_N4"},
	} {
		value := os.Getenv(mapping[0])
		if value == "" {
			value = os.Getenv(mapping[1])
		}
		if value == "" && mapping[0] == "E2E_LATEST_RELEASE_IMAGE" {
			value = latestImage
		}
		if value != "" {
			images[mapping[0]] = value
		}
	}
	return images
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

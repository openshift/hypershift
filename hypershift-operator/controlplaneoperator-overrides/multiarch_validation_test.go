package controlplaneoperatoroverrides

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

// ociPlatform represents a platform entry in an OCI image index manifest.
type ociPlatform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

// ociManifestDescriptor represents a single manifest entry in a manifest list or OCI image index.
type ociManifestDescriptor struct {
	Platform *ociPlatform `json:"platform,omitempty"`
}

// ociIndex represents a Docker manifest list or OCI image index.
type ociIndex struct {
	MediaType string                  `json:"mediaType,omitempty"`
	Manifests []ociManifestDescriptor `json:"manifests"`
}

// collectUniqueOverrideImages returns a deduplicated list of all cpoImage references
// across all platforms in the given CPOOverrides.
func collectUniqueOverrideImages(o *CPOOverrides) []string {
	seen := map[string]struct{}{}
	var images []string

	addImages := func(platformOverrides *CPOPlatformOverrides) {
		if platformOverrides == nil {
			return
		}
		for _, override := range platformOverrides.Overrides {
			if override.CPOImage == "" {
				continue
			}
			// Trim any trailing whitespace that might exist in YAML values
			image := strings.TrimSpace(override.CPOImage)
			if _, ok := seen[image]; !ok {
				seen[image] = struct{}{}
				images = append(images, image)
			}
		}
	}

	addImages(o.Platforms.AWS)
	addImages(o.Platforms.Azure)
	return images
}

// inspectImageArchitectures uses skopeo to inspect a container image and returns the
// list of architectures found in its manifest list.
func inspectImageArchitectures(image string) ([]string, error) {
	cmd := exec.Command("skopeo", "inspect", "--raw", fmt.Sprintf("docker://%s", image))
	output, err := cmd.Output()
	if err != nil {
		var stderr string
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
		}
		return nil, fmt.Errorf("skopeo inspect failed for %s: %v, stderr: %s", image, err, stderr)
	}

	var index ociIndex
	if err := json.Unmarshal(output, &index); err != nil {
		return nil, fmt.Errorf("failed to parse manifest for %s: %v", image, err)
	}

	if len(index.Manifests) == 0 {
		return nil, fmt.Errorf("image %s does not have a manifest list (single-arch image or unsupported format)", image)
	}

	var architectures []string
	for _, m := range index.Manifests {
		if m.Platform != nil && m.Platform.Architecture != "" {
			architectures = append(architectures, m.Platform.Architecture)
		}
	}
	return architectures, nil
}

func TestParseMultiarchManifest(t *testing.T) {
	g := NewWithT(t)

	rawManifest := `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.docker.distribution.manifest.list.v2+json",
		"manifests": [
			{
				"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
				"digest": "sha256:aaa",
				"size": 1234,
				"platform": {
					"architecture": "amd64",
					"os": "linux"
				}
			},
			{
				"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
				"digest": "sha256:bbb",
				"size": 1234,
				"platform": {
					"architecture": "arm64",
					"os": "linux"
				}
			},
			{
				"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
				"digest": "sha256:ccc",
				"size": 1234,
				"platform": {
					"architecture": "s390x",
					"os": "linux"
				}
			}
		]
	}`

	var index ociIndex
	err := json.Unmarshal([]byte(rawManifest), &index)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(index.Manifests).To(HaveLen(3))

	var architectures []string
	for _, m := range index.Manifests {
		if m.Platform != nil {
			architectures = append(architectures, m.Platform.Architecture)
		}
	}
	g.Expect(architectures).To(ContainElements("amd64", "arm64"))
}

func TestParseSingleArchManifest(t *testing.T) {
	g := NewWithT(t)

	// A single-arch manifest has no "manifests" array
	rawManifest := `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
		"config": {
			"mediaType": "application/vnd.docker.container.image.v1+json",
			"digest": "sha256:aaa",
			"size": 1234
		},
		"layers": []
	}`

	var index ociIndex
	err := json.Unmarshal([]byte(rawManifest), &index)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(index.Manifests).To(BeEmpty())
}

func TestOverrideImagesHaveMultiarchSupport(t *testing.T) {
	skopeoPath, err := exec.LookPath("skopeo")
	if err != nil || skopeoPath == "" {
		t.Skip("skopeo is not available, skipping multiarch validation test")
	}

	g := NewWithT(t)

	o, err := loadOverrides(overridesYAML)
	g.Expect(err).ToNot(HaveOccurred(), "failed to load overrides.yaml")
	g.Expect(o).ToNot(BeNil())

	images := collectUniqueOverrideImages(o)
	g.Expect(images).ToNot(BeEmpty(), "no override images found in overrides.yaml")

	t.Logf("Validating multiarch support for %d unique override images", len(images))

	requiredArchitectures := []string{"amd64", "arm64"}
	var failures []string

	for _, image := range images {
		architectures, err := inspectImageArchitectures(image)
		if err != nil {
			failures = append(failures, fmt.Sprintf("  %s: %v", image, err))
			continue
		}

		archSet := map[string]struct{}{}
		for _, arch := range architectures {
			archSet[arch] = struct{}{}
		}

		var missingArchs []string
		for _, required := range requiredArchitectures {
			if _, ok := archSet[required]; !ok {
				missingArchs = append(missingArchs, required)
			}
		}

		if len(missingArchs) > 0 {
			failures = append(failures, fmt.Sprintf("  %s: missing architectures %v (found: %v)", image, missingArchs, architectures))
		} else {
			t.Logf("  OK: %s (architectures: %v)", image, architectures)
		}
	}

	g.Expect(failures).To(BeEmpty(),
		fmt.Sprintf("The following override images lack required multiarch support (need amd64 and arm64):\n%s", strings.Join(failures, "\n")))
}

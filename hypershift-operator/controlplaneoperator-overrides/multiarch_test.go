//go:build validate_override_images

package controlplaneoperatoroverrides

import (
	"encoding/json"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

// manifestEntry represents a single platform manifest within a manifest list.
type manifestEntry struct {
	Platform struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"platform"`
}

// imageManifest represents the top-level structure returned by skopeo inspect --raw
// for manifest list images (Docker manifest list v2 or OCI image index).
type imageManifest struct {
	MediaType string          `json:"mediaType"`
	Manifests []manifestEntry `json:"manifests"`
}

// TestOverrideImagesMultiArch validates that all CPO override images referenced in
// overrides.yaml are manifest lists containing at least amd64 and arm64 architectures.
// This test requires skopeo to be installed and network access to container registries.
// It is gated behind the validate_override_images build tag to prevent it from running
// during normal unit test execution.
func TestOverrideImagesMultiArch(t *testing.T) {
	g := NewWithT(t)

	skopeoPath, err := exec.LookPath("skopeo")
	if err != nil {
		t.Fatal("skopeo not found in PATH; this test requires skopeo when the validate_override_images build tag is active")
	}
	t.Logf("Using skopeo at: %s", skopeoPath)

	images := AllOverrideImages()
	g.Expect(images).ToNot(BeEmpty(), "Expected at least one override image to validate")
	t.Logf("Validating %d unique override images", len(images))

	for image, refs := range images {
		t.Run(sanitizeTestName(image), func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			refsList := strings.Join(refs, ", ")

			// Inspect the image manifest using skopeo
			cmd := exec.CommandContext(t.Context(), skopeoPath, "inspect", "--raw", fmt.Sprintf("docker://%s", image))
			output, err := cmd.CombinedOutput()
			g.Expect(err).ToNot(HaveOccurred(),
				"Failed to inspect image %s (used by: %s): %s", image, refsList, string(output))

			// Parse the manifest
			var manifest imageManifest
			err = json.Unmarshal(output, &manifest)
			g.Expect(err).ToNot(HaveOccurred(),
				"Failed to parse manifest JSON for image %s", image)

			// Verify it's a manifest list
			isManifestList := manifest.MediaType == "application/vnd.docker.distribution.manifest.list.v2+json" ||
				manifest.MediaType == "application/vnd.oci.image.index.v1+json"

			// Some registries omit mediaType in the response but still return a manifest list
			if !isManifestList && len(manifest.Manifests) > 0 {
				isManifestList = true
			}

			g.Expect(isManifestList).To(BeTrue(),
				"Image %s is not a manifest list (mediaType: %s), used by: %s",
				image, manifest.MediaType, refsList)

			// Extract architectures
			archs := make(map[string]bool)
			for _, entry := range manifest.Manifests {
				archs[entry.Platform.Architecture] = true
			}
			archList := slices.Sorted(maps.Keys(archs))

			g.Expect(archs).To(HaveKey("amd64"),
				"Image %s missing amd64 architecture, used by: %s. Available architectures: %v",
				image, refsList, archList)
			g.Expect(archs).To(HaveKey("arm64"),
				"Image %s missing arm64 architecture, used by: %s. Available architectures: %v",
				image, refsList, archList)

			t.Logf("Image %s: OK (architectures: %v, used by %d version(s))",
				image, archList, len(refs))
		})
	}
}

// sanitizeTestName replaces characters that are problematic in test names with underscores.
func sanitizeTestName(image string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"@", "_",
		":", "_",
	)
	return replacer.Replace(image)
}

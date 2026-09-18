//go:build e2ev2

package lifecycle

import (
	"os"
	"path/filepath"
	"testing"
)

// testPlansDir holds the test plans that ship in the hypershift-tests image
// (see Dockerfile.e2e) and are selected by CI through the TEST_PLAN env var.
const testPlansDir = "../testplans"

// TestShippedTestPlansAreValid parses every test plan that ships with the
// image and validates it against the ClusterSpecs of the platform it targets.
// Without this, renaming a variant would only be caught when the periodic job
// runs.
func TestShippedTestPlansAreValid(t *testing.T) {
	specsByPlatform := map[string][]ClusterSpec{
		"aws":   (&AWSPlatformConfig{}).ClusterSpecs("release", "n1-release"),
		"azure": (&AzurePlatformConfig{}).ClusterSpecs("release", "n1-release"),
	}

	entries, err := os.ReadDir(testPlansDir)
	if err != nil {
		t.Fatalf("reading %s: %v", testPlansDir, err)
	}

	var found int
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		found++
		t.Run(entry.Name(), func(t *testing.T) {
			path := filepath.Join(testPlansDir, entry.Name())
			plan, err := ReadTestPlan(path)
			if err != nil {
				t.Fatalf("ReadTestPlan(%s): %v", path, err)
			}
			specs, ok := specsByPlatform[plan.Platform]
			if !ok {
				t.Fatalf("test plan %s targets unknown platform %q", path, plan.Platform)
			}
			if err := plan.Validate(specs); err != nil {
				t.Errorf("test plan %s is invalid: %v", path, err)
			}
		})
	}

	if found == 0 {
		t.Fatalf("no test plans found in %s", testPlansDir)
	}
}

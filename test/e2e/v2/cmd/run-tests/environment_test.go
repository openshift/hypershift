//go:build e2ev2

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

func TestSetupTestEnvironment(t *testing.T) {
	for _, name := range []string{"E2E_LATEST_RELEASE_IMAGE", "RELEASE_IMAGE_LATEST", "E2E_PREVIOUS_RELEASE_IMAGE", "OCP_IMAGE_N1", "E2E_N1_RELEASE_IMAGE", "E2E_N2_RELEASE_IMAGE", "OCP_IMAGE_N2", "E2E_N3_RELEASE_IMAGE", "OCP_IMAGE_N3", "E2E_N4_RELEASE_IMAGE", "OCP_IMAGE_N4", "E2E_SERVICE_DOMAIN", "HYPERSHIFT_EXTERNAL_DNS_DOMAIN", "PULL_SECRET_FILE", "PULL_SECRET"} {
		t.Setenv(name, "")
	}
	for _, mapping := range [][2]string{
		{"E2E_LATEST_RELEASE_IMAGE", "RELEASE_IMAGE_LATEST"},
		{"E2E_PREVIOUS_RELEASE_IMAGE", "OCP_IMAGE_N1"},
		{"E2E_N1_RELEASE_IMAGE", "OCP_IMAGE_N1"},
		{"E2E_N2_RELEASE_IMAGE", "OCP_IMAGE_N2"},
		{"E2E_N3_RELEASE_IMAGE", "OCP_IMAGE_N3"},
		{"E2E_N4_RELEASE_IMAGE", "OCP_IMAGE_N4"},
		{"E2E_SERVICE_DOMAIN", "HYPERSHIFT_EXTERNAL_DNS_DOMAIN"},
		{"PULL_SECRET_FILE", "PULL_SECRET"},
	} {
		t.Run("When "+mapping[1]+" is provided, it should propagate to "+mapping[0], func(t *testing.T) {
			t.Setenv(mapping[0], "")
			t.Setenv(mapping[1], "configured-input")
			setupTestEnvironment()
			if os.Getenv(mapping[0]) != "configured-input" {
				t.Fatal("input was not propagated")
			}
		})
		t.Run("When "+mapping[0]+" is explicitly provided, it should preserve the override", func(t *testing.T) {
			t.Setenv(mapping[0], "suite-override")
			t.Setenv(mapping[1], "creation-input")
			setupTestEnvironment()
			if os.Getenv(mapping[0]) != "suite-override" {
				t.Fatal("suite override was overwritten")
			}
		})
		t.Run("When "+mapping[1]+" is absent, it should leave the suite input empty", func(t *testing.T) {
			t.Setenv(mapping[0], "")
			t.Setenv(mapping[1], "")
			setupTestEnvironment()
			if os.Getenv(mapping[0]) != "" {
				t.Fatal("absent prerequisite was invented")
			}
		})
	}
}

func TestSetupManifestEnvironmentProcess(t *testing.T) {
	const latest = "quay.io/openshift-release-dev/ocp-release:4.22.0-x86_64"
	const previous = "quay.io/openshift-release-dev/ocp-release:4.21.10-x86_64"
	const n2Override = "quay.io/openshift-release-dev/ocp-release:4.20.11-x86_64"
	if os.Getenv("RELEASE_IMAGE_HANDOFF_TEST") == "1" {
		setupTestEnvironment()
		manifest, err := lifecycle.ReadManifest(os.Getenv("SHARED_DIR"))
		if err != nil {
			t.Fatal(err)
		}
		if err := setupManifestEnvironment(manifest); err != nil {
			t.Fatal(err)
		}
		for name, expected := range map[string]string{"E2E_LATEST_RELEASE_IMAGE": latest, "E2E_PREVIOUS_RELEASE_IMAGE": previous, "E2E_N1_RELEASE_IMAGE": previous, "E2E_N2_RELEASE_IMAGE": n2Override, "E2E_N3_RELEASE_IMAGE": "", "E2E_N4_RELEASE_IMAGE": ""} {
			if os.Getenv(name) != expected {
				t.Fatalf("create/run process handoff did not retain the expected %s", name)
			}
		}
		return
	}
	t.Run("When run lacks create's sourced environment, it should recover validated images and retain explicit run overrides", func(t *testing.T) {
		imageVariables := []string{"E2E_LATEST_RELEASE_IMAGE", "RELEASE_IMAGE_LATEST", "E2E_PREVIOUS_RELEASE_IMAGE", "OCP_IMAGE_N1", "E2E_N1_RELEASE_IMAGE", "E2E_N2_RELEASE_IMAGE", "OCP_IMAGE_N2", "E2E_N3_RELEASE_IMAGE", "OCP_IMAGE_N3", "E2E_N4_RELEASE_IMAGE", "OCP_IMAGE_N4"}
		for _, name := range imageVariables {
			t.Setenv(name, "")
		}
		t.Setenv("OCP_IMAGE_N1", previous)
		t.Setenv("OCP_IMAGE_N2", "quay.io/openshift-release-dev/ocp-release:4.20.10-x86_64")
		dir := t.TempDir()
		if err := lifecycle.WriteManifest(dir, &lifecycle.ClusterManifest{ReleaseImages: lifecycle.ReleaseImagesFromEnvironment(latest)}); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSetupManifestEnvironmentProcess$")
		for _, entry := range os.Environ() {
			name, _, _ := strings.Cut(entry, "=")
			keep := true
			for _, imageVariable := range imageVariables {
				if name == imageVariable {
					keep = false
				}
			}
			if keep && name != "SHARED_DIR" && name != "RELEASE_IMAGE_HANDOFF_TEST" {
				command.Env = append(command.Env, entry)
			}
		}
		command.Env = append(command.Env, "RELEASE_IMAGE_HANDOFF_TEST=1", "SHARED_DIR="+dir, "E2E_N2_RELEASE_IMAGE="+n2Override)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("separate run-process image handoff failed: %v\n%s", err, output)
		}
	})
}

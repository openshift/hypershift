package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, field, value, remove, wantError string
	}{
		{name: "When the source is pinned, it should record deterministic provenance and detect changed input contents"},
		{name: "When the source module is unexpected, it should reject the metadata", field: "Path", value: "github.com/openshift/hypershift", wantError: "unexpected upstream module"},
		{name: "When the version is missing, it should reject incomplete metadata", field: "Version", wantError: "incomplete"},
		{name: "When the module checksum is missing, it should reject incomplete metadata", field: "Sum", wantError: "incomplete"},
		{name: "When the module-file checksum is missing, it should reject incomplete metadata", field: "GoModSum", wantError: "incomplete"},
		{name: "When the revision is missing, it should reject incomplete metadata", field: "Hash", wantError: "full Git revision"},
		{name: "When the revision is malformed, it should reject the metadata", field: "Hash", value: "not-a-git-revision", wantError: "full Git revision"},
		{name: "When a CRD fixture is missing, it should fail before writing provenance", remove: "charts/operator/crds/monitoring.googleapis.com_rules.yaml", wantError: "no such file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream, output := t.TempDir(), t.TempDir()
			for _, name := range []string{
				"pkg/operator/apis/monitoring/v1/pod_types.go",
				"pkg/operator/apis/monitoring/v1/transport_types.go",
				"pkg/operator/apis/monitoring/v1/ignored_test.go",
				"charts/operator/crds/monitoring.googleapis.com_podmonitorings.yaml",
				"charts/operator/crds/monitoring.googleapis.com_rules.yaml",
			} {
				if name == tc.remove {
					continue
				}
				path := filepath.Join(upstream, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("source"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			info := map[string]interface{}{
				"Path": "github.com/GoogleCloudPlatform/prometheus-engine", "Version": "v0.17.2",
				"Sum": "h1:module", "GoModSum": "h1:module-file",
				"Origin": map[string]string{"Hash": "058752ec791edefba5c42486eb9237562c3f0d29"},
			}
			if tc.field == "Hash" {
				info["Origin"].(map[string]string)["Hash"] = tc.value
			} else if tc.field != "" {
				info[tc.field] = tc.value
			}
			data, err := json.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			sourceInfo := filepath.Join(t.TempDir(), "source.json")
			if err := os.WriteFile(sourceInfo, data, 0644); err != nil {
				t.Fatal(err)
			}
			err = writeProvenance(upstream, sourceInfo, output)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q, got %v", tc.wantError, err)
				}
				if _, err := os.Stat(filepath.Join(output, "upstream.sha256")); !os.IsNotExist(err) {
					t.Fatal("failed generation should not write provenance")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(output, "upstream.sha256")
			first, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{"# Version: v0.17.2", "# Revision: 058752ec791edefba5c42486eb9237562c3f0d29", "# Module sum: h1:module", "transport_types.go", "monitoring.googleapis.com_rules.yaml"} {
				if !strings.Contains(string(first), required) {
					t.Fatalf("missing provenance %q", required)
				}
			}
			if strings.Contains(string(first), "ignored_test.go") {
				t.Fatal("upstream tests are not extraction inputs")
			}
			if err := writeProvenance(upstream, sourceInfo, output); err != nil {
				t.Fatal(err)
			}
			second, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if string(first) != string(second) {
				t.Fatal("provenance changed on identical inputs")
			}
			if err := os.WriteFile(filepath.Join(upstream, "pkg/operator/apis/monitoring/v1/transport_types.go"), []byte("changed"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := writeProvenance(upstream, sourceInfo, output); err != nil {
				t.Fatal(err)
			}
			third, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if string(first) == string(third) {
				t.Fatal("changed input contents did not change the recorded checksum")
			}
		})
	}
}

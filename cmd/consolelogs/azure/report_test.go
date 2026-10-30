package azure

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyArtifacts(t *testing.T) {
	for _, tt := range []struct {
		name            string
		expected        []string
		status, content string
		compressed      bool
		bytes           int64
		wantError       bool
	}{
		{"When serial content matches the summary, it should validate", []string{"worker-0"}, StatusCollected, "serial output", false, 13, false},
		{"When the expected inventory is empty, it should reject a vacuous pass", nil, StatusCollected, "serial output", false, 13, true},
		{"When an expected machine is missing, it should fail", []string{"other"}, StatusCollected, "serial output", false, 13, true},
		{"When the expected inventory repeats a machine, it should reject the inventory", []string{"worker-0", "worker-0"}, StatusCollected, "serial output", false, 13, true},
		{"When collection failed but an old file exists, it should reject the stale file", []string{"worker-0"}, StatusFailed, "serial output", false, 13, true},
		{"When compressed content is empty, it should fail", []string{"worker-0"}, StatusCollected, "", true, 1, true},
		{"When content differs from the recorded length, it should fail", []string{"worker-0"}, StatusCollected, "serial output", false, 100, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file, err := os.Create(filepath.Join(dir, "serial.log"))
			if err != nil {
				t.Fatal(err)
			}
			if tt.compressed {
				gz := gzip.NewWriter(file)
				_, err = gz.Write([]byte(tt.content))
				if closeErr := gz.Close(); err == nil {
					err = closeErr
				}
			} else {
				_, err = file.WriteString(tt.content)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			report := CollectionReport{Machines: []MachineResult{{Name: "worker-0", Serial: ArtifactResult{Status: tt.status, Path: "serial.log", Bytes: tt.bytes, Compressed: tt.compressed}}}}
			err = VerifyArtifacts(dir, report, tt.expected, false)
			if (err != nil) != tt.wantError {
				t.Fatalf("got %v", err)
			}
			if !tt.wantError && VerifyArtifacts(dir, report, tt.expected, true) == nil {
				t.Fatal("missing journals passed validation")
			}
		})
	}
}

//go:build e2ev2

package tests

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequiredGCPFile(t *testing.T) {
	for _, envName := range []string{"E2E_GCP_CREDENTIALS_FILE", "E2E_GCP_CONTROL_PLANE_PROJECT_ID_FILE"} {
		t.Run("When "+envName+" is unset, it should report the missing input", func(t *testing.T) {
			t.Setenv(envName, "")
			_, err := requiredGCPFile(envName)
			if err == nil || !strings.Contains(err.Error(), envName) {
				t.Fatalf("expected an error identifying %s, got %v", envName, err)
			}
		})
		t.Run("When "+envName+" points to a missing file, it should return the filesystem error", func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "missing")
			t.Setenv(envName, file)
			_, err := requiredGCPFile(envName)
			if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), envName) || !strings.Contains(err.Error(), file) {
				t.Fatalf("expected a missing-file error identifying %s and %s, got %v", envName, file, err)
			}
		})
		t.Run("When "+envName+" points to an existing file, it should return the path", func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(file, []byte("test"), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(envName, file)
			got, err := requiredGCPFile(envName)
			if err != nil || got != file {
				t.Fatalf("expected %s and no error, got %s, %v", file, got, err)
			}
		})
	}
}

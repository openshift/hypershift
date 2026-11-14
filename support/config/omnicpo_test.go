package config

import (
	"testing"

	"github.com/blang/semver"
)

func TestOmniCPOSupportsVersion(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"4.19.0", false},
		{"4.20.0", true},
		{"4.20.42", true},
		{"4.21.0", true},
		{"4.22.0", true},
		{"4.23.0", true},
		{"5.0.0", true},
		{"5.0.0-rc.4", true},
		{"5.1.0", false},
	}
	for _, tc := range tests {
		if got := OmniCPOSupportsVersion(semver.MustParse(tc.version)); got != tc.want {
			t.Errorf("OmniCPOSupportsVersion(%s) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

func TestOmniCPOCheckVersion(t *testing.T) {
	t.Run("off is a no-op even for an unsupported version", func(t *testing.T) {
		t.Setenv(OmniCPOEnvVar, "0")
		if err := OmniCPOCheckVersion("4.19.0"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})
	t.Run("on and supported", func(t *testing.T) {
		t.Setenv(OmniCPOEnvVar, "1")
		if err := OmniCPOCheckVersion("4.20.0"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})
	t.Run("on and unsupported errors", func(t *testing.T) {
		t.Setenv(OmniCPOEnvVar, "1")
		if err := OmniCPOCheckVersion("4.19.0"); err == nil {
			t.Error("expected error, got nil")
		}
	})
	t.Run("on and unparsable errors", func(t *testing.T) {
		t.Setenv(OmniCPOEnvVar, "1")
		if err := OmniCPOCheckVersion("not-a-version"); err == nil {
			t.Error("expected error, got nil")
		}
	})
}

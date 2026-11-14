package controlplaneoperatoroverrides

import (
	"testing"

	"github.com/blang/semver"
)

func TestShouldUseFromMain(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		versions string
		version  string
		want     bool
	}{
		{"disabled", false, "4.20", "4.20.5", false},
		{"enabled, in list", true, "4.20", "4.20.5", true},
		{"enabled, in list among many", true, "4.20,4.21", "4.20.5", true},
		{"enabled, not in list", true, "4.21", "4.20.5", false},
		{"enabled, empty list", true, "", "4.20.5", false},
		{"enabled, different minor", true, "4.20", "4.23.0", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.enabled {
				t.Setenv(CPOFromMainEnvVar, "1")
			} else {
				t.Setenv(CPOFromMainEnvVar, "0")
			}
			t.Setenv(CPOFromMainVersionsEnvVar, tc.versions)
			if got := ShouldUseFromMain(semver.MustParse(tc.version)); got != tc.want {
				t.Errorf("ShouldUseFromMain(%s) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

func TestCPOFromMainImage(t *testing.T) {
	v := semver.MustParse("4.20.0")
	const hoImage = "ho-image"

	t.Run("disabled returns empty", func(t *testing.T) {
		t.Setenv(CPOFromMainEnvVar, "0")
		if got := CPOFromMainImage(v, hoImage); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
	t.Run("enabled with explicit image", func(t *testing.T) {
		t.Setenv(CPOFromMainEnvVar, "1")
		t.Setenv(CPOFromMainVersionsEnvVar, "4.20")
		t.Setenv(CPOFromMainImageEnvVar, "my-image")
		if got := CPOFromMainImage(v, hoImage); got != "my-image" {
			t.Errorf("got %q, want my-image", got)
		}
	})
	t.Run("enabled without explicit image falls back to HO image", func(t *testing.T) {
		t.Setenv(CPOFromMainEnvVar, "1")
		t.Setenv(CPOFromMainVersionsEnvVar, "4.20")
		t.Setenv(CPOFromMainImageEnvVar, "")
		if got := CPOFromMainImage(v, hoImage); got != hoImage {
			t.Errorf("got %q, want %q", got, hoImage)
		}
	})
	t.Run("enabled but version not in list", func(t *testing.T) {
		t.Setenv(CPOFromMainEnvVar, "1")
		t.Setenv(CPOFromMainVersionsEnvVar, "4.21")
		t.Setenv(CPOFromMainImageEnvVar, "my-image")
		if got := CPOFromMainImage(v, hoImage); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

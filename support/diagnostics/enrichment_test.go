package diagnostics

import (
	"os"
	"testing"
)

func TestIsEnrichmentDisabled(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{
			name:     "When env var is unset, enrichment should be enabled",
			envValue: "",
			want:     false,
		},
		{
			name:     "When env var is set to '1', enrichment should be disabled",
			envValue: "1",
			want:     true,
		},
		{
			name:     "When env var is set to 'true', enrichment should be disabled",
			envValue: "true",
			want:     true,
		},
		{
			name:     "When env var is set to any non-empty value, enrichment should be disabled",
			envValue: "yes",
			want:     true,
		},
		{
			name:     "When env var is set to 'false', enrichment should be enabled",
			envValue: "false",
			want:     false,
		},
		{
			name:     "When env var is set to '0', enrichment should be enabled",
			envValue: "0",
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Always set/clear the env var for isolation; t.Setenv
			// restores the original value after the subtest.
			t.Setenv(EnvDisableDiagnosticEnrichment, tc.envValue)
			if tc.envValue == "" {
				// t.Setenv sets the var to ""; os.Unsetenv makes it truly absent.
				// t.Setenv already registered the cleanup, so the restore still works.
				if err := os.Unsetenv(EnvDisableDiagnosticEnrichment); err != nil {
					t.Fatalf("failed to unset env var: %v", err)
				}
			}
			got := IsEnrichmentDisabled()
			if got != tc.want {
				t.Errorf("IsEnrichmentDisabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

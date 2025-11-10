package util

import (
	"testing"
)

func TestParseAWSTags(t *testing.T) {
	tests := []struct {
		name        string
		tags        []string
		expected    map[string]string
		expectError bool
		errorSubstr string
	}{
		{
			name:     "When valid unique tags are provided, it should parse successfully",
			tags:     []string{"env=production", "team=platform"},
			expected: map[string]string{"env": "production", "team": "platform"},
		},
		{
			name:     "When empty slice is provided, it should return empty map",
			tags:     []string{},
			expected: map[string]string{},
		},
		{
			name:     "When tag value contains equals sign, it should preserve the value",
			tags:     []string{"config=key=value"},
			expected: map[string]string{"config": "key=value"},
		},
		{
			name:        "When duplicate tag keys are provided, it should return error",
			tags:        []string{"env=production", "env=staging"},
			expectError: true,
			errorSubstr: "duplicate tag key",
		},
		{
			name:        "When malformed tag is provided, it should return error",
			tags:        []string{"invalid-tag"},
			expectError: true,
			errorSubstr: "invalid tag specification",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseAWSTags(tt.tags)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errorSubstr)
					return
				}
				if tt.errorSubstr != "" && !contains(err.Error(), tt.errorSubstr) {
					t.Errorf("expected error containing %q, got %q", tt.errorSubstr, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d tags, got %d", len(tt.expected), len(result))
				return
			}

			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("expected tag %q=%q, got %q=%q", k, v, k, result[k])
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

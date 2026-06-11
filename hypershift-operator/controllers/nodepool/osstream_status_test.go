package nodepool

import (
	"testing"
)

func TestInferOSStreamFromNodeInfo(t *testing.T) {
	tests := []struct {
		name     string
		osImage  string
		expected string
	}{
		{
			name:     "When osImage contains CoreOS 4xx version it should return rhel-9",
			osImage:  "Red Hat Enterprise Linux CoreOS 419.97.202503170921-0 (Plow)",
			expected: "rhel-9",
		},
		{
			name:     "When osImage contains CoreOS 5xx version it should return rhel-10",
			osImage:  "Red Hat Enterprise Linux CoreOS 517.94.202506010000-0 (Plow)",
			expected: "rhel-10",
		},
		{
			name:     "When osImage is empty it should return empty string",
			osImage:  "",
			expected: "",
		},
		{
			name:     "When osImage does not contain CoreOS it should return empty string",
			osImage:  "Ubuntu 22.04.3 LTS",
			expected: "",
		},
		{
			name:     "When osImage contains CoreOS with no version it should return empty string",
			osImage:  "Red Hat Enterprise Linux CoreOS",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := inferOSStreamFromNodeInfo(tc.osImage)
			if result != tc.expected {
				t.Errorf("inferOSStreamFromNodeInfo(%q) = %q, want %q", tc.osImage, result, tc.expected)
			}
		})
	}
}
